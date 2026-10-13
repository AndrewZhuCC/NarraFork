/**
 * Find Claude Code and Codex sessions on the server or a device, and gather one
 * session's files for import.
 *
 * Listing never reads whole transcripts: a candidate's title, cwd and times come
 * from the first and last {@link SESSION_PREVIEW_BYTES} of the file. Paging is by
 * mtime (newest first) with an opaque cursor, and the walk itself is capped, so a
 * config dir with thousands of sessions stays one bounded request.
 *
 * Only files the CLIs themselves would load are offered: `<root>/projects/<dir>/
 * <uuid>.jsonl` for Claude Code and `sessions/**` + `archived_sessions/**`
 * rollouts for Codex. Import takes a path back from the client, so
 * `resolveSessionFile` re-validates it against the same rules instead of trusting
 * that it came from a listing.
 */

import {
	claudeCodeAgentIdFromFileName,
	claudeCodeSessionIdFromFileName,
} from "@shared/session-interchange/claude-code/paths";
import {
	CODEX_ARCHIVED_SESSIONS_DIRNAME,
	CODEX_SESSIONS_DIRNAME,
	parseCodexRolloutFileName,
} from "@shared/session-interchange/codex/paths";
import {
	type InterchangeFormat,
	type InterchangeLine,
	titleFromText,
} from "@shared/session-interchange/model";
import { ValidationError } from "../../lib/errors";
import {
	decodeExcerpt,
	isZstd,
	SESSION_FILE_MAX_BYTES,
	SESSION_IMPORT_MAX_TOTAL_BYTES,
	SESSION_PREVIEW_BYTES,
} from "./jsonl";
import { isSafeRelPath, type SessionFs, type SessionFsEntry } from "./session-fs";

/** Files walked per scan. Beyond this the listing is marked truncated. */
export const SCAN_MAX_FILES = 20_000;

/** Candidates returned per page. */
export const SCAN_PAGE_MAX = 50;

/** Subagent files read per imported session. */
export const IMPORT_MAX_SUBAGENT_FILES = 200;

/** Codex child rollouts are found by reading heads; this caps that work. */
const CODEX_CHILD_PROBE_MAX = 400;

export interface SessionCandidate {
	format: InterchangeFormat;
	/** Absolute path on the target; pass back to import as-is. */
	path: string;
	sessionId: string;
	size: number;
	mtime: string;
	title?: string;
	cwd?: string;
	/** Subagent transcripts found beside it (Claude Code only; Codex counts at import). */
	subagentCount?: number;
	archived?: boolean;
}

export interface SessionCandidatePage {
	root: string;
	candidates: SessionCandidate[];
	nextCursor: string | null;
	/** The walk hit {@link SCAN_MAX_FILES}; older sessions may be missing. */
	truncated: boolean;
}

// ── Candidate discovery ────────────────────────────────────────────────────

interface RawCandidate {
	entry: SessionFsEntry;
	sessionId: string;
	archived?: boolean;
	subagentCount?: number;
}

function claudeCodeCandidates(entries: SessionFsEntry[]): RawCandidate[] {
	const subagentCounts = new Map<string, number>();
	for (const entry of entries) {
		const parts = entry.relPath.split("/");
		// <projectDir>/<sessionId>/subagents/agent-<id>.jsonl
		if (
			parts.length === 4 &&
			parts[2] === "subagents" &&
			claudeCodeAgentIdFromFileName(parts[3] ?? "")
		) {
			const key = `${parts[0]}/${parts[1]}`;
			subagentCounts.set(key, (subagentCounts.get(key) ?? 0) + 1);
		}
	}
	const out: RawCandidate[] = [];
	for (const entry of entries) {
		const parts = entry.relPath.split("/");
		if (parts.length !== 2) continue;
		const sessionId = claudeCodeSessionIdFromFileName(parts[1] ?? "");
		if (!sessionId || entry.size === 0) continue;
		out.push({
			entry,
			sessionId,
			subagentCount: subagentCounts.get(`${parts[0]}/${sessionId}`) ?? 0,
		});
	}
	return out;
}

function codexCandidates(entries: SessionFsEntry[], archived: boolean): RawCandidate[] {
	const out: RawCandidate[] = [];
	for (const entry of entries) {
		const name = entry.relPath.split("/").at(-1) ?? "";
		const parsed = parseCodexRolloutFileName(name);
		if (!parsed || entry.size === 0) continue;
		out.push({ entry, sessionId: parsed.threadId, archived });
	}
	return out;
}

// ── Cursor ─────────────────────────────────────────────────────────────────

interface Cursor {
	mtimeMs: number;
	relPath: string;
}

function encodeCursor(cursor: Cursor): string {
	return Buffer.from(JSON.stringify(cursor)).toString("base64url");
}

function decodeCursor(value: string | undefined): Cursor | null {
	if (!value) return null;
	try {
		const parsed = JSON.parse(Buffer.from(value, "base64url").toString("utf8"));
		if (typeof parsed?.mtimeMs === "number" && typeof parsed?.relPath === "string") return parsed;
	} catch {
		// Fall through.
	}
	throw new ValidationError("Invalid scan cursor");
}

/** Newest first; relPath breaks ties so the order (and the cursor) is total. */
function compareCandidates(a: SessionFsEntry, b: SessionFsEntry): number {
	return b.mtimeMs - a.mtimeMs || a.relPath.localeCompare(b.relPath);
}

function isAfterCursor(entry: SessionFsEntry, cursor: Cursor): boolean {
	return (
		compareCandidates(entry, { relPath: cursor.relPath, size: 0, mtimeMs: cursor.mtimeMs }) > 0
	);
}

// ── Previews ───────────────────────────────────────────────────────────────

function str(value: unknown): string | undefined {
	return typeof value === "string" && value.trim() ? value : undefined;
}

function firstUserPromptCC(rows: InterchangeLine[]): string | undefined {
	for (const row of rows) {
		if (typeof row === "string" || row.type !== "user" || row.isMeta === true) continue;
		const content = (row.message as { content?: unknown } | undefined)?.content;
		const text =
			typeof content === "string"
				? content
				: Array.isArray(content)
					? content.find((b) => b?.type === "text" && typeof b.text === "string")?.text
					: undefined;
		// Local slash-command echoes are wrapped in tags and make useless titles.
		if (typeof text === "string" && text.trim() && !text.trimStart().startsWith("<")) return text;
	}
	return undefined;
}

function previewClaudeCode(head: InterchangeLine[], tail: InterchangeLine[]) {
	let cwd: string | undefined;
	let title: string | undefined;
	for (const row of [...head, ...tail]) {
		if (typeof row === "string") continue;
		cwd = str(row.cwd) ?? cwd;
		if (row.type === "custom-title") title = str(row.customTitle) ?? title;
		else if (row.type === "ai-title" && !title) title = str(row.aiTitle);
	}
	return { cwd, title: title ?? titleFromText(firstUserPromptCC(head)) };
}

function previewCodex(head: InterchangeLine[]) {
	let cwd: string | undefined;
	let prompt: string | undefined;
	for (const row of head) {
		if (typeof row === "string") continue;
		const payload = row.payload as Record<string, unknown> | undefined;
		if (row.type === "session_meta" && !cwd) cwd = str(payload?.cwd);
		if (row.type === "event_msg" && payload?.type === "user_message" && !prompt) {
			prompt = str(payload.message);
		}
	}
	return { cwd, title: titleFromText(prompt) };
}

async function preview(
	fs: SessionFs,
	format: InterchangeFormat,
	path: string,
	size: number,
): Promise<{ cwd?: string; title?: string }> {
	try {
		const head = await fs.readHead(path, Math.min(size, SESSION_PREVIEW_BYTES));
		// A compressed rollout cannot be previewed from an excerpt; its name still
		// carries the id and time, which is enough to pick it.
		if (isZstd(head)) return {};
		const headRows = decodeExcerpt(head, { atStart: false, atEnd: size > head.byteLength });
		if (format === "codex") return previewCodex(headRows);
		let tailRows: InterchangeLine[] = [];
		if (size > SESSION_PREVIEW_BYTES && fs.readTail) {
			const tail = await fs.readTail(path, SESSION_PREVIEW_BYTES);
			tailRows = decodeExcerpt(tail, { atStart: true, atEnd: false });
		}
		return previewClaudeCode(headRows, tailRows);
	} catch {
		// A file that vanished or cannot be read still lists; import will report it.
		return {};
	}
}

// ── Listing ────────────────────────────────────────────────────────────────

export async function scanSessions(
	fs: SessionFs,
	input: { format: InterchangeFormat; root: string; cursor?: string; limit?: number },
): Promise<SessionCandidatePage> {
	const limit = Math.min(Math.max(1, input.limit ?? 20), SCAN_PAGE_MAX);
	const cursor = decodeCursor(input.cursor);
	let raw: RawCandidate[];
	let truncated: boolean;
	let base: (relPath: string, archived?: boolean) => string;

	if (input.format === "claude-code") {
		const projects = fs.join(input.root, "projects");
		const walk = await fs.walk(projects, { maxDepth: 4, maxEntries: SCAN_MAX_FILES });
		raw = claudeCodeCandidates(walk.entries);
		truncated = walk.truncated;
		base = (relPath) => fs.join(projects, relPath);
	} else {
		const live = fs.join(input.root, CODEX_SESSIONS_DIRNAME);
		const archive = fs.join(input.root, CODEX_ARCHIVED_SESSIONS_DIRNAME);
		const [liveWalk, archiveWalk] = await Promise.all([
			fs.walk(live, { maxDepth: 4, maxEntries: SCAN_MAX_FILES }),
			fs.walk(archive, { maxDepth: 4, maxEntries: SCAN_MAX_FILES }),
		]);
		// Archived paths get a prefix so relPaths (and so cursors) stay unique.
		raw = [
			...codexCandidates(liveWalk.entries, false),
			...codexCandidates(
				archiveWalk.entries.map((e) => ({ ...e, relPath: `@archived/${e.relPath}` })),
				true,
			),
		];
		truncated = liveWalk.truncated || archiveWalk.truncated;
		base = (relPath) =>
			relPath.startsWith("@archived/")
				? fs.join(archive, relPath.slice("@archived/".length))
				: fs.join(live, relPath);
	}

	// The CLIs only ever create plain names (Claude Code sanitizes project dirs to
	// `[A-Za-z0-9-]`); anything else was made by hand and is skipped, not fatal.
	const sorted = raw
		.filter((c) => isSafeRelPath(c.entry.relPath.replace(/^@archived\//, "")))
		.sort((a, b) => compareCandidates(a.entry, b.entry));
	const after = cursor ? sorted.filter((c) => isAfterCursor(c.entry, cursor)) : sorted;
	const page = after.slice(0, limit + 1);
	const hasMore = page.length > limit;
	if (hasMore) page.length = limit;

	const candidates: SessionCandidate[] = [];
	for (const item of page) {
		const path = base(item.entry.relPath);
		candidates.push({
			format: input.format,
			path,
			sessionId: item.sessionId,
			size: item.entry.size,
			mtime: new Date(item.entry.mtimeMs).toISOString(),
			...(item.subagentCount ? { subagentCount: item.subagentCount } : {}),
			...(item.archived ? { archived: true } : {}),
			...(await preview(fs, input.format, path, item.entry.size)),
		});
	}
	const last = page.at(-1);
	return {
		root: input.root,
		candidates,
		nextCursor:
			hasMore && last
				? encodeCursor({ mtimeMs: last.entry.mtimeMs, relPath: last.entry.relPath })
				: null,
		truncated,
	};
}

// ── Gathering one session for import ───────────────────────────────────────

export interface SessionFileSet {
	format: InterchangeFormat;
	sessionId: string;
	main: { path: string; bytes: Uint8Array };
	/** Claude Code: sidechain transcripts with their `.meta.json` agentType. */
	subagents: Array<{ path: string; bytes: Uint8Array; agentId?: string; agentType?: string }>;
	/** Codex: rollouts that may be children; the parser links the real ones. */
	children: Array<{ path: string; bytes: Uint8Array }>;
}

export function resolveSessionFile(
	fs: SessionFs,
	format: InterchangeFormat,
	root: string,
	path: string,
): { sessionId: string; path: string; dir: string } {
	const separators = fs.flavor === "windows" ? /[\\/]+/ : /\/+/;
	const rootParts = root.split(separators).filter(Boolean);
	const parts = path.split(separators).filter(Boolean);
	const same = (a: string | undefined, b: string | undefined) =>
		fs.flavor === "windows" ? a?.toLowerCase() === b?.toLowerCase() : a === b;
	const insideRoot =
		parts.length > rootParts.length && rootParts.every((part, index) => same(part, parts[index]));
	const rel = parts.slice(rootParts.length);
	if (!insideRoot || !isSafeRelPath(rel.join("/"))) {
		throw new ValidationError("Session path is outside the session directory");
	}
	const fileName = rel.at(-1) ?? "";
	let sessionId: string | null;
	if (format === "claude-code") {
		sessionId = claudeCodeSessionIdFromFileName(fileName);
		if (rel.length !== 3 || rel[0] !== "projects" || !sessionId) {
			throw new ValidationError("Not a Claude Code session transcript");
		}
	} else {
		const top = rel[0];
		sessionId = parseCodexRolloutFileName(fileName)?.threadId ?? null;
		if (
			!sessionId ||
			rel.length < 2 ||
			rel.length > 5 ||
			(top !== CODEX_SESSIONS_DIRNAME && top !== CODEX_ARCHIVED_SESSIONS_DIRNAME)
		) {
			throw new ValidationError("Not a Codex rollout file");
		}
	}
	// Rebuilt from the validated segments under the server-chosen root, so what is
	// read is exactly what was checked, never the client's spelling of it.
	const resolved = fs.join(root, rel.join("/"));
	return { sessionId, path: resolved, dir: fs.dirname(resolved) };
}

class ByteBudget {
	private used = 0;
	charge(bytes: number): void {
		this.used += bytes;
		if (this.used > SESSION_IMPORT_MAX_TOTAL_BYTES) {
			throw new ValidationError(
				`Session and its subagents exceed ${Math.round(SESSION_IMPORT_MAX_TOTAL_BYTES / (1024 * 1024))} MiB`,
			);
		}
	}
}

function parseAgentType(bytes: Uint8Array): string | undefined {
	try {
		const meta = JSON.parse(new TextDecoder().decode(bytes));
		return typeof meta?.agentType === "string" ? meta.agentType : undefined;
	} catch {
		return undefined;
	}
}

/** Head of a rollout, decoded far enough to read its `session_meta`. */
async function codexParentOf(
	fs: SessionFs,
	path: string,
	size: number,
): Promise<string | undefined> {
	const head = await fs.readHead(path, Math.min(size, SESSION_PREVIEW_BYTES));
	if (isZstd(head)) return undefined;
	for (const row of decodeExcerpt(head, { atStart: false, atEnd: size > head.byteLength })) {
		if (typeof row === "string" || row.type !== "session_meta") continue;
		const spawn = (
			row.payload as { source?: { subagent?: { thread_spawn?: { parent_thread_id?: unknown } } } }
		)?.source?.subagent?.thread_spawn;
		return typeof spawn?.parent_thread_id === "string"
			? spawn.parent_thread_id.toLowerCase()
			: undefined;
	}
	return undefined;
}

export async function collectSessionFiles(
	fs: SessionFs,
	input: { format: InterchangeFormat; root: string; path: string },
): Promise<SessionFileSet> {
	const resolved = resolveSessionFile(fs, input.format, input.root, input.path);
	const { sessionId, dir } = resolved;
	const budget = new ByteBudget();
	const mainBytes = await fs.readAll(resolved.path, SESSION_FILE_MAX_BYTES);
	budget.charge(mainBytes.byteLength);

	if (input.format === "claude-code") {
		const subDir = fs.join(dir, `${sessionId}/subagents`);
		const walk = await fs.walk(subDir, {
			maxDepth: 1,
			maxEntries: IMPORT_MAX_SUBAGENT_FILES * 2 + 1,
		});
		const metaByAgent = new Map<string, SessionFsEntry>();
		for (const entry of walk.entries) {
			const match = /^agent-(.+)\.meta\.json$/.exec(entry.relPath);
			if (match?.[1]) metaByAgent.set(match[1], entry);
		}
		const subagents: SessionFileSet["subagents"] = [];
		for (const entry of walk.entries) {
			const agentId = claudeCodeAgentIdFromFileName(entry.relPath);
			if (!agentId) continue;
			if (subagents.length >= IMPORT_MAX_SUBAGENT_FILES) break;
			const path = fs.join(subDir, entry.relPath);
			const bytes = await fs.readAll(path, SESSION_FILE_MAX_BYTES);
			budget.charge(bytes.byteLength);
			const meta = metaByAgent.get(agentId);
			const agentType =
				meta && meta.size <= 64 * 1024
					? parseAgentType(await fs.readAll(fs.join(subDir, meta.relPath), 64 * 1024))
					: undefined;
			subagents.push({ path, bytes, agentId, ...(agentType ? { agentType } : {}) });
		}
		return {
			format: "claude-code",
			sessionId,
			main: { path: resolved.path, bytes: mainBytes },
			subagents,
			children: [],
		};
	}

	// Codex writes a child rollout beside its parent's (same day folder, since the
	// child starts while the parent runs); its session_meta names the parent. Only
	// the parent's folder is probed, which keeps this bounded without an index.
	const walk = await fs.walk(dir, { maxDepth: 1, maxEntries: CODEX_CHILD_PROBE_MAX });
	const children: SessionFileSet["children"] = [];
	for (const entry of walk.entries) {
		const parsed = parseCodexRolloutFileName(entry.relPath);
		if (!parsed || parsed.threadId === sessionId) continue;
		if (children.length >= IMPORT_MAX_SUBAGENT_FILES) break;
		const path = fs.join(dir, entry.relPath);
		if ((await codexParentOf(fs, path, entry.size).catch(() => undefined)) !== sessionId) continue;
		const bytes = await fs.readAll(path, SESSION_FILE_MAX_BYTES);
		budget.charge(bytes.byteLength);
		children.push({ path, bytes });
	}
	return {
		format: "codex",
		sessionId,
		main: { path: resolved.path, bytes: mainBytes },
		subagents: [],
		children,
	};
}
