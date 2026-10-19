import { createHash, randomUUID } from "node:crypto";
import { constants, type createReadStream, createWriteStream } from "node:fs";
import {
	appendFile,
	copyFile,
	link,
	lstat,
	mkdir,
	open,
	opendir,
	rename,
	rm,
	truncate,
	unlink,
	writeFile,
} from "node:fs/promises";
import { join } from "node:path";
import { Readable } from "node:stream";
import archiver from "archiver";
import {
	DEFAULT_RECORDING_REQUEST_BYTES,
	DEFAULT_RECORDING_RESPONSE_BYTES,
	RECORDING_SCHEMA,
	type RecordingIdentity,
	type RecordingPinReason,
	type RecordingSettings,
	type RequestRecordingDetail,
	type SavedRecording,
} from "../../shared/request-recording";
import { AppError, NotFoundError, ValidationError } from "./errors";
import { generateId } from "./id";
import {
	RECORDING_ARCHIVE_BYTES,
	RECORDING_CHUNK_BYTES,
	RECORDING_DOWNLOAD_LEASE_MS,
	RECORDING_MANIFEST_BYTES,
	RECORDING_MAX_DOWNLOADS,
	RECORDING_MAX_RECORDS,
	RECORDING_MAX_SEGMENTS,
	RECORDING_METADATA_BYTES,
	RECORDING_SAVE_LIMIT,
	RECORDING_SEGMENT_BYTES,
	REQUEST_RECORDING_DIR,
	REQUEST_RECORDING_SAVED_DIR,
	type RecorderOperation,
	type RecordingSide,
} from "./request-recorder-protocol";
import {
	sanitizeRecordingDisplayText,
	sanitizeRecordingMetadata,
} from "./request-recorder-sanitize";

const SAVE_ID = /^[A-Za-z0-9_-]{21}$/;
const HASH = /^[a-f0-9]{64}$/;
const SEGMENT_NAME = /^[a-f0-9-]{36}\.jsonl$/;
// Room for request + response header sets (each bounded at 64 KiB by the sanitizer).
const META_PER_RECORD = 192 * 1024;
const SUMMARY_BYTES = 512 * 1024;
const MAX_ACTIVE_RECORDS = 128;

interface Segment {
	file: string;
	side: RecordingSide;
	bytes: number;
	order: number;
}
interface RecordingRecord {
	identity: RecordingIdentity;
	startedAt: string;
	endedAt?: string;
	status: string;
	error?: string;
	metadata: Array<{
		side: RecordingSide;
		value: Record<string, unknown>;
		time: string;
		order: number;
	}>;
	gaps: Record<RecordingSide, number>;
	gapReasons: string[];
	segments: Segment[];
	continuous: boolean;
	errorsOnly: boolean;
	saveId?: string;
	/** Requested pin; applied when the request ends. */
	pinReason?: RecordingPinReason;
}
interface RecordingManifest {
	schema: typeof RECORDING_SCHEMA;
	summary: SavedRecording;
	requests: RecordingRecord[];
}
interface SelectedRecording {
	record: RecordingRecord;
	chunks: string;
	saved?: SavedRecording;
}
export interface PreparedRecordingArchive {
	path: string;
	manifest: RecordingManifest;
	files: Array<{ source: string; name: string; bytes: number }>;
	key: string;
}
function hash(value: string): string {
	return createHash("sha256").update(value).digest("hex");
}
function boundedError(error: unknown): string {
	return (error instanceof Error ? error.message : String(error)).slice(0, 2048);
}
async function ensureDirectory(path: string): Promise<void> {
	await mkdir(path, { recursive: true, mode: 0o700 });
	const info = await lstat(path);
	if (!info.isDirectory() || info.isSymbolicLink())
		throw new ValidationError("Unsafe recording directory");
}
async function readBoundedJson<T>(
	path: string,
	maximum: number,
	onRead?: (bytes: number) => void,
): Promise<T> {
	const fd = await open(path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
	try {
		const info = await fd.stat();
		if (!info.isFile() || info.size > maximum)
			throw new ValidationError("Recording metadata exceeds budget");
		// A bounded read protects against a file growing after stat.
		const buffer = Buffer.alloc(Math.min(info.size + 1, maximum + 1));
		const { bytesRead } = await fd.read(buffer, 0, buffer.length, 0);
		onRead?.(bytesRead);
		if (bytesRead > maximum) throw new ValidationError("Recording metadata exceeds budget");
		return JSON.parse(buffer.subarray(0, bytesRead).toString("utf8")) as T;
	} finally {
		await fd.close();
	}
}
async function atomicJson(
	path: string,
	value: unknown,
	max = RECORDING_MANIFEST_BYTES,
): Promise<void> {
	const content = JSON.stringify(value);
	if (Buffer.byteLength(content) > max)
		throw new ValidationError("Recording metadata exceeds budget");
	const temporary = `${path}.${randomUUID()}.tmp`;
	try {
		await writeFile(temporary, content, { flag: "wx", mode: 0o600 });
		await rename(temporary, path);
	} finally {
		await unlink(temporary).catch(() => {});
	}
}

/** Used only in a worker (or isolated tests). No database, network or model dependency. */
export class RequestRecorderStore {
	private readonly ring: string;
	private readonly saved: string;
	private readonly exports: string;
	private records = new Map<string, RecordingRecord>();
	/** Immutable, ended single-request saves, including explicitly partial/truncated evidence. */
	private completedSaves = new Map<string, string>();
	private current = new Map<string, Segment>();
	private segments: Array<{ requestId: string; segment: Segment }> = [];
	private totals = { request: 0, response: 0 };
	private dirty = new Set<string>();
	private metadataSizes = new Map<string, number>();
	private metadataBytes = 0;
	private archiveReservations = new Set<string>();
	private completedExports = new Map<string, number>();
	private savedRecoverySweep?: AsyncGenerator<void>;
	private savedSweepJob?: Promise<void>;
	private order = Date.now() * 1000;
	private initialized?: Promise<void>;
	private gateAt = 0;
	private gateAllowed = true;
	private config: RecordingSettings = {
		ringEnabled: true,
		ringRequestBytes: DEFAULT_RECORDING_REQUEST_BYTES,
		ringResponseBytes: DEFAULT_RECORDING_RESPONSE_BYTES,
		autoPin: "off",
	};
	constructor(
		readonly home: string,
		private readonly diskGate: () => Promise<boolean> = async () => true,
	) {
		this.ring = join(home, REQUEST_RECORDING_DIR);
		this.saved = join(home, REQUEST_RECORDING_SAVED_DIR);
		this.exports = join(this.ring, ".exports");
	}
	private recordDir(requestId: string): string {
		return join(this.ring, hash(requestId));
	}
	private narratorDir(narratorId: string): string {
		return join(this.saved, hash(narratorId));
	}
	private saveDir(narratorId: string, id: string): string {
		if (!SAVE_ID.test(id)) throw new NotFoundError("Recording", id);
		return join(this.narratorDir(narratorId), id);
	}
	private chunksDir(record: RecordingRecord): string {
		return record.saveId
			? join(this.saveDir(record.identity.narratorId, record.saveId), "chunks")
			: join(this.recordDir(record.identity.requestId), "chunks");
	}
	private async writable(): Promise<boolean> {
		if (Date.now() - this.gateAt > 1000) {
			this.gateAt = Date.now();
			this.gateAllowed = await this.diskGate().catch(() => false);
		}
		return this.gateAllowed;
	}
	private initialize(): Promise<void> {
		this.initialized ??= this.recover().catch((error) => {
			this.initialized = undefined;
			throw error;
		});
		return this.initialized;
	}
	private async readRecoverySegment(
		path: string,
		record: RecordingRecord,
	): Promise<Segment | null> {
		const fd = await open(path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
		let validBytes = 0;
		let first: { direction: RecordingSide; order: number } | undefined;
		let fileBytes = 0;
		try {
			const info = await fd.stat();
			if (!info.isFile() || info.size > RECORDING_SEGMENT_BYTES) return null;
			fileBytes = info.size;
			const bytes = Buffer.alloc(Math.min(info.size + 1, RECORDING_SEGMENT_BYTES + 1));
			const read = await fd.read(bytes, 0, bytes.length, 0);
			if (read.bytesRead > RECORDING_SEGMENT_BYTES) return null;
			while (validBytes < read.bytesRead) {
				const newline = bytes.indexOf(10, validBytes);
				if (newline < 0 || newline >= read.bytesRead) break;
				try {
					const event = JSON.parse(bytes.subarray(validBytes, newline).toString("utf8"));
					if (
						event.requestId !== record.identity.requestId ||
						!["request", "response"].includes(event.direction) ||
						event.encoding !== "base64" ||
						typeof event.type !== "string" ||
						event.type.length > 256 ||
						typeof event.data !== "string" ||
						event.data.length > Math.ceil(RECORDING_CHUNK_BYTES / 3) * 4 ||
						!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(event.data) ||
						!Number.isSafeInteger(event.order) ||
						event.order < 0 ||
						!Number.isSafeInteger(event.packet) ||
						event.packet < 0 ||
						!Number.isSafeInteger(event.offset) ||
						event.offset < 0 ||
						!Number.isSafeInteger(event.totalBytes) ||
						event.totalBytes < 0
					)
						break;
					const payloadBytes =
						(event.data.length / 4) * 3 -
						(event.data.endsWith("==") ? 2 : event.data.endsWith("=") ? 1 : 0);
					if (
						event.offset + payloadBytes > event.totalBytes ||
						(first && first.direction !== event.direction)
					)
						break;
					first ??= { direction: event.direction, order: event.order };
				} catch {
					break;
				}
				validBytes = newline + 1;
			}
		} finally {
			await fd.close();
		}
		if (!first || !validBytes) return null;
		if (validBytes < fileBytes) {
			// Only discard an uncommitted/torn suffix, never a verified complete frame.
			await truncate(path, validBytes);
			this.gap(
				record,
				first.direction,
				fileBytes - validBytes,
				"incomplete segment tail discarded during recovery",
			);
		}
		return {
			file: path.slice(path.lastIndexOf(process.platform === "win32" ? "\\" : "/") + 1),
			side: first.direction,
			bytes: validBytes,
			order: first.order,
		};
	}

	private async recoverSegments(record: RecordingRecord): Promise<Segment[]> {
		const dir = this.chunksDir(record);
		const parents = record.saveId
			? [
					this.saved,
					this.narratorDir(record.identity.narratorId),
					this.saveDir(record.identity.narratorId, record.saveId),
					dir,
				]
			: [this.recordDir(record.identity.requestId), dir];
		for (const path of parents) {
			const info = await lstat(path).catch((error) => {
				if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
				throw error;
			});
			if (!info) {
				this.gap(record, "response", 0, "recording directory missing during recovery");
				return [];
			}
			if (!info.isDirectory() || info.isSymbolicLink())
				throw new ValidationError("Unsafe recovery directory");
		}
		const checkpoint = new Map(
			record.segments
				.slice(0, RECORDING_MAX_SEGMENTS)
				.filter(
					(segment) =>
						SEGMENT_NAME.test(segment.file) &&
						["request", "response"].includes(segment.side) &&
						Number.isSafeInteger(segment.order),
				)
				.map((segment) => [segment.file, segment]),
		);
		const valid: Segment[] = [];
		const directory = await opendir(dir);
		let scanned = 0;
		for await (const entry of directory) {
			if (!SEGMENT_NAME.test(entry.name)) continue;
			const path = join(dir, entry.name);
			const previous = checkpoint.get(entry.name);
			const info = await lstat(path);
			// Runtime-created directories contain at most MAX_SEGMENTS; excess owned files
			// are discarded without parsing so damaged input cannot grow recovery memory.
			let recovered: Segment | null = null;
			if (
				++scanned <= RECORDING_MAX_SEGMENTS &&
				info.isFile() &&
				!info.isSymbolicLink() &&
				info.size <= RECORDING_SEGMENT_BYTES
			) {
				recovered =
					previous && previous.bytes === info.size
						? { ...previous, bytes: info.size }
						: await this.readRecoverySegment(path, record);
			}
			if (recovered) valid.push(recovered);
			else if (!info.isDirectory()) {
				await unlink(path);
				this.gap(
					record,
					previous?.side ?? "response",
					0,
					"invalid or excess segment discarded during recovery",
				);
			}
		}
		const found = new Set(valid.map((segment) => segment.file));
		for (const segment of checkpoint.values())
			if (!found.has(segment.file))
				this.gap(record, segment.side, 0, "checkpoint segment missing during recovery");
		return valid.sort((a, b) => a.order - b.order);
	}

	private async *scanUnpublishedSavedDirectories(): AsyncGenerator<void> {
		const root = await lstat(this.saved).catch((error) => {
			if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
			throw error;
		});
		if (!root) return;
		if (!root.isDirectory() || root.isSymbolicLink())
			throw new ValidationError("Unsafe saved recording recovery directory");
		const narrators = await opendir(this.saved);
		for await (const narrator of narrators) {
			if (!narrator.isDirectory() || !HASH.test(narrator.name)) {
				yield;
				continue;
			}
			const parent = join(this.saved, narrator.name);
			const directory = await opendir(parent);
			for await (const entry of directory) {
				if (!entry.isDirectory()) {
					yield;
					continue;
				}
				const pending = /^\.pending-[A-Za-z0-9_-]{21}$/.test(entry.name);
				if (!pending && !SAVE_ID.test(entry.name)) {
					yield;
					continue;
				}
				const path = join(parent, entry.name);
				if (pending) await rm(path, { recursive: true, force: true });
				else {
					const exists = async (name: string) =>
						lstat(join(path, name)).catch((error) => {
							if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
							throw error;
						});
					const manifest = await exists("manifest.json");
					const summary = await exists("summary.json");
					// A published pair is never removed by maintenance. New continuous captures
					// publish a durable initial pair before accepting transport bytes.
					if (!manifest && !summary) await rm(path, { recursive: true, force: true });
					else if (manifest?.isFile() && !manifest.isSymbolicLink() && !summary) {
						const value = await readBoundedJson<RecordingManifest>(
							join(path, "manifest.json"),
							RECORDING_MANIFEST_BYTES,
						).catch(() => null);
						if (
							value?.schema === RECORDING_SCHEMA &&
							value.summary?.id === entry.name &&
							typeof value.summary.narratorId === "string" &&
							hash(value.summary.narratorId) === narrator.name
						)
							await atomicJson(join(path, "summary.json"), value.summary, SUMMARY_BYTES);
					}
				}
				yield;
			}
			yield;
		}
	}

	private async drainSavedRecovery(limit = 64): Promise<void> {
		if (!this.savedRecoverySweep) return;
		if (!this.savedSweepJob)
			this.savedSweepJob = (async () => {
				for (let count = 0; count < limit && this.savedRecoverySweep; count++) {
					const step = await this.savedRecoverySweep.next();
					if (step.done) this.savedRecoverySweep = undefined;
				}
			})().finally(() => {
				this.savedSweepJob = undefined;
			});
		await this.savedSweepJob;
	}
	private async recover(): Promise<void> {
		this.records.clear();
		this.current.clear();
		this.segments = [];
		this.dirty.clear();
		this.metadataSizes.clear();
		this.metadataBytes = 0;
		this.totals = { request: 0, response: 0 };
		await ensureDirectory(this.ring);
		// Derived downloads are disposable; a crashed process can leave at most four.
		const exportDirectory = await lstat(this.exports).catch((error) => {
			if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
			throw error;
		});
		if (exportDirectory && (!exportDirectory.isDirectory() || exportDirectory.isSymbolicLink()))
			throw new ValidationError("Unsafe recording export directory");
		const oldExports = exportDirectory ? await opendir(this.exports) : null;
		if (oldExports) {
			let scanned = 0;
			for await (const entry of oldExports) {
				if (++scanned > 256) break;
				if (/^[a-f0-9-]{36}\.zip$/.test(entry.name))
					await unlink(join(this.exports, entry.name)).catch(() => {});
			}
		}
		const directory = await opendir(this.ring);
		let entries = 0;
		for await (const entry of directory) {
			if (!entry.isDirectory() || !HASH.test(entry.name)) continue;
			if (++entries > RECORDING_MAX_RECORDS) {
				await rm(join(this.ring, entry.name), { recursive: true, force: true });
				continue;
			}
			try {
				const record = await readBoundedJson<RecordingRecord>(
					join(this.ring, entry.name, "record.json"),
					RECORDING_METADATA_BYTES,
				);
				if (
					hash(record.identity.requestId) !== entry.name ||
					typeof record.identity.narratorId !== "string" ||
					!Array.isArray(record.segments)
				)
					throw new Error("Invalid record");
				record.status = record.endedAt ? record.status : "interrupted";
				if (!record.endedAt)
					this.gap(
						record,
						"response",
						0,
						"process interrupted; final buffered bytes may be missing",
					);
				record.segments = await this.recoverSegments(record);
				for (const segment of record.segments) {
					this.order = Math.max(this.order, segment.order + 1);
					if (!record.saveId) {
						this.segments.push({ requestId: record.identity.requestId, segment });
						this.totals[segment.side] += segment.bytes;
					}
				}
				this.records.set(record.identity.requestId, record);
				this.markDirty(record);
				if (record.saveId) {
					// The receipt may survive a crash before chunks/ was created. Finish
					// creating the validated private path before publishing a readable bundle.
					await ensureDirectory(this.saved);
					await ensureDirectory(this.narratorDir(record.identity.narratorId));
					await ensureDirectory(this.saveDir(record.identity.narratorId, record.saveId));
					await ensureDirectory(this.chunksDir(record));
					await this.writeContinuous(record);
					this.forgetRecord(record.identity.requestId);
					await rm(this.recordDir(record.identity.requestId), { recursive: true, force: true });
				}
			} catch {
				await rm(join(this.ring, entry.name), { recursive: true, force: true });
			}
		}
		this.segments.sort((a, b) => a.segment.order - b.segment.order);
		await this.prune();
		this.savedRecoverySweep = this.scanUnpublishedSavedDirectories();
		await this.drainSavedRecovery(256);
	}
	async execute(op: RecorderOperation, signal?: AbortSignal): Promise<unknown> {
		if (op.type === "configure") {
			this.config = op.settings;
		}
		await this.initialize();
		switch (op.type) {
			case "configure":
				await this.prune();
				return null;
			case "begin":
				return this.begin(op);
			case "chunk":
				return this.capture(op);
			case "meta": {
				const record = this.records.get(op.requestId);
				if (!record) return null;
				if (op.value.metadataTruncated) this.gap(record, op.side, 1, "metadata truncated");
				if (op.value.headersTruncated) this.gap(record, op.side, 1, "headers truncated");
				record.metadata.push({
					side: op.side,
					value: op.value,
					time: new Date().toISOString(),
					order: this.order++,
				});
				while (
					record.metadata.length > 64 ||
					Buffer.byteLength(JSON.stringify(record.metadata)) > META_PER_RECORD
				) {
					record.metadata.shift();
					this.gap(record, op.side, 1, "metadata budget");
				}
				this.markDirty(record);
				await this.prune();
				return null;
			}
			case "gap": {
				const record = this.records.get(op.requestId);
				if (record) this.gap(record, op.side, op.bytes, op.reason);
				return null;
			}
			case "end":
				return this.end(op);
			case "bind": {
				const record = this.records.get(op.requestId);
				if (record) {
					record.identity.apiRequestId = op.apiRequestId;
					this.markDirty(record);
				}
				return null;
			}
			case "pin":
				return this.pin(op.requestId, op.reason);
			case "detail":
				return this.detail(op.narratorId, op.requestId, signal);
			case "save-specific": {
				const found = await this.findRequest(op.narratorId, op.requestId, signal);
				signal?.throwIfAborted();
				// A single-request durable bundle already satisfies the contract.
				if (found.saved?.requestCount === 1 && found.record.endedAt) return found.saved;
				return this.save(op.narratorId, [found.record], found.chunks, signal, "request");
			}
			case "save":
				return this.save(op.narratorId, undefined, undefined, undefined, "manual");
			case "list":
				return this.list(op.narratorId, op.cursor, op.limit);
			case "delete":
				return this.remove(op.narratorId, op.saveId);
			case "flush":
				return this.flush();
			case "archive":
				throw new Error("Archive must run outside the capture queue");
		}
	}
	private markDirty(record: RecordingRecord): void {
		const id = record.identity.requestId;
		const bytes = Buffer.byteLength(JSON.stringify({ ...record, segments: [] }));
		this.metadataBytes += bytes - (this.metadataSizes.get(id) ?? 0);
		this.metadataSizes.set(id, bytes);
		this.dirty.add(id);
	}
	private forgetRecord(id: string): void {
		this.records.delete(id);
		this.dirty.delete(id);
		this.metadataBytes -= this.metadataSizes.get(id) ?? 0;
		this.metadataSizes.delete(id);
	}
	private async begin(op: Extract<RecorderOperation, { type: "begin" }>): Promise<void> {
		if (this.records.has(op.identity.requestId)) return;
		if (!(await this.writable()))
			throw new AppError("Recording paused by disk safety", 507, "RECORDING_DISK_PRESSURE");
		if (
			[...this.records.values()].filter((record) => record.status === "inFlight").length >=
			MAX_ACTIVE_RECORDS
		)
			throw new AppError("Too many active recordings", 429, "RECORDING_BUSY");
		const record: RecordingRecord = {
			identity: op.identity,
			startedAt: new Date().toISOString(),
			status: "inFlight",
			metadata: [],
			gaps: { request: 0, response: 0 },
			gapReasons: [],
			segments: [],
			continuous: op.continuous,
			errorsOnly: op.errorsOnly,
		};
		await ensureDirectory(this.recordDir(op.identity.requestId));
		if (op.continuous && !op.errorsOnly) {
			await this.checkSaveCount(op.identity.narratorId);
			record.saveId = generateId();
			// Durable ownership receipt precedes any fixed-directory transport bytes.
			await atomicJson(
				join(this.recordDir(op.identity.requestId), "record.json"),
				record,
				RECORDING_METADATA_BYTES,
			);
			await ensureDirectory(this.saved);
			await ensureDirectory(this.narratorDir(op.identity.narratorId));
			await ensureDirectory(this.saveDir(op.identity.narratorId, record.saveId));
		}
		await ensureDirectory(this.chunksDir(record));
		await atomicJson(
			join(this.recordDir(op.identity.requestId), "record.json"),
			record,
			RECORDING_METADATA_BYTES,
		);
		if (record.saveId) await this.writeContinuous(record);
		this.records.set(op.identity.requestId, record);
		this.markDirty(record);
		await this.prune();
	}
	private gap(record: RecordingRecord, side: RecordingSide, bytes: number, reason: string): void {
		record.gaps[side] += Math.max(0, bytes);
		if (!record.gapReasons.includes(reason) && record.gapReasons.length < 16)
			record.gapReasons.push(reason.slice(0, 256));
		this.markDirty(record);
	}
	private async capture(op: Extract<RecorderOperation, { type: "chunk" }>): Promise<void> {
		const record = this.records.get(op.requestId);
		if (!record) return;
		try {
			await this.prune();
		} catch (error) {
			this.gap(
				record,
				op.side,
				op.data.byteLength,
				`retention cleanup failed: ${boundedError(error)}`,
			);
			return;
		}
		if (!(await this.writable())) {
			this.gap(record, op.side, op.data.byteLength, "disk safety");
			return;
		}
		const cap = record.saveId
			? RECORDING_ARCHIVE_BYTES - RECORDING_MANIFEST_BYTES
			: this.budget(op.side);
		if (cap <= 0) return;
		let data = op.data;
		let offset = op.offset;
		const event = {
			requestId: op.requestId,
			direction: op.side,
			type: op.kind,
			time: op.time,
			order: this.order++,
			packet: op.packet,
			offset,
			totalBytes: op.total,
			encoding: "base64",
			data: "",
		};
		const overhead = Buffer.byteLength(JSON.stringify(event)) + 1;
		const maxPayload = Math.max(
			0,
			Math.floor(((Math.min(cap, RECORDING_SEGMENT_BYTES) - overhead) * 3) / 4) - 3,
		);
		if (data.byteLength > maxPayload) {
			const dropped = data.byteLength - maxPayload;
			data = data.subarray(dropped);
			offset += dropped;
			this.gap(record, op.side, dropped, "capacity");
		}
		if (!data.byteLength && op.total > 0) return;
		event.offset = offset;
		event.data = Buffer.from(data).toString("base64");
		const line = `${JSON.stringify(event)}\n`;
		const bytes = Buffer.byteLength(line);
		if (
			record.saveId &&
			record.segments.reduce((sum, segment) => sum + segment.bytes, 0) + bytes > cap
		) {
			this.gap(record, op.side, data.byteLength, "single continuous recording safety limit");
			return;
		}
		const key = `${op.requestId}:${op.side}`;
		let segment = this.current.get(key);
		if (!segment || segment.bytes + bytes > Math.min(cap, RECORDING_SEGMENT_BYTES)) {
			segment = { file: `${randomUUID()}.jsonl`, side: op.side, bytes: 0, order: this.order++ };
			record.segments.push(segment);
			this.current.set(key, segment);
			if (!record.saveId) this.segments.push({ requestId: op.requestId, segment });
		}
		try {
			await appendFile(join(this.chunksDir(record), segment.file), line, { mode: 0o600 });
			segment.bytes += bytes;
			if (!record.saveId) this.totals[op.side] += bytes;
			this.markDirty(record);
			await this.prune();
		} catch (error) {
			this.gap(record, op.side, data.byteLength, `write failed: ${boundedError(error)}`);
		}
	}
	private budget(side: RecordingSide): number {
		return side === "request" ? this.config.ringRequestBytes : this.config.ringResponseBytes;
	}
	private async discardSegment(item: { requestId: string; segment: Segment }): Promise<void> {
		const record = this.records.get(item.requestId);
		if (record) {
			try {
				await unlink(join(this.chunksDir(record), item.segment.file));
			} catch (error) {
				if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
			}
		}
		// Do not pretend disk bytes have been reclaimed when unlink failed.
		this.totals[item.segment.side] -= item.segment.bytes;
		if (!record) return;
		const key = `${item.requestId}:${item.segment.side}`;
		if (this.current.get(key) === item.segment) this.current.delete(key);
		record.segments = record.segments.filter((segment) => segment !== item.segment);
		this.gap(record, item.segment.side, item.segment.bytes, "oldest segments evicted");
	}
	private async prune(): Promise<void> {
		for (const side of ["request", "response"] as const) {
			while (this.totals[side] > this.budget(side)) {
				const index = this.segments.findIndex((item) => item.segment.side === side);
				if (index < 0) break;
				await this.discardSegment(this.segments[index]);
				this.segments.splice(index, 1);
			}
		}
		while (this.segments.length > RECORDING_MAX_SEGMENTS) {
			await this.discardSegment(this.segments[0]);
			this.segments.shift();
		}
		while (
			this.records.size > RECORDING_MAX_RECORDS ||
			this.metadataBytes > RECORDING_METADATA_BYTES
		) {
			const candidate = [...this.records.values()].find(
				(record) => !record.saveId && record.status !== "inFlight",
			);
			if (!candidate) break;
			const removed = this.segments.filter(
				(item) => item.requestId === candidate.identity.requestId,
			);
			for (const item of removed) await this.discardSegment(item);
			this.segments = this.segments.filter(
				(item) => item.requestId !== candidate.identity.requestId,
			);
			this.forgetRecord(candidate.identity.requestId);
			await rm(this.recordDir(candidate.identity.requestId), { recursive: true, force: true });
		}
	}
	private async end(op: Extract<RecorderOperation, { type: "end" }>): Promise<void> {
		const record = this.records.get(op.requestId);
		if (!record) return;
		record.endedAt = new Date().toISOString();
		record.status = op.status ?? (op.error ? "error" : "complete");
		record.error = op.error?.slice(0, 2048);
		this.current.delete(`${op.requestId}:request`);
		this.current.delete(`${op.requestId}:response`);
		this.markDirty(record);
		if (record.saveId) {
			await this.writeContinuous(record);
			this.forgetRecord(op.requestId);
			await rm(this.recordDir(op.requestId), { recursive: true, force: true });
		} else if (record.pinReason) {
			await this.savePinned(record, record.pinReason);
		} else if (record.continuous && record.errorsOnly && op.error) {
			await this.savePinned(record, "error");
		}
		await this.prune();
	}
	/** Pin a request on end; an already-ended request in the ring is saved right away. */
	private async pin(requestId: string, reason: RecordingPinReason): Promise<void> {
		const record = this.records.get(requestId);
		// Continuous captures are already pinned from their start.
		if (!record || record.saveId) return;
		if (!record.endedAt) {
			record.pinReason ??= reason;
			this.markDirty(record);
			return;
		}
		await this.savePinned(record, reason);
	}
	private async savePinned(record: RecordingRecord, reason: RecordingPinReason): Promise<void> {
		const key = `${record.identity.narratorId}\0${record.identity.requestId}`;
		if (this.completedSaves.has(key)) return;
		try {
			await this.save(record.identity.narratorId, [record], undefined, undefined, reason);
		} catch (error) {
			// Automatic pins are best effort: limits and disk pressure keep the ring copy only.
			this.gap(record, "response", 0, `pin failed: ${boundedError(error)}`);
		}
	}
	private async pruneExports(): Promise<void> {
		for (const [path, expiresAt] of this.completedExports) {
			const info = await lstat(path).catch(() => null);
			if (!info) {
				this.completedExports.delete(path);
				continue;
			}
			if (performance.now() >= expiresAt) {
				try {
					await unlink(path);
					this.completedExports.delete(path);
				} catch {
					/* Keep the quota until an open Windows download releases the file. */
				}
			}
		}
	}
	async flush(): Promise<void> {
		await this.drainSavedRecovery();
		await this.pruneExports();
		for (const requestId of [...this.dirty]) {
			const record = this.records.get(requestId);
			if (!record) {
				this.dirty.delete(requestId);
				continue;
			}
			await atomicJson(
				join(this.recordDir(requestId), "record.json"),
				record,
				RECORDING_METADATA_BYTES,
			);
			if (record.saveId) await this.writeContinuous(record);
			this.dirty.delete(requestId);
		}
	}
	private summary(
		id: string,
		narratorId: string,
		records: RecordingRecord[],
		continuous = false,
		reason?: RecordingPinReason,
	): SavedRecording {
		return {
			id,
			narratorId,
			createdAt: new Date().toISOString(),
			requestIds: records.map((record) => record.identity.requestId),
			apiRequestIds: records.flatMap((record) =>
				record.identity.apiRequestId ? [record.identity.apiRequestId] : [],
			),
			requestCount: records.length,
			bytes: records.reduce(
				(sum, record) => sum + record.segments.reduce((size, segment) => size + segment.bytes, 0),
				0,
			),
			partial: records.some(
				(record) =>
					["inFlight", "interrupted", "aborted"].includes(record.status) ||
					record.metadata.some((meta) => meta.value.bodyIncomplete === true),
			),
			truncated: records.some(
				(record) =>
					record.gaps.request > 0 || record.gaps.response > 0 || record.gapReasons.length > 0,
			),
			...(continuous ? { continuous: true } : {}),
			...(reason ? { reason } : {}),
		};
	}
	private rememberCompletedSave(summary: SavedRecording, records: RecordingRecord[]): void {
		if (records.length !== 1 || !records[0].endedAt) return;
		const key = `${summary.narratorId}\0${records[0].identity.requestId}`;
		this.completedSaves.delete(key);
		this.completedSaves.set(key, summary.id);
		while (this.completedSaves.size > RECORDING_MAX_RECORDS) {
			const oldest = this.completedSaves.keys().next().value;
			if (oldest === undefined) break;
			this.completedSaves.delete(oldest);
		}
	}
	private async writeContinuous(record: RecordingRecord): Promise<void> {
		if (!record.saveId) return;
		const dir = this.saveDir(record.identity.narratorId, record.saveId);
		const summary = this.summary(
			record.saveId,
			record.identity.narratorId,
			[record],
			true,
			"continuous",
		);
		summary.createdAt = record.startedAt;
		await atomicJson(join(dir, "manifest.json"), {
			schema: RECORDING_SCHEMA,
			summary,
			requests: [record],
		});
		await atomicJson(join(dir, "summary.json"), summary, SUMMARY_BYTES);
		this.rememberCompletedSave(summary, [record]);
	}
	private async checkSaveCount(narratorId: string): Promise<void> {
		const directory = await opendir(this.narratorDir(narratorId)).catch(() => null);
		if (!directory) return;
		let count = 0;
		for await (const entry of directory) {
			if (entry.isDirectory() && SAVE_ID.test(entry.name) && ++count >= RECORDING_SAVE_LIMIT)
				throw new AppError(
					"Saved recording count limit reached; delete old recordings",
					409,
					"RECORDING_SAVE_LIMIT",
				);
		}
	}
	private async save(
		narratorId: string,
		selected?: RecordingRecord[],
		selectedChunks?: string,
		signal?: AbortSignal,
		reason?: RecordingPinReason,
	): Promise<SavedRecording> {
		signal?.throwIfAborted();
		if (!(await this.writable()))
			throw new AppError(
				"Cannot save recording under disk pressure",
				507,
				"RECORDING_DISK_PRESSURE",
			);
		await this.checkSaveCount(narratorId);
		const records =
			selected ??
			[...this.records.values()].filter((record) => record.identity.narratorId === narratorId);
		if (!records.length && this.config.autoPin === "all") {
			// In continuous mode completed requests are already durable, not in the ring.
			const latest = (await this.list(narratorId, undefined, 50)).items.find(
				(item) => item.continuous,
			);
			if (latest) return (await this.readSaved(narratorId, latest.id)).summary;
		}
		if (!records.length)
			throw new ValidationError("No recorded requests are available", "RECORDING_EMPTY");
		// Seal ALL current files included in the snapshot. Future appends get new inodes.
		for (const record of records) {
			this.current.delete(`${record.identity.requestId}:request`);
			this.current.delete(`${record.identity.requestId}:response`);
		}
		await this.flush();
		const id = generateId();
		const summary = this.summary(id, narratorId, records, false, reason);
		const frozen = structuredClone(records);
		const manifest: RecordingManifest = { schema: RECORDING_SCHEMA, summary, requests: frozen };
		const manifestBytes = Buffer.byteLength(JSON.stringify(manifest));
		if (manifestBytes > RECORDING_MANIFEST_BYTES)
			throw new ValidationError("Recording manifest exceeds safety budget");
		if (summary.bytes + manifestBytes > RECORDING_ARCHIVE_BYTES)
			throw new AppError(
				"Recording window exceeds the 512 MiB bundle limit; reduce the recording capacity and retry",
				413,
				"RECORDING_ARCHIVE_LIMIT",
			);
		await ensureDirectory(this.saved);
		await ensureDirectory(this.narratorDir(narratorId));
		const temporary = join(this.narratorDir(narratorId), `.pending-${id}`);
		try {
			await ensureDirectory(join(temporary, "chunks"));
			for (const record of records) {
				for (const segment of record.segments) {
					signal?.throwIfAborted();
					if (!SEGMENT_NAME.test(segment.file))
						throw new ValidationError("Unsafe recording segment");
					const source = join(selectedChunks ?? this.chunksDir(record), segment.file);
					if (selectedChunks) {
						const info = await lstat(source);
						if (!info.isFile() || info.isSymbolicLink() || info.size !== segment.bytes)
							throw new NotFoundError("Request recording", "request");
					}
					const target = join(temporary, "chunks", segment.file);
					try {
						await link(source, target);
					} catch (error) {
						if (
							(error as NodeJS.ErrnoException).code === "EXDEV" ||
							(error as NodeJS.ErrnoException).code === "EPERM"
						)
							await copyFile(source, target, constants.COPYFILE_EXCL);
						else throw error;
					}
				}
			}
			await atomicJson(join(temporary, "manifest.json"), manifest);
			await atomicJson(join(temporary, "summary.json"), summary, SUMMARY_BYTES);
			signal?.throwIfAborted();
			await rename(temporary, this.saveDir(narratorId, id));
			this.rememberCompletedSave(summary, records);
			return summary;
		} finally {
			await rm(temporary, { recursive: true, force: true });
		}
	}
	private safeDetailText(value: string, maximum = 2048): string {
		return sanitizeRecordingDisplayText(
			value
				.slice(0, maximum * 2)
				.split(this.home)
				.join("[recording data]"),
			maximum,
		);
	}
	private previewText(bytes: Buffer, maximum = 64 * 1024): string {
		let text = bytes.toString("utf8");
		// Invalid/trailing UTF-8 can expand into replacement characters on decoding.
		while (Buffer.byteLength(text) > maximum) text = text.slice(0, -1);
		return text;
	}
	private async findRequest(
		narratorId: string,
		requestId: string,
		signal?: AbortSignal,
	): Promise<SelectedRecording> {
		signal?.throwIfAborted();
		await this.initialize();
		if (!requestId || Buffer.byteLength(requestId) > 512 || /[\p{Cc}]/u.test(requestId))
			throw new NotFoundError("Request recording", "request");
		const ring = this.records.get(requestId);
		if (ring?.identity.narratorId !== narratorId && ring)
			throw new NotFoundError("Request recording", "request");
		// Prefer an ended single-request save, even if the immutable evidence has gaps.
		// An in-flight snapshot is never substituted for the later finished request.
		if (ring?.endedAt) {
			const completed = await this.findSavedRequest(narratorId, requestId, signal, true).catch(
				(error: unknown) => {
					// Saved reuse is optional when exact ring evidence is still available.
					// A large history must not disable preview or targeted saving of that evidence.
					if (error instanceof AppError && error.code === "RECORDING_BUSY") return undefined;
					throw error;
				},
			);
			if (completed) {
				// Usage tracking can bind after transport finish; enrich the view without
				// rewriting immutable evidence or losing a newer same-request binding.
				completed.record.identity.apiRequestId =
					ring.identity.apiRequestId ?? completed.record.identity.apiRequestId;
				return completed;
			}
		}
		// Fully evicted, finished ring entries have no body evidence left.
		if (
			ring &&
			(ring.segments.length > 0 || !ring.endedAt || (!ring.gaps.request && !ring.gaps.response))
		) {
			if (ring.identity.narratorId !== narratorId)
				throw new NotFoundError("Request recording", "request");
			// Seal the preview snapshot's current inodes without creating permanent evidence.
			this.current.delete(`${requestId}:request`);
			this.current.delete(`${requestId}:response`);
			const record = structuredClone(ring);
			const chunks = this.chunksDir(record);
			const roots = record.saveId
				? [
						this.saved,
						this.narratorDir(narratorId),
						this.saveDir(narratorId, record.saveId),
						chunks,
					]
				: [this.ring, this.recordDir(requestId), chunks];
			for (const path of roots) {
				const info = await lstat(path).catch(() => null);
				if (!info?.isDirectory() || info.isSymbolicLink())
					throw new NotFoundError("Request recording", "request");
			}
			return {
				record,
				chunks: this.chunksDir(record),
				...(record.saveId
					? { saved: this.summary(record.saveId, narratorId, [record], true) }
					: {}),
			};
		}
		const saved = await this.findSavedRequest(narratorId, requestId, signal);
		if (!saved) throw new NotFoundError("Request recording", "request");
		return saved;
	}
	private async findSavedRequest(
		narratorId: string,
		requestId: string,
		signal?: AbortSignal,
		completedOnly = false,
	): Promise<SelectedRecording | undefined> {
		signal?.throwIfAborted();
		const key = `${narratorId}\0${requestId}`;
		const cached = this.completedSaves.get(key);
		// Completed single-request saves remain exact, immutable evidence even after
		// ring eviction. Validate the cache before scanning history in either mode.
		if (cached) {
			const manifest = await this.readSaved(narratorId, cached).catch(() => null);
			const record = manifest?.requests.find(
				(item) => item.identity.requestId === requestId && item.identity.narratorId === narratorId,
			);
			if (
				manifest &&
				record?.endedAt &&
				manifest.requests.length === 1 &&
				manifest.summary.requestCount === 1 &&
				manifest.summary.requestIds[0] === requestId
			) {
				return {
					record: structuredClone(record),
					chunks: join(this.saveDir(narratorId, cached), "chunks"),
					saved: manifest.summary,
				};
			}
			this.completedSaves.delete(key);
		}
		for (const path of [this.saved, this.narratorDir(narratorId)]) {
			const info = await lstat(path).catch(() => null);
			if (!info?.isDirectory() || info.isSymbolicLink()) return undefined;
		}
		const directory = await opendir(this.narratorDir(narratorId));
		let candidate: SavedRecording | undefined;
		let scanned = 0;
		let scanBytes = 0;
		for await (const entry of directory) {
			signal?.throwIfAborted();
			if (++scanned > RECORDING_SAVE_LIMIT + 128 || scanBytes > 32 * 1024 * 1024)
				throw new AppError("Recording lookup exceeds safety budget", 429, "RECORDING_BUSY");
			if (!entry.isDirectory() || !SAVE_ID.test(entry.name)) continue;
			const summary = await readBoundedJson<SavedRecording>(
				join(this.narratorDir(narratorId), entry.name, "summary.json"),
				SUMMARY_BYTES,
				(bytes) => {
					scanBytes += bytes;
				},
			).catch(() => null);
			if (scanBytes > 32 * 1024 * 1024)
				throw new AppError("Recording lookup exceeds safety budget", 429, "RECORDING_BUSY");
			if (
				summary?.id !== entry.name ||
				summary.narratorId !== narratorId ||
				!Array.isArray(summary.requestIds) ||
				!summary.requestIds.includes(requestId) ||
				(completedOnly && (summary.requestCount !== 1 || summary.requestIds.length !== 1))
			)
				continue;
			if (
				!candidate ||
				(summary.continuous && !candidate.continuous) ||
				(Boolean(summary.continuous) === Boolean(candidate.continuous) &&
					summary.createdAt > candidate.createdAt)
			)
				candidate = summary;
		}
		if (!candidate) return undefined;
		signal?.throwIfAborted();
		const manifest = await this.readSaved(narratorId, candidate.id);
		const record = manifest.requests.find(
			(item) => item.identity.requestId === requestId && item.identity.narratorId === narratorId,
		);
		if (!record || (completedOnly && !record.endedAt)) return undefined;
		this.rememberCompletedSave(manifest.summary, manifest.requests);
		return {
			record: structuredClone(record),
			chunks: join(this.saveDir(narratorId, candidate.id), "chunks"),
			saved: manifest.summary,
		};
	}
	async detail(
		narratorId: string,
		requestId: string,
		signal?: AbortSignal,
	): Promise<RequestRecordingDetail> {
		const found = await this.findRequest(narratorId, requestId, signal);
		const { record } = found;
		const summary = this.summary("", narratorId, [record]);
		const previewTruncated = { request: false, response: false };
		const previews = { request: "", response: "" };
		let missing = false;
		for (const side of ["request", "response"] as const) {
			const parts: Buffer[] = [];
			let bytes = 0;
			let inputBytes = 0;
			const append = (text: Buffer) => {
				const remaining = 64 * 1024 - bytes;
				if (text.length > remaining) previewTruncated[side] = true;
				const part = text.subarray(0, remaining);
				parts.push(part);
				bytes += part.length;
			};
			for (const meta of record.metadata.filter((item) => item.side === side)) {
				const safe = sanitizeRecordingMetadata(meta.value);
				if (safe.metadataTruncated) previewTruncated[side] = true;
				append(Buffer.from(`${this.safeDetailText(JSON.stringify(safe), 16 * 1024)}\n`));
			}
			for (const segment of record.segments
				.filter((item) => item.side === side)
				.sort((a, b) => a.order - b.order)) {
				signal?.throwIfAborted();
				if (bytes >= 64 * 1024 || inputBytes >= 1024 * 1024) {
					previewTruncated[side] = true;
					break;
				}
				if (!SEGMENT_NAME.test(segment.file) || segment.bytes > RECORDING_SEGMENT_BYTES) {
					missing = previewTruncated[side] = true;
					continue;
				}
				const fd = await open(
					join(found.chunks, segment.file),
					constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0),
				).catch(() => null);
				if (!fd) {
					missing = previewTruncated[side] = true;
					continue;
				}
				try {
					const info = await fd.stat();
					if (!info.isFile()) {
						missing = previewTruncated[side] = true;
						continue;
					}
					const buffer = Buffer.alloc(Math.min(segment.bytes, RECORDING_SEGMENT_BYTES));
					const { bytesRead } = await fd.read(buffer, 0, buffer.length, 0);
					inputBytes += bytesRead;
					if (bytesRead < segment.bytes) missing = previewTruncated[side] = true;
					for (const line of buffer.subarray(0, bytesRead).toString("utf8").split("\n")) {
						if (!line) continue;
						try {
							const event = JSON.parse(line);
							if (
								event.requestId !== requestId ||
								event.direction !== side ||
								typeof event.data !== "string"
							)
								throw new Error("Frame identity mismatch");
							append(Buffer.from(event.data, "base64"));
						} catch {
							missing = previewTruncated[side] = true;
						}
					}
				} finally {
					await fd.close();
				}
			}
			previews[side] = this.previewText(Buffer.concat(parts));
			if (record.gaps[side] > 0) previewTruncated[side] = true;
		}
		return {
			requestId,
			narratorId,
			apiRequestId: record.identity.apiRequestId
				? this.safeDetailText(record.identity.apiRequestId, 256)
				: undefined,
			provider: this.safeDetailText(record.identity.provider, 256),
			model: this.safeDetailText(record.identity.model, 256),
			startedAt: this.safeDetailText(record.startedAt, 64),
			endedAt: record.endedAt ? this.safeDetailText(record.endedAt, 64) : undefined,
			status: this.safeDetailText(record.status, 128),
			error: record.error ? this.safeDetailText(record.error) : undefined,
			partial: summary.partial || missing,
			truncated: summary.truncated || missing,
			requestPreview: previews.request,
			responsePreview: previews.response,
			previewTruncated,
			...(found.saved ? { savedRecording: found.saved } : {}),
		};
	}
	private async readSaved(narratorId: string, id: string): Promise<RecordingManifest> {
		const dir = this.saveDir(narratorId, id);
		try {
			for (const path of [this.saved, this.narratorDir(narratorId), dir, join(dir, "chunks")]) {
				const info = await lstat(path);
				if (!info.isDirectory() || info.isSymbolicLink()) throw new Error("Unsafe directory");
			}
			const manifest = await readBoundedJson<RecordingManifest>(
				join(dir, "manifest.json"),
				RECORDING_MANIFEST_BYTES,
			);
			if (
				manifest.schema !== RECORDING_SCHEMA ||
				manifest.summary.id !== id ||
				manifest.summary.narratorId !== narratorId
			)
				throw new Error("Recording identity mismatch");
			return manifest;
		} catch {
			throw new NotFoundError("Recording", id);
		}
	}
	async list(
		narratorId: string,
		cursor?: string,
		limit = 50,
		signal?: AbortSignal,
	): Promise<{ items: SavedRecording[]; nextCursor: string | null }> {
		signal?.throwIfAborted();
		await this.initialize();
		const count = Math.min(100, Math.max(1, Math.floor(limit)));
		if (cursor && cursor.length > 160) throw new ValidationError("Invalid recording cursor");
		for (const path of [this.saved, this.narratorDir(narratorId)]) {
			const info = await lstat(path).catch((error) => {
				if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
				throw error;
			});
			if (!info) return { items: [], nextCursor: null };
			if (!info.isDirectory() || info.isSymbolicLink())
				throw new NotFoundError("Recording", narratorId);
		}
		const directory = await opendir(this.narratorDir(narratorId));
		const items: SavedRecording[] = [];
		let scanned = 0;
		const key = (item: SavedRecording) => `${item.createdAt}|${item.id}`;
		for await (const entry of directory) {
			signal?.throwIfAborted();
			if (!entry.isDirectory() || !SAVE_ID.test(entry.name)) continue;
			if (++scanned > RECORDING_SAVE_LIMIT)
				throw new AppError(
					"Saved recording index exceeds safety limit",
					409,
					"RECORDING_SAVE_LIMIT",
				);
			const summary = await readBoundedJson<SavedRecording>(
				join(this.narratorDir(narratorId), entry.name, "summary.json"),
				SUMMARY_BYTES,
			).catch(() => null);
			if (
				!summary ||
				summary.narratorId !== narratorId ||
				summary.id !== entry.name ||
				(cursor && key(summary) >= cursor)
			)
				continue;
			items.push(summary);
			items.sort((a, b) => (key(a) < key(b) ? 1 : -1));
			if (items.length > count + 1) items.pop();
		}
		let hasMore = items.length > count;
		if (hasMore) items.pop();
		// Lists carry IDs for the copy action, never a multi-MiB unbounded summary response.
		let bytes = items.reduce((sum, item) => sum + Buffer.byteLength(JSON.stringify(item)), 0);
		while (items.length > 1 && bytes > SUMMARY_BYTES) {
			const removed = items.pop() as SavedRecording;
			bytes -= Buffer.byteLength(JSON.stringify(removed));
			hasMore = true;
		}
		return { items, nextCursor: hasMore ? key(items[items.length - 1]) : null };
	}
	private async remove(narratorId: string, id: string): Promise<void> {
		await this.readSaved(narratorId, id);
		const key = `${narratorId}:${id}`;
		if (
			this.archiveReservations.has(key) ||
			[...this.records.values()].some((record) => record.saveId === id)
		)
			throw new AppError("Recording is active; retry after it completes", 409, "RECORDING_ACTIVE");
		await rm(this.saveDir(narratorId, id), { recursive: true });
	}
	/** Short, serialized phase. The slow ZIP job runs separately from ingestion. */
	async prepareArchive(
		narratorId: string,
		id: string,
		signal?: AbortSignal,
	): Promise<PreparedRecordingArchive> {
		signal?.throwIfAborted();
		await this.initialize();
		if (!(await this.writable()))
			throw new AppError("Cannot archive under disk pressure", 507, "RECORDING_DISK_PRESSURE");
		for (const record of this.records.values()) {
			if (record.saveId === id && record.identity.narratorId === narratorId) {
				this.current.delete(`${record.identity.requestId}:request`);
				this.current.delete(`${record.identity.requestId}:response`);
				await this.writeContinuous(record);
			}
		}
		const manifest = await this.readSaved(narratorId, id);
		const files: PreparedRecordingArchive["files"] = [];
		let bytes = Buffer.byteLength(JSON.stringify(manifest));
		for (const record of manifest.requests) {
			signal?.throwIfAborted();
			if (record.identity.narratorId !== narratorId) throw new NotFoundError("Recording", id);
			for (const segment of record.segments) {
				if (!SEGMENT_NAME.test(segment.file)) throw new NotFoundError("Recording", id);
				const source = join(this.saveDir(narratorId, id), "chunks", segment.file);
				const info = await lstat(source);
				if (!info.isFile() || info.isSymbolicLink() || info.size !== segment.bytes)
					throw new ValidationError("Recording segment missing or changed");
				bytes += info.size;
				if (bytes > RECORDING_ARCHIVE_BYTES)
					throw new AppError(
						"Recording exceeds the 512 MiB download limit",
						413,
						"RECORDING_ARCHIVE_LIMIT",
					);
				files.push({ source, name: `chunks/${segment.file}`, bytes: segment.bytes });
			}
		}
		await ensureDirectory(this.exports);
		const key = `${narratorId}:${id}`;
		await this.pruneExports();
		if (
			this.completedExports.size + this.archiveReservations.size >= RECORDING_MAX_DOWNLOADS ||
			this.archiveReservations.size >= 2 ||
			this.archiveReservations.has(key)
		)
			throw new AppError("Recording archive is busy; retry", 429, "RECORDING_BUSY");
		this.archiveReservations.add(key);
		return { path: join(this.exports, `${randomUUID()}.zip`), manifest, files, key };
	}
	async buildArchive(
		job: PreparedRecordingArchive,
		signal: AbortSignal,
	): Promise<{ path: string; bytes: number }> {
		if (signal.aborted) {
			this.archiveReservations.delete(job.key);
			signal.throwIfAborted();
		}
		const archive = archiver("zip", { zlib: { level: 1 } });
		const output = createWriteStream(job.path, { flags: "wx", mode: 0o600 });
		const closed = new Promise<void>((resolveClosed) => output.once("close", resolveClosed));
		const streams: ReturnType<typeof createReadStream>[] = [];
		const abort = () => {
			archive.abort();
			output.destroy(new Error("Recording archive cancelled"));
			for (const stream of streams) stream.destroy();
		};
		try {
			signal.throwIfAborted();
			signal.addEventListener("abort", abort, { once: true });
			const finished = new Promise<void>((resolveJob, reject) => {
				output.once("close", () => resolveJob());
				output.once("error", reject);
				archive.once("error", reject);
				archive.once("warning", reject);
			});
			archive.pipe(output);
			archive.append(JSON.stringify(job.manifest), { name: "manifest.json" });
			for (const file of job.files) {
				signal.throwIfAborted();
				const source = Readable.from(
					(async function* () {
						const fd = await open(file.source, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
						try {
							const stream = fd.createReadStream({
								autoClose: false,
								end: Math.max(0, file.bytes - 1),
							});
							streams.push(stream);
							try {
								for await (const chunk of stream) {
									signal.throwIfAborted();
									yield chunk;
								}
							} finally {
								streams.splice(streams.indexOf(stream), 1);
								stream.destroy();
							}
						} finally {
							await fd.close();
						}
					})(),
				);
				archive.append(source, { name: file.name });
			}
			await Promise.all([archive.finalize(), finished]);
			signal.throwIfAborted();
			const bytes = (await lstat(job.path)).size;
			if (bytes > RECORDING_ARCHIVE_BYTES)
				throw new AppError(
					"Recording ZIP exceeds the download limit",
					413,
					"RECORDING_ARCHIVE_LIMIT",
				);
			this.completedExports.set(job.path, performance.now() + RECORDING_DOWNLOAD_LEASE_MS);
			return { path: job.path, bytes };
		} catch (error) {
			archive.abort();
			output.destroy();
			for (const stream of streams) stream.destroy();
			await closed;
			await unlink(job.path).catch(() => {});
			throw error;
		} finally {
			signal.removeEventListener("abort", abort);
			this.archiveReservations.delete(job.key);
		}
	}
}
