/**
 * `/api/session-interchange` — find and import Claude Code / Codex sessions.
 *
 * Export lives on the narrator router (`/api/narrators/:id/session-export*`) so it
 * sits behind the same per-narrator access gate as every other narrator read.
 *
 * Authorization is decided in `resolveSessionLocation` (server: admin; device:
 * manager + authorized user). Upload needs only a signed-in user: the bytes come
 * from the caller's own machine and land in a narrator only they own.
 *
 * Resource bounds, since upload is open to every user:
 *  - the multipart body is capped before it is read;
 *  - every file of one import shares one decoded-bytes budget (`DecodeBudget`),
 *    so many small zstd files cannot each expand to the per-file cap;
 *  - at most one import runs per user at a time.
 */

import {
	type ClaudeCodeSubagentFile,
	parseClaudeCodeSession,
} from "@shared/session-interchange/claude-code/parse";
import { claudeCodeAgentIdFromFileName } from "@shared/session-interchange/claude-code/paths";
import { parseCodexSession } from "@shared/session-interchange/codex/parse";
import {
	type InterchangeFormat,
	type InterchangeLine,
	type InterchangeSession,
	WarningCollector,
} from "@shared/session-interchange/model";
import { type Context, Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { activeDatabaseBackend } from "../db";
import { AppError, ValidationError, zodValidationError } from "../lib/errors";
import {
	sessionImportFromPathSchema,
	sessionImportUploadFieldsSchema,
	sessionScanQuerySchema,
} from "../lib/validators";
import { importInterchangeSession } from "../services/session-interchange/import";
import {
	DecodeBudget,
	decodeSessionBytes,
	SESSION_FILE_MAX_BYTES,
	SESSION_IMPORT_MAX_TOTAL_BYTES,
	SESSION_LINE_MAX_BYTES,
} from "../services/session-interchange/jsonl";
import {
	resolveSessionLocation,
	type SessionPrincipal,
} from "../services/session-interchange/locations";
import { collectSessionFiles, scanSessions } from "../services/session-interchange/scan";

/** Files accepted in one upload: a main transcript plus its subagents. */
const UPLOAD_MAX_FILES = 201;

/** Multipart overhead on top of the session bytes themselves. */
const UPLOAD_BODY_SLACK = 1024 * 1024;

/** The path-import body is a handful of short strings. */
const IMPORT_PATH_BODY_MAX_BYTES = 64 * 1024;

function principalOf(c: Context): SessionPrincipal {
	const user = c.get("user") as { sub?: string; role?: string } | undefined;
	return { userId: user?.sub ?? "", isAdmin: user?.role === "admin" };
}

/** Request-scoped abort: a closed tab stops a long device read or import. */
function requestSignal(c: Context): AbortSignal | undefined {
	return c.req.raw.signal;
}

/**
 * Import writes narrators through SQLite-only transaction helpers. Refuse on the
 * PostgreSQL backend before a body is read, rather than failing after decoding a
 * large upload.
 */
export function requireSqliteSessionInterchange(backend: string = activeDatabaseBackend): void {
	if (backend === "postgres") {
		throw new AppError(
			"Session import is not yet supported on the PostgreSQL backend",
			503,
			"POSTGRES_UNSUPPORTED",
		);
	}
}

/** One import per user at a time: each holds up to the decoded budget in memory. */
const activeImports = new Set<string>();

async function withImportSlot<T>(userId: string, body: () => Promise<T>): Promise<T> {
	if (activeImports.has(userId)) {
		throw new AppError(
			"Another session import is still running; wait for it to finish",
			429,
			"SESSION_IMPORT_BUSY",
		);
	}
	activeImports.add(userId);
	try {
		return await body();
	} finally {
		activeImports.delete(userId);
	}
}

/** Decode one file against the import's shared budget, noting skipped rows. */
async function decodeFile(
	bytes: Uint8Array,
	label: string,
	budget: DecodeBudget,
	warnings: WarningCollector,
	signal?: AbortSignal,
): Promise<InterchangeLine[]> {
	const decoded = await decodeSessionBytes(bytes, { label, budget, signal });
	if (decoded.oversizedLines > 0) {
		warnings.add(
			"unparseable_line",
			`${label}: row over ${Math.round(SESSION_LINE_MAX_BYTES / (1024 * 1024))} MiB`,
			decoded.oversizedLines,
		);
	}
	return decoded.rows;
}

function assembleSession(
	format: InterchangeFormat,
	main: InterchangeLine[],
	subagents: ClaudeCodeSubagentFile[],
	children: Array<{ lines: InterchangeLine[] }>,
	fallbackId: string,
	decodeWarnings: WarningCollector,
): InterchangeSession {
	const session =
		format === "claude-code"
			? parseClaudeCodeSession({ main, subagents, fallbackSessionId: fallbackId })
			: parseCodexSession({ main, children, fallbackThreadId: fallbackId });
	const warnings = new WarningCollector();
	warnings.merge(decodeWarnings.list());
	warnings.merge(session.warnings);
	return { ...session, warnings: warnings.list() };
}

/** First decoded row decides the format: Codex rollouts open with `session_meta`. */
function sniffFormat(rows: InterchangeLine[]): InterchangeFormat | null {
	for (const row of rows.slice(0, 20)) {
		if (typeof row === "string") continue;
		if (row.type === "session_meta" || row.type === "response_item" || row.type === "event_msg") {
			return "codex";
		}
		if (typeof row.uuid === "string" || typeof row.sessionId === "string") return "claude-code";
	}
	return null;
}

export function createSessionInterchangeRoutes() {
	const routes = new Hono();

	routes.use("*", async (_c, next) => {
		requireSqliteSessionInterchange();
		return next();
	});

	routes.get("/scan", async (c) => {
		const parsed = sessionScanQuerySchema.safeParse(c.req.query());
		if (!parsed.success) throw zodValidationError(parsed.error);
		const { format, target, deviceId, root, cursor, limit } = parsed.data;
		const location =
			target === "local" ? { target, root } : { target, deviceId: deviceId ?? "", root };
		const resolved = await resolveSessionLocation(location, format, principalOf(c), {
			signal: requestSignal(c),
		});
		return c.json(await scanSessions(resolved.fs, { format, root: resolved.root, cursor, limit }));
	});

	routes.post(
		"/import/path",
		bodyLimit({
			maxSize: IMPORT_PATH_BODY_MAX_BYTES,
			onError: (c) => c.json({ error: "Request body too large", code: "REQUEST_TOO_LARGE" }, 413),
		}),
		async (c) => {
			const parsed = sessionImportFromPathSchema.safeParse(await c.req.json().catch(() => null));
			if (!parsed.success) throw zodValidationError(parsed.error);
			const input = parsed.data;
			const principal = principalOf(c);
			const signal = requestSignal(c);
			return withImportSlot(principal.userId, async () => {
				const resolved = await resolveSessionLocation(input.location, input.format, principal, {
					signal,
				});
				const files = await collectSessionFiles(resolved.fs, {
					format: input.format,
					root: resolved.root,
					path: input.path,
				});
				const budget = new DecodeBudget();
				const warnings = new WarningCollector();
				const main = await decodeFile(files.main.bytes, "Session file", budget, warnings, signal);
				const subagents: ClaudeCodeSubagentFile[] = [];
				for (const sub of files.subagents) {
					subagents.push({
						lines: await decodeFile(sub.bytes, "Subagent file", budget, warnings, signal),
						...(sub.agentId ? { agentId: sub.agentId } : {}),
						...(sub.agentType ? { agentType: sub.agentType } : {}),
					});
				}
				const children: Array<{ lines: InterchangeLine[] }> = [];
				for (const child of files.children) {
					children.push({
						lines: await decodeFile(child.bytes, "Child thread", budget, warnings, signal),
					});
				}
				const session = assembleSession(
					input.format,
					main,
					subagents,
					children,
					files.sessionId,
					warnings,
				);
				const result = await importInterchangeSession(session, {
					ownerUserId: principal.userId,
					cwd: input.cwd,
					model: input.model,
					title: input.title,
					signal,
					source: {
						format: input.format,
						location:
							input.location.target === "device" ? `device:${input.location.deviceId}` : "server",
					},
				});
				return c.json(result, 201);
			});
		},
	);

	routes.post(
		"/import/upload",
		bodyLimit({
			maxSize: SESSION_IMPORT_MAX_TOTAL_BYTES + UPLOAD_BODY_SLACK,
			onError: (c) =>
				c.json({ error: "Upload exceeds the session size limit", code: "SESSION_TOO_LARGE" }, 413),
		}),
		async (c) => {
			const principal = principalOf(c);
			const signal = requestSignal(c);
			return withImportSlot(principal.userId, async () => {
				const form = await c.req.formData();
				const fields = sessionImportUploadFieldsSchema.safeParse({
					format: form.get("format") ?? undefined,
					cwd: form.get("cwd") ?? undefined,
					model: form.get("model") ?? undefined,
					title: form.get("title") ?? undefined,
				});
				if (!fields.success) throw zodValidationError(fields.error);

				const uploads = form
					.getAll("files")
					.filter((value): value is File => value instanceof File);
				if (uploads.length === 0)
					throw new ValidationError("No session file uploaded (field 'files')");
				if (uploads.length > UPLOAD_MAX_FILES) {
					throw new ValidationError(`At most ${UPLOAD_MAX_FILES} files per upload`);
				}

				// Decode everything first: the main file is the one no other file points at.
				// One budget across all files is what bounds a multi-file zstd bomb.
				const budget = new DecodeBudget();
				const warnings = new WarningCollector();
				const decoded: Array<{ name: string; rows: InterchangeLine[] }> = [];
				// Claude Code keeps each sidechain's agentType in `agent-<id>.meta.json`; those
				// companions are read for that one field and never parsed as transcripts.
				const agentTypes = new Map<string, string>();
				for (const file of uploads) {
					const name = baseName(file.name);
					const metaAgentId = /^agent-([A-Za-z0-9_-]+)\.meta\.json$/.exec(name)?.[1];
					if (metaAgentId) {
						const agentType = file.size <= META_MAX_BYTES ? await readAgentType(file) : undefined;
						if (agentType) agentTypes.set(metaAgentId, agentType);
						continue;
					}
					if (file.size > SESSION_FILE_MAX_BYTES) {
						throw new ValidationError(`${name} exceeds the session file size limit`);
					}
					const bytes = new Uint8Array(await file.arrayBuffer());
					decoded.push({
						name: file.name,
						rows: await decodeFile(bytes, name, budget, warnings, signal),
					});
				}

				const format =
					fields.data.format === "auto"
						? (decoded.map((d) => sniffFormat(d.rows)).find((f) => f) ?? null)
						: fields.data.format;
				if (!format)
					throw new ValidationError(
						"Could not tell whether this is a Claude Code or Codex session",
					);

				const main =
					format === "claude-code"
						? (decoded.find((d) => !claudeCodeAgentIdFromFileName(baseName(d.name))) ?? decoded[0])
						: (decoded.find((d) => !isCodexChild(d.rows)) ?? decoded[0]);
				if (!main) throw new ValidationError("No session file uploaded");
				const rest = decoded.filter((d) => d !== main);
				const session = assembleSession(
					format,
					main.rows,
					format === "claude-code"
						? rest.map((d) => {
								const agentId = claudeCodeAgentIdFromFileName(baseName(d.name));
								const agentType = agentId ? agentTypes.get(agentId) : undefined;
								return {
									lines: d.rows,
									...(agentId ? { agentId } : {}),
									...(agentType ? { agentType } : {}),
								};
							})
						: [],
					format === "codex" ? rest.map((d) => ({ lines: d.rows })) : [],
					baseName(main.name).replace(/\.jsonl(\.zst)?$/i, ""),
					warnings,
				);
				const result = await importInterchangeSession(session, {
					ownerUserId: principal.userId,
					cwd: fields.data.cwd,
					model: fields.data.model,
					title: fields.data.title,
					signal,
					source: { format, location: "upload" },
				});
				return c.json(result, 201);
			});
		},
	);

	return routes;
}

/** A sidechain `.meta.json` is a few dozen bytes; anything much larger is not one. */
const META_MAX_BYTES = 64 * 1024;

async function readAgentType(file: File): Promise<string | undefined> {
	try {
		const meta = JSON.parse(await file.text());
		return typeof meta?.agentType === "string" && meta.agentType.trim()
			? meta.agentType
			: undefined;
	} catch {
		return undefined;
	}
}

function baseName(name: string): string {
	return name.split(/[\\/]/).at(-1) ?? name;
}

/** A Codex child thread names its parent in its first `session_meta`. */
function isCodexChild(rows: InterchangeLine[]): boolean {
	for (const row of rows.slice(0, 5)) {
		if (typeof row === "string" || row.type !== "session_meta") continue;
		const source = (row.payload as { source?: { subagent?: unknown } } | undefined)?.source;
		return !!source?.subagent;
	}
	return false;
}

export const sessionInterchangeRoutes = createSessionInterchangeRoutes();
