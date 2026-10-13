/**
 * Where an exported session goes: a CLI's config dir on the server or a device,
 * or a download.
 *
 * Writes are all-or-nothing from the user's point of view. Every file of a session
 * is created fresh (never overwriting), and a failure part-way removes the files
 * this export already created, so a CLI never lists a session whose sidechains or
 * child threads are missing.
 *
 * Only paths the exporter computed are written, under a root the route resolved;
 * nothing here accepts a destination path from the client.
 */

import { type InterchangeFormat, WarningCollector } from "@shared/session-interchange/model";
import archiver from "archiver";
import type { SessionExportFile, SessionExportResult } from "./export";
import type { SessionFs } from "./session-fs";

export interface WrittenSession {
	root: string;
	/** Absolute paths on the target, in write order. */
	paths: string[];
	/** False when the session's cwd was not found on the target (written anyway). */
	cwdExists: boolean;
	warnings: SessionExportResult["warnings"];
}

export async function writeSessionExport(
	fs: SessionFs,
	root: string,
	result: SessionExportResult,
): Promise<WrittenSession> {
	const warnings = new WarningCollector();
	warnings.merge(result.warnings);

	// Resuming needs the CLI started in the session's cwd. A missing cwd is written
	// anyway (the user may create it, or be exporting ahead of a checkout) but it
	// is reported so the resume command does not fail as a surprise.
	const cwdStat = await fs.stat(result.cwd).catch(() => null);
	const cwdExists = cwdStat?.isDirectory === true;
	if (!cwdExists) warnings.add("target_cwd_missing", result.cwd);

	const created: string[] = [];
	const appends: SessionExportFile[] = [];
	try {
		for (const file of result.files) {
			if (file.mode === "append") {
				appends.push(file);
				continue;
			}
			const path = fs.join(root, file.relativePath.join("/"));
			await fs.mkdirp(fs.dirname(path));
			await fs.createFile(path, new TextEncoder().encode(file.content));
			created.push(path);
		}
	} catch (error) {
		for (const path of created.reverse()) await fs.removeFile(path).catch(() => undefined);
		throw error;
	}

	// The index is a shared, append-only file the CLI also writes. It only supplies
	// a title, so it is skipped where appending is not atomic rather than risking a
	// read-modify-write race with a running Codex.
	const paths = [...created];
	for (const file of appends) {
		const path = fs.join(root, file.relativePath.join("/"));
		if (!fs.appendFile) {
			warnings.add("session_index_skipped", file.relativePath.join("/"));
			continue;
		}
		try {
			await fs.appendFile(path, new TextEncoder().encode(file.content));
			paths.push(path);
		} catch (error) {
			warnings.add("session_index_skipped", error instanceof Error ? error.message : String(error));
		}
	}
	return { root, paths, cwdExists, warnings: warnings.list() };
}

// ── Download ───────────────────────────────────────────────────────────────

export interface SessionDownload {
	fileName: string;
	contentType: string;
	body: ReadableStream<Uint8Array>;
	/**
	 * Where the file goes, relative to the CLI's config dir (`projects/<dir>/<id>.jsonl`,
	 * `sessions/YYYY/MM/DD/rollout-*.jsonl`). Only for a single file: a browser
	 * download strips the folders, so the user has to be told where to put it. A
	 * zip already carries its layout, and extracting it into the config dir is enough.
	 */
	placement: string | null;
}

function safeName(value: string | undefined, fallback: string): string {
	const cleaned = (value ?? "")
		.normalize("NFKC")
		.replace(/[^\p{L}\p{N}._-]+/gu, "-")
		.replace(/^-+|-+$/g, "")
		.slice(0, 60);
	return cleaned || fallback;
}

/**
 * A single session file downloads as-is; a session with sidechains or child
 * threads downloads as a zip laid out exactly as the files sit under the CLI's
 * config dir, so extracting it at `~/.claude` or `~/.codex` installs the session.
 *
 * The Codex index row is left out: appended to a real index it is useful, but as
 * a lone file extracted over one it would replace the user's whole index.
 */
export function buildSessionDownload(result: SessionExportResult): SessionDownload {
	const files = result.files.filter((file) => file.mode === "create");
	const base = `${safeName(result.title, result.format)}-${result.sessionId.slice(0, 8)}`;
	const only = files.length === 1 ? files[0] : undefined;
	if (only) {
		const bytes = new TextEncoder().encode(only.content);
		return {
			fileName: only.relativePath.at(-1) ?? `${base}.jsonl`,
			contentType: "application/x-ndjson; charset=utf-8",
			placement: only.relativePath.join("/"),
			body: new ReadableStream({
				start(controller) {
					controller.enqueue(bytes);
					controller.close();
				},
			}),
		};
	}

	const archive = archiver("zip", { zlib: { level: 6 } });
	const body = new ReadableStream<Uint8Array>({
		start(controller) {
			archive.on("data", (chunk: Buffer) => controller.enqueue(new Uint8Array(chunk)));
			archive.on("end", () => controller.close());
			archive.on("error", (error: Error) => controller.error(error));
			for (const file of files) {
				archive.append(file.content, { name: file.relativePath.join("/") });
			}
			void archive.finalize();
		},
		cancel() {
			archive.abort();
		},
	});
	return { fileName: `${base}.zip`, contentType: "application/zip", body, placement: null };
}

export function formatConfigDirName(format: InterchangeFormat): string {
	return format === "claude-code" ? ".claude" : ".codex";
}
