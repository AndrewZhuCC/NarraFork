/**
 * Local filesystem storage backend.
 */

import { createHash, randomUUID } from "node:crypto";
import {
	existsSync,
	mkdirSync,
	readdirSync,
	readFileSync,
	rmSync,
	statSync,
	unlinkSync,
} from "node:fs";
import { mkdir, open, opendir, rename, stat, statfs, unlink } from "node:fs/promises";
import { dirname, isAbsolute, join, relative, resolve as resolvePath } from "node:path";
import { MAX_MIRROR_UPLOAD_CONCURRENCY } from "../../shared/mirror-relay";
import type { StorageBackend, VerifiedFileOptions } from "./types";

interface MirrorReservation {
	size: number;
	quotaGrowth: number;
	physicalReserved: boolean;
}

/** Thrown when a storage path would escape the configured base directory. */
export class UnsafeStoragePathError extends Error {
	constructor(path: string) {
		super(`Unsafe storage path: ${path}`);
		this.name = "UnsafeStoragePathError";
	}
}

async function abortable<T>(operation: Promise<T>, signal: AbortSignal): Promise<T> {
	if (signal.aborted) throw new Error("Mirror upload aborted or deadline exceeded");
	let reject: (error: Error) => void = () => {};
	const interrupted = new Promise<never>((_, r) => {
		reject = r;
	});
	const abort = () => reject(new Error("Mirror upload aborted or deadline exceeded"));
	signal.addEventListener("abort", abort, { once: true });
	try {
		return await Promise.race([operation, interrupted]);
	} finally {
		signal.removeEventListener("abort", abort);
	}
}

export class LocalStorage implements StorageBackend {
	private readonly resolvedBaseDir: string;

	constructor(private readonly baseDir: string) {
		if (!existsSync(baseDir)) {
			mkdirSync(baseDir, { recursive: true });
		}
		this.resolvedBaseDir = resolvePath(baseDir);
	}

	/**
	 * Join a storage-relative path against the base directory, refusing anything
	 * that escapes it. Route handlers interpolate request parameters into storage
	 * paths, so containment is enforced here rather than trusting every caller.
	 */
	private resolve(path: string): string {
		if (!path || isAbsolute(path)) throw new UnsafeStoragePathError(path);
		if (path.includes("\0")) throw new UnsafeStoragePathError(path);
		const full = resolvePath(join(this.resolvedBaseDir, path));
		const rel = relative(this.resolvedBaseDir, full);
		if (rel === "" || rel === ".." || rel.startsWith(`..${"/"}`) || isAbsolute(rel)) {
			throw new UnsafeStoragePathError(path);
		}
		// On Windows, relative() may use backslashes for the traversal prefix.
		if (rel.startsWith("..\\")) throw new UnsafeStoragePathError(path);
		return full;
	}

	async saveFile(path: string, data: Buffer | ReadableStream): Promise<void> {
		if (Buffer.isBuffer(data)) return this.saveAtomicFile(path, data);
		await this.writeStream(path, data, undefined);
	}

	async saveAtomicFile(path: string, data: Buffer, signal?: AbortSignal): Promise<void> {
		if (signal?.aborted) throw new Error("Mirror metadata commit aborted");
		const full = this.resolve(path);
		await mkdir(dirname(full), { recursive: true });
		const temporary = `${full}.${randomUUID()}.tmp`;
		const file = await open(temporary, "wx", 0o600);
		try {
			if (signal?.aborted) throw new Error("Mirror metadata commit aborted");
			await file.writeFile(data);
			await file.sync();
			await file.close();
			if (signal?.aborted) throw new Error("Mirror metadata commit aborted");
			await rename(temporary, full);
		} finally {
			await file.close().catch(() => {});
			await unlink(temporary).catch(() => {});
		}
	}

	/** Physical temp-space reserve is distinct from projected committed-object quota growth. */
	private reservedBytes = 0;
	private activeUploads = 0;
	private readonly mirrorReservations = new Map<string, MirrorReservation>();
	private readonly activeMirrorTemporaries = new Set<string>();
	private mirrorAdmission: Promise<void> = Promise.resolve();
	/** Admission and final rename share this short lock; streamed writes never hold it. */
	private async withMirrorLock<T>(operation: () => Promise<T>): Promise<T> {
		const previous = this.mirrorAdmission;
		let release = () => {};
		this.mirrorAdmission = new Promise<void>((resolve) => {
			release = resolve;
		});
		await previous;
		try {
			return await operation();
		} finally {
			release();
		}
	}

	/** Transfer temp ownership back to the scanner atomically with withdrawal of its reservations. */
	private async releaseMirrorTemporary(
		temporary: string,
		reservation?: MirrorReservation,
	): Promise<void> {
		if (!reservation) return;
		await this.withMirrorLock(async () => {
			this.activeMirrorTemporaries.delete(temporary);
			reservation.quotaGrowth = 0;
			if (reservation.physicalReserved) this.reservedBytes -= reservation.size;
			reservation.physicalReserved = false;
		});
	}

	/** Only scan the new content-addressed mirror directory, never historical data. */
	private async mirrorBytes(signal: AbortSignal): Promise<number> {
		const directory = this.resolve("mirror-objects");
		const entries = await opendir(directory).catch((error) => {
			if (error.code === "ENOENT") return null;
			throw error;
		});
		let total = 0;
		let count = 0;
		if (entries)
			for await (const entry of entries) {
				signal.throwIfAborted();
				if (++count > 4096) throw new Error("Mirror object quota exceeded");
				if (!entry.isFile()) throw new Error("Invalid mirror object storage");
				const full = join(directory, entry.name);
				// Only this process's exact owned temps are covered by reservations. Crash leftovers count.
				if (this.activeMirrorTemporaries.has(full)) continue;
				total += await stat(full)
					.then((value) => value.size)
					.catch((error) => {
						if (error.code === "ENOENT") return 0;
						throw error;
					});
			}
		return total;
	}

	async saveVerifiedFile(
		path: string,
		data: ReadableStream<Uint8Array>,
		options: VerifiedFileOptions,
	): Promise<void> {
		const full = this.resolve(path);
		if (this.activeUploads >= MAX_MIRROR_UPLOAD_CONCURRENCY || this.mirrorReservations.has(full)) {
			void data.cancel().catch(() => {});
			throw new Error("Mirror upload concurrency limit or duplicate object");
		}
		const signal = AbortSignal.any([options.signal, AbortSignal.timeout(options.timeoutMs)]);
		const reservation: MirrorReservation = {
			size: options.size,
			quotaGrowth: 0,
			physicalReserved: false,
		};
		this.activeUploads++;
		this.mirrorReservations.set(full, reservation);
		try {
			await this.withMirrorLock(async () => {
				signal.throwIfAborted();
				const disk = await abortable(statfs(this.resolvedBaseDir), signal);
				if (disk.bavail * disk.bsize < options.minFreeBytes + this.reservedBytes + options.size)
					throw new Error("Mirror disk reserve exhausted");
				const existing = (await this.getFileSize(path)) ?? 0;
				// Do not borrow future shrinkage: that upload may fail before replacing the old object.
				const growth = Math.max(0, options.size - existing);
				const used = await abortable(this.mirrorBytes(signal), signal);
				const pending = [...this.mirrorReservations.values()].reduce(
					(sum, r) => sum + r.quotaGrowth,
					0,
				);
				if (used + pending + growth > options.quotaBytes)
					throw new Error("Mirror disk quota exceeded");
				signal.throwIfAborted();
				reservation.quotaGrowth = growth;
				reservation.physicalReserved = true;
				this.reservedBytes += options.size;
			});
			await this.writeStream(path, data, { ...options, signal }, reservation);
		} finally {
			await this.withMirrorLock(async () => {
				if (reservation.physicalReserved) this.reservedBytes -= options.size;
				this.mirrorReservations.delete(full);
				this.activeUploads--;
			});
			void data.cancel().catch(() => {});
		}
	}

	private async writeStream(
		path: string,
		data: ReadableStream<Uint8Array>,
		options: VerifiedFileOptions | undefined,
		reservation?: MirrorReservation,
	): Promise<void> {
		const full = this.resolve(path);
		await mkdir(dirname(full), { recursive: true });
		const temporary = `${full}.${randomUUID()}.tmp`;
		if (reservation) this.activeMirrorTemporaries.add(temporary);
		const file = await open(temporary, "wx", 0o600).catch(async (error) => {
			await this.releaseMirrorTemporary(temporary, reservation);
			throw error;
		});
		let reader: ReadableStreamDefaultReader<Uint8Array>;
		try {
			reader = data.getReader();
		} catch (error) {
			await file.close().catch(() => {});
			await unlink(temporary).catch(() => {});
			await this.releaseMirrorTemporary(temporary, reservation);
			throw error;
		}
		const hash = createHash("sha256");
		let bytes = 0;
		let rejectAbort: (reason: Error) => void = () => {};
		const interrupted = new Promise<never>((_, reject) => {
			rejectAbort = reject;
		});
		const abort = () =>
			rejectAbort(
				new Error(
					options?.signal.reason?.name === "TimeoutError"
						? "Mirror upload deadline exceeded"
						: "Mirror upload aborted",
				),
			);
		options?.signal.addEventListener("abort", abort, { once: true });
		const timer = setTimeout(
			() => rejectAbort(new Error("Mirror upload deadline exceeded")),
			options?.timeoutMs ?? 120_000,
		);
		try {
			if (options?.signal.aborted) throw new Error("Mirror upload aborted");
			while (true) {
				const { done, value } = await Promise.race([reader.read(), interrupted]);
				if (done) break;
				bytes += value.byteLength;
				if (bytes > (options?.size ?? 512 * 1024 * 1024))
					throw new Error("Mirror upload byte limit exceeded");
				let offset = 0;
				while (offset < value.byteLength) {
					const result = await Promise.race([
						file.write(value, offset, Math.min(64 * 1024, value.byteLength - offset)),
						interrupted,
					]);
					if (result.bytesWritten === 0) throw new Error("Mirror disk write failed");
					hash.update(value.subarray(offset, offset + result.bytesWritten));
					offset += result.bytesWritten;
				}
			}
			if (options && (bytes !== options.size || hash.digest("hex") !== options.sha256))
				throw new Error("Mirror upload size or SHA256 mismatch");
			await Promise.race([file.sync(), interrupted]);
			if (options?.signal.aborted) throw new Error("Mirror upload aborted");
			await file.close();
			const commit = async () => {
				options?.signal.throwIfAborted();
				await rename(temporary, full);
				if (reservation) {
					reservation.quotaGrowth = 0;
					if (reservation.physicalReserved) this.reservedBytes -= reservation.size;
					reservation.physicalReserved = false;
				}
			};
			if (reservation) await this.withMirrorLock(commit);
			else await commit();
		} finally {
			clearTimeout(timer);
			options?.signal.removeEventListener("abort", abort);
			void reader.cancel().catch(() => {});
			await file.close().catch(() => {});
			await unlink(temporary).catch(() => {});
			// Failed unlink leaves a crash-like orphan: subsequent scans charge its actual bytes.
			await this.releaseMirrorTemporary(temporary, reservation);
		}
	}

	async getFile(path: string): Promise<Buffer | null> {
		const fullPath = this.resolve(path);
		if (!existsSync(fullPath)) return null;
		try {
			return readFileSync(fullPath);
		} catch {
			return null;
		}
	}

	async getFileStream(path: string): Promise<ReadableStream | null> {
		const fullPath = this.resolve(path);
		if (!existsSync(fullPath)) return null;
		try {
			const file = Bun.file(fullPath);
			return file.stream();
		} catch {
			return null;
		}
	}

	async getFileSize(path: string): Promise<number | null> {
		const fullPath = this.resolve(path);
		if (!existsSync(fullPath)) return null;
		try {
			return statSync(fullPath).size;
		} catch {
			return null;
		}
	}

	async deleteFile(path: string): Promise<void> {
		const fullPath = this.resolve(path);
		if (existsSync(fullPath)) {
			try {
				unlinkSync(fullPath);
			} catch {
				// ignore
			}
		}
	}

	async deleteDirectory(path: string): Promise<void> {
		const fullPath = this.resolve(path);
		if (existsSync(fullPath)) {
			try {
				rmSync(fullPath, { recursive: true, force: true });
			} catch {
				// ignore
			}
		}
	}

	async listFiles(prefix: string): Promise<string[]> {
		const fullPath = this.resolve(prefix);
		if (!existsSync(fullPath)) return [];

		const results: string[] = [];
		const walk = (dir: string) => {
			try {
				const entries = readdirSync(dir, { withFileTypes: true });
				for (const entry of entries) {
					const entryPath = join(dir, entry.name);
					if (entry.isDirectory()) {
						walk(entryPath);
					} else {
						results.push(relative(this.baseDir, entryPath));
					}
				}
			} catch {
				// ignore
			}
		};

		try {
			const stat = statSync(fullPath);
			if (stat.isDirectory()) {
				walk(fullPath);
			} else {
				results.push(relative(this.baseDir, fullPath));
			}
		} catch {
			// ignore
		}

		return results;
	}

	async fileExists(path: string): Promise<boolean> {
		return existsSync(this.resolve(path));
	}

	async getFileSliceStream(
		path: string,
		start: number,
		end: number,
	): Promise<ReadableStream | null> {
		const fullPath = this.resolve(path);
		if (!existsSync(fullPath)) return null;
		try {
			return Bun.file(fullPath)
				.slice(start, end + 1)
				.stream();
		} catch {
			return null;
		}
	}
}
