/**
 * Filesystem access for session import/export, on the server or on a remote device.
 *
 * Scan, import and export share one small interface so their path rules (which
 * files may be read, where exports may land) are written once and apply to both
 * machines. Every method is bounded: listings cap their entry count, reads cap
 * their byte count, and local walks never follow symlinks out of the tree.
 *
 * Paths handed to callers are absolute on the target machine. Relative paths are
 * always slash-separated, whatever the target's own separator is.
 */

import { randomBytes } from "node:crypto";
import type { Dirent } from "node:fs";
import { appendFile, link, lstat, mkdir, open, readdir, rm, stat, unlink } from "node:fs/promises";
import { homedir } from "node:os";
import { dirname, isAbsolute, join, posix, resolve, win32 } from "node:path";
import type { ReadBytesResult } from "../../lib/agent/execution/backend";
import type { TransferStatResult } from "../../lib/agent/execution/rpc-types";
import { ValidationError } from "../../lib/errors";
import { getNarraforkPath } from "../../lib/narrafork-home";

export type PathFlavor = "posix" | "windows";

export interface SessionFsEntry {
	/** Slash-separated, relative to the walked directory. */
	relPath: string;
	size: number;
	mtimeMs: number;
}

export interface SessionFsWalk {
	entries: SessionFsEntry[];
	/** The entry cap was reached; the listing is a prefix. */
	truncated: boolean;
}

export interface SessionFsStat {
	isFile: boolean;
	isDirectory: boolean;
	size: number;
	mtimeMs: number;
}

export interface SessionFs {
	readonly target: "local" | "device";
	readonly deviceId: string | null;
	readonly flavor: PathFlavor;
	/** Absolute path of a slash-separated relative path under `root`. */
	join(root: string, relPath: string): string;
	dirname(path: string): string;
	/** Regular files under `dir`, at most `maxDepth` levels down. Missing dir → empty. */
	walk(dir: string, opts: { maxDepth: number; maxEntries: number }): Promise<SessionFsWalk>;
	stat(path: string): Promise<SessionFsStat | null>;
	/** The first `bytes` bytes. */
	readHead(path: string, bytes: number): Promise<Uint8Array>;
	/** The last `bytes` bytes, when the target can read at an offset. */
	readTail?(path: string, bytes: number): Promise<Uint8Array>;
	/** The whole file; fails when it is larger than `maxBytes`. */
	readAll(path: string, maxBytes: number): Promise<Uint8Array>;
	mkdirp(path: string): Promise<void>;
	/** Create a new file; fails with {@link FileExistsError} when `path` exists. */
	createFile(path: string, bytes: Uint8Array): Promise<void>;
	/** Append to a file, creating it; only where the target can append atomically. */
	appendFile?(path: string, bytes: Uint8Array): Promise<void>;
	removeFile(path: string): Promise<void>;
}

export class FileExistsError extends ValidationError {
	constructor(path: string) {
		super(`Refusing to overwrite an existing file: ${path}`);
	}
}

function tooLarge(path: string, maxBytes: number): ValidationError {
	return new ValidationError(
		`Session file is larger than ${Math.round(maxBytes / (1024 * 1024))} MiB: ${path}`,
	);
}

// ── Path helpers ───────────────────────────────────────────────────────────

function pathApi(flavor: PathFlavor) {
	return flavor === "windows" ? win32 : posix;
}

/** Plain names only: every path this module writes is computed, never user text. */
const SAFE_SEGMENT = /^[A-Za-z0-9._-]+$/;

export function isSafeRelPath(relPath: string): boolean {
	return relPath
		.split("/")
		.every((segment) => SAFE_SEGMENT.test(segment) && segment !== "." && segment !== "..");
}

export function assertSafeRelPath(relPath: string): string[] {
	if (!isSafeRelPath(relPath)) throw new ValidationError(`Unsafe session path: ${relPath}`);
	return relPath.split("/");
}

function joinUnder(flavor: PathFlavor, root: string, relPath: string): string {
	return pathApi(flavor).join(root, ...assertSafeRelPath(relPath));
}

export function isAbsoluteFor(flavor: PathFlavor, path: string): boolean {
	if (flavor === "windows") {
		// Drive-qualified or UNC only: `\foo` is relative to the current drive.
		return /^[A-Za-z]:[\\/]/.test(path) || /^\\\\[^\\]+\\[^\\]+/.test(path);
	}
	return posix.isAbsolute(path);
}

/**
 * The home directory implied by a device's default working directory.
 *
 * Devices report their default cwd, not their home, and the CLIs keep their data
 * under the home. Only unambiguous layouts are recognized; anything else makes the
 * caller ask for the root instead of guessing and writing somewhere unexpected.
 */
export function inferRemoteHome(defaultCwd: string | null | undefined, os: string): string | null {
	const cwd = defaultCwd?.trim();
	if (!cwd) return null;
	if (os === "windows") {
		const match = /^([A-Za-z]:\\Users\\[^\\]+)(?:\\|$)/i.exec(cwd.replace(/\//g, "\\"));
		return match?.[1] ?? null;
	}
	if (cwd === "/root" || cwd.startsWith("/root/")) return "/root";
	const match = /^(\/(?:home|Users)\/[^/]+)(?:\/|$)/.exec(cwd);
	return match?.[1] ?? null;
}

// ── Local (NarraFork server) ───────────────────────────────────────────────

async function readRange(path: string, position: number, length: number): Promise<Uint8Array> {
	const handle = await open(path, "r");
	try {
		const buffer = Buffer.alloc(length);
		let read = 0;
		while (read < length) {
			const { bytesRead } = await handle.read(buffer, read, length - read, position + read);
			if (bytesRead === 0) break;
			read += bytesRead;
		}
		return new Uint8Array(buffer.buffer, buffer.byteOffset, read);
	} finally {
		await handle.close();
	}
}

/**
 * Write to a sibling temp file, then hard-link it into place.
 *
 * A reader (a running CLI listing its sessions) never sees a half-written file,
 * and `link` refuses an existing target where `rename` would silently replace it.
 */
async function createExclusive(path: string, bytes: Uint8Array): Promise<void> {
	const temp = `${path}.${randomBytes(6).toString("hex")}.nftmp`;
	const handle = await open(temp, "wx", 0o600);
	try {
		await handle.writeFile(bytes);
		await handle.sync().catch(() => undefined);
	} finally {
		await handle.close();
	}
	try {
		await link(temp, path);
	} catch (error) {
		const code = (error as NodeJS.ErrnoException).code;
		if (code === "EEXIST") throw new FileExistsError(path);
		if (code !== "EPERM" && code !== "ENOTSUP" && code !== "EXDEV" && code !== "ENOSYS") {
			throw error;
		}
		// Filesystems without hard links: an exclusive create still never overwrites.
		const target = await open(path, "wx", 0o600).catch((openError) => {
			if ((openError as NodeJS.ErrnoException).code === "EEXIST") throw new FileExistsError(path);
			throw openError;
		});
		try {
			await target.writeFile(bytes);
		} catch (writeError) {
			// This call created the file, so a failed write must not leave it behind:
			// the caller only records paths that were created successfully.
			await target.close().catch(() => undefined);
			await unlink(path).catch(() => undefined);
			throw writeError;
		}
		await target.close();
	} finally {
		await unlink(temp).catch(() => undefined);
	}
}

export function createLocalSessionFs(): SessionFs {
	const flavor: PathFlavor = process.platform === "win32" ? "windows" : "posix";
	return {
		target: "local",
		deviceId: null,
		flavor,
		join: (root, relPath) => joinUnder(flavor, root, relPath),
		dirname: (path) => dirname(path),
		async walk(dir, opts) {
			const entries: SessionFsEntry[] = [];
			let truncated = false;
			const visit = async (absolute: string, rel: string, depth: number): Promise<void> => {
				let children: Dirent[];
				try {
					children = await readdir(absolute, { withFileTypes: true });
				} catch (error) {
					const code = (error as NodeJS.ErrnoException).code;
					if (code === "ENOENT" || code === "ENOTDIR" || code === "EACCES") return;
					throw error;
				}
				for (const child of children) {
					if (truncated) return;
					// Skipped, not followed: a link out of the tree must not widen a scan.
					if (child.isSymbolicLink()) continue;
					const childRel = rel ? `${rel}/${child.name}` : child.name;
					const childAbs = join(absolute, child.name);
					if (child.isDirectory()) {
						if (depth < opts.maxDepth) await visit(childAbs, childRel, depth + 1);
						continue;
					}
					if (!child.isFile()) continue;
					if (entries.length >= opts.maxEntries) {
						truncated = true;
						return;
					}
					const info = await lstat(childAbs).catch(() => null);
					if (info?.isFile()) {
						entries.push({ relPath: childRel, size: info.size, mtimeMs: info.mtimeMs });
					}
				}
			};
			await visit(dir, "", 1);
			return { entries, truncated };
		},
		async stat(path) {
			const info = await stat(path).catch(() => null);
			return info
				? {
						isFile: info.isFile(),
						isDirectory: info.isDirectory(),
						size: info.size,
						mtimeMs: info.mtimeMs,
					}
				: null;
		},
		readHead: (path, bytes) => readRange(path, 0, bytes),
		async readTail(path, bytes) {
			const info = await stat(path);
			const start = Math.max(0, info.size - bytes);
			return readRange(path, start, info.size - start);
		},
		async readAll(path, maxBytes) {
			const info = await stat(path);
			if (!info.isFile()) throw new ValidationError(`Not a file: ${path}`);
			if (info.size > maxBytes) throw tooLarge(path, maxBytes);
			return readRange(path, 0, info.size);
		},
		async mkdirp(path) {
			await mkdir(path, { recursive: true });
		},
		createFile: createExclusive,
		async appendFile(path, bytes) {
			await appendFile(path, bytes, { mode: 0o600 });
		},
		async removeFile(path) {
			await rm(path, { force: true });
		},
	};
}

/** `CLAUDE_CONFIG_DIR` or `~/.claude`, resolved as the CLI does on this server. */
export function localClaudeCodeRoot(): string {
	const env = process.env.CLAUDE_CONFIG_DIR?.trim();
	return env && isAbsolute(env) ? resolve(env) : join(homedir(), ".claude");
}

/** `CODEX_HOME` or `~/.codex`, resolved as the CLI does on this server. */
export function localCodexRoot(): string {
	const env = process.env.CODEX_HOME?.trim();
	return env && isAbsolute(env) ? resolve(env) : join(homedir(), ".codex");
}

// ── Remote device ──────────────────────────────────────────────────────────

/**
 * The device operations this module needs, as a port so tests can supply an
 * in-memory device. The production port is `createDeviceSessionFsPort`.
 */
export interface DeviceSessionFsPort {
	deviceId: string;
	platformOs: string;
	/** Default cwd the device reported, used to infer its home directory. */
	defaultCwd: string | null;
	/** Largest payload one file RPC may carry; bigger files use a chunked transfer. */
	maxRpcBytes: number;
	/** `transfer.stat`: metadata, or the whole subtree when `recursive`. */
	stat(
		path: string,
		opts: { recursive?: boolean; maxEntries?: number },
	): Promise<TransferStatResult>;
	read(path: string, maxBytes: number): Promise<ReadBytesResult>;
	write(path: string, bytes: Uint8Array): Promise<void>;
	mkdirp(path: string): Promise<void>;
	remove(path: string): Promise<void>;
	/** Chunked download to a local file, for files over `maxRpcBytes`. */
	download?(path: string, size: number, mtimeMs: number, localDest: string): Promise<void>;
	/** Chunked upload from a local file, for files over `maxRpcBytes`. */
	upload?(localPath: string, remoteDest: string): Promise<void>;
}

function isNotFound(error: unknown): boolean {
	return /not found|no such file|does not exist|ENOENT/i.test(String(error));
}

async function withScratchFile<T>(body: (path: string) => Promise<T>): Promise<T> {
	const path = getNarraforkPath(
		"tmp",
		"session-interchange",
		`${randomBytes(8).toString("hex")}.bin`,
	);
	await mkdir(dirname(path), { recursive: true });
	try {
		return await body(path);
	} finally {
		// The transfer service keeps resume state beside its destination.
		for (const suffix of ["", ".nfpart", ".nfmeta", ".nfmeta.tmp"]) {
			await rm(`${path}${suffix}`, { force: true }).catch(() => undefined);
		}
	}
}

export function createDeviceSessionFs(port: DeviceSessionFsPort): SessionFs {
	const flavor: PathFlavor = port.platformOs === "windows" ? "windows" : "posix";
	const api = pathApi(flavor);
	// base64 inflates a write by a third; stay comfortably under the RPC cap.
	const inlineWriteMax = Math.floor(port.maxRpcBytes * 0.7);

	const statPath = async (path: string): Promise<SessionFsStat | null> => {
		try {
			const info = await port.stat(path, {});
			if (!info.exists) return null;
			return {
				isFile: !info.isDirectory,
				isDirectory: info.isDirectory,
				size: info.size,
				mtimeMs: info.mtimeMs,
			};
		} catch (error) {
			if (isNotFound(error)) return null;
			throw error;
		}
	};

	return {
		target: "device",
		deviceId: port.deviceId,
		flavor,
		join: (root, relPath) => joinUnder(flavor, root, relPath),
		dirname: (path) => api.dirname(path),
		async walk(dir, opts) {
			// One recursive stat for the tree instead of a listing per directory: a
			// Claude Code projects dir has hundreds of subdirectories.
			let result: TransferStatResult;
			try {
				result = await port.stat(dir, { recursive: true, maxEntries: opts.maxEntries });
			} catch (error) {
				if (isNotFound(error)) return { entries: [], truncated: false };
				throw error;
			}
			if (!result.exists || !result.isDirectory) return { entries: [], truncated: false };
			const entries: SessionFsEntry[] = [];
			for (const entry of result.entries ?? []) {
				if (entry.isDirectory) continue;
				const relPath = entry.relPath.replace(/\\/g, "/");
				if (relPath.split("/").length > opts.maxDepth) continue;
				entries.push({ relPath, size: entry.size, mtimeMs: entry.mtimeMs });
			}
			return { entries, truncated: result.truncated === true };
		},
		stat: statPath,
		async readHead(path, bytes) {
			return (await port.read(path, bytes)).bytes;
		},
		async readAll(path, maxBytes) {
			const info = await statPath(path);
			if (!info?.isFile) throw new ValidationError(`Not a file: ${path}`);
			if (info.size > maxBytes) throw tooLarge(path, maxBytes);
			if (info.size <= port.maxRpcBytes) {
				const read = await port.read(path, Math.max(1, info.size));
				if (read.truncated) throw new ValidationError(`Device returned a partial read of ${path}`);
				return read.bytes;
			}
			const download = port.download;
			if (!download) throw tooLarge(path, port.maxRpcBytes);
			return withScratchFile(async (local) => {
				await download(path, info.size, info.mtimeMs, local);
				return readRange(local, 0, info.size);
			});
		},
		mkdirp: (path) => port.mkdirp(path),
		async createFile(path, bytes) {
			// No exclusive-create RPC exists. The check still means something: every
			// exported file name carries a fresh UUID, so a hit is a real collision.
			if (await statPath(path)) throw new FileExistsError(path);
			if (bytes.byteLength <= inlineWriteMax) {
				await port.write(path, bytes);
				return;
			}
			const upload = port.upload;
			if (!upload) {
				throw new ValidationError(`Exported file is too large for a single device write: ${path}`);
			}
			await withScratchFile(async (local) => {
				await Bun.write(local, bytes);
				try {
					await upload(local, path);
				} catch (error) {
					// A failed chunked upload can leave the target and its resume state on the
					// device; the caller only rolls back paths that were fully created.
					for (const suffix of ["", ".nfpart", ".nfmeta", ".nfmeta.tmp"]) {
						await port.remove(`${path}${suffix}`).catch(() => undefined);
					}
					throw error;
				}
			});
		},
		removeFile: (path) => port.remove(path),
	};
}
