import { createHash } from "node:crypto";
import { type BigIntStats, constants } from "node:fs";
import * as fs from "node:fs/promises";
import { dirname, isAbsolute, normalize, relative, sep } from "node:path";
import {
	FILE_CHANGE_LIMITS,
	type FileChangeBlobRef,
	type FileChangeIdentity,
	type FileChangeState,
	fileChangeStatesEqual,
	type KnownFileChangeState,
} from "@shared/file-change-protocol";
import { type ExecutionBackend, LOCAL_DEVICE_ID } from "../lib/agent/execution/backend";
import { localBackend } from "../lib/agent/execution/local-backend";
import { guardDiskWrite } from "../lib/disk-safety";
import {
	createFileChangeIdentity,
	type FileChangeScopeIdentity,
	fileChangeIdentityKey,
} from "./file-change-identity";
import { localObjectIdentity } from "./file-change-local-io";

export const LOCAL_RESTORE_LIMITS = Object.freeze({
	timeoutMs: 30_000,
	maxTimeoutMs: 120_000,
	pathComponents: 128,
	createdParents: 32,
	ioChunkBytes: 64 * 1024,
});
export interface LocalRestoreDescriptor {
	mutationId: string;
	requestDigest: string;
	identity: Readonly<FileChangeIdentity>;
	scope: Readonly<FileChangeScopeIdentity>;
	expectedObjectIdentity?: string | null;
	expected: KnownFileChangeState;
	desired: KnownFileChangeState;
	createParents?: boolean;
}
export interface LocalRestoreGuard extends Readonly<LocalRestoreDescriptor> {
	phase: "preflight" | "before_dispatch" | "after_dispatch";
	signal: AbortSignal;
}
export interface LocalFileRestoreInput extends LocalRestoreDescriptor {
	backend: ExecutionBackend;
	authorize(guard: LocalRestoreGuard): Promise<void>;
	assertCurrent(guard: LocalRestoreGuard): void | Promise<void>;
	onDispatch(guard: LocalRestoreGuard): void;
	readBlob(
		ref: Readonly<FileChangeBlobRef>,
		options: { signal: AbortSignal; maxBytes: number },
	): Promise<Uint8Array>;
	signal: AbortSignal;
	timeoutMs?: number;
}
export interface LocalRestoreObservation {
	state: FileChangeState;
	raw: Uint8Array | null;
	objectIdentity: string | null;
}
export type LocalRestoreFailure =
	| "invalid_input"
	| "unsupported_backend"
	| "unsupported_object"
	| "target_changed"
	| "expected_mismatch"
	| "authorization_failed"
	| "guard_failed"
	| "permission_denied"
	| "blob_invalid"
	| "budget_exceeded"
	| "cancelled"
	| "timeout"
	| "io_busy"
	| "io_failed";
export interface LocalFileRestoreResult {
	status: "applied" | "not_dispatched" | "uncertain_after_dispatch";
	reason: LocalRestoreFailure | "verified" | "no_change" | "preflight_verified";
	observation: LocalRestoreObservation | null;
	ioPending: boolean;
	/** The caller keeps its single-file queue until actual IO and descriptor cleanup finish. */
	whenSettled: Promise<void>;
	stats: { elapsedMs: number; bytesRead: number; bytesWritten: number; dispatches: 0 | 1 };
}
let restoreSemantics: NodeJS.Platform = process.platform;
export function setLocalRestoreSemanticsForTests(platform: NodeJS.Platform | null): void {
	restoreSemantics = platform ?? process.platform;
}
class RestoreError extends Error {
	constructor(readonly code: LocalRestoreFailure) {
		super(code);
	}
}
function fail(code: LocalRestoreFailure): never {
	throw new RestoreError(code);
}
export function restoreLocalFile(input: LocalFileRestoreInput): Promise<LocalFileRestoreResult> {
	return run(input, false);
}
export function preflightLocalFileRestore(
	input: LocalFileRestoreInput,
): Promise<LocalFileRestoreResult> {
	return run(input, true);
}

async function run(
	input: LocalFileRestoreInput,
	preflight: boolean,
): Promise<LocalFileRestoreResult> {
	const started = performance.now();
	const controller = new AbortController();
	const timeout = input.timeoutMs ?? LOCAL_RESTORE_LIMITS.timeoutMs;
	if (!Number.isSafeInteger(timeout) || timeout < 1 || timeout > LOCAL_RESTORE_LIMITS.maxTimeoutMs)
		fail("invalid_input");
	const signal = AbortSignal.any([input.signal, controller.signal]);
	let session: RestoreSession | undefined;
	let settled = false;
	let reason: LocalFileRestoreResult["reason"] = "invalid_input";
	let success = false;
	const body = (async () => {
		try {
			session = new RestoreSession(input, signal);
			reason = await session.execute(preflight);
			success = true;
		} catch (error) {
			reason = failureCode(error, signal);
		} finally {
			try {
				await session?.close();
			} catch {
				success = false;
				reason = "io_failed";
			}
			settled = true;
		}
	})();
	let timer: ReturnType<typeof setTimeout> | undefined;
	const deadline = new Promise<void>((resolve) => {
		timer = setTimeout(() => {
			controller.abort(new RestoreError("timeout"));
			resolve();
		}, timeout);
	});
	await Promise.race([body, deadline]);
	if (settled && timer) clearTimeout(timer);
	else {
		reason = "timeout";
		success = false;
	}
	return {
		status: session?.dispatched
			? success
				? "applied"
				: "uncertain_after_dispatch"
			: "not_dispatched",
		reason,
		observation: settled ? (session?.observation ?? null) : null,
		ioPending: !settled,
		whenSettled: body,
		stats: {
			elapsedMs: Math.round(performance.now() - started),
			bytesRead: session?.bytesRead ?? 0,
			bytesWritten: session?.bytesWritten ?? 0,
			dispatches: session?.dispatched ? 1 : 0,
		},
	};
}

/** Current object checks only. Historical workspace state never authorizes a syscall. */
class RestoreSession {
	dispatched = false;
	bytesRead = 0;
	bytesWritten = 0;
	observation: LocalRestoreObservation | null = null;
	private file?: fs.FileHandle;
	private stamp?: BigIntStats;
	private readonly ancestors = new Map<string, { identity: string; mode: bigint } | null>();
	private readonly target: LocalRestoreDescriptor;
	constructor(
		private readonly input: LocalFileRestoreInput,
		private readonly signal: AbortSignal,
	) {
		this.target = freezeDescriptor(input);
		if (
			input.backend !== localBackend ||
			input.backend.kind !== "local" ||
			this.target.identity.deviceId !== LOCAL_DEVICE_ID ||
			this.target.identity.pathFlavor !== input.backend.pathFlavor
		)
			fail("unsupported_backend");
		for (const name of ["authorize", "assertCurrent", "onDispatch", "readBlob"] as const)
			if (typeof input[name] !== "function") fail("invalid_input");
	}
	private check() {
		this.signal.throwIfAborted();
	}
	private context(phase: LocalRestoreGuard["phase"]): LocalRestoreGuard {
		return { ...this.target, phase, signal: this.signal };
	}
	private async guard(phase: LocalRestoreGuard["phase"]) {
		this.check();
		const guard = this.context(phase);
		try {
			await this.input.assertCurrent(guard);
		} catch {
			this.check();
			fail("guard_failed");
		}
		try {
			await this.input.authorize(guard);
		} catch {
			this.check();
			fail("authorization_failed");
		}
		const canonical = await this.input.backend.resolvePathIdentity(
			this.target.identity.lexicalPath,
			{ signal: this.signal },
		);
		if (
			!this.input.backend.paths.equals(canonical.canonicalPath, this.target.identity.canonicalPath)
		)
			fail("target_changed");
		this.check();
	}
	private async entry(path: string) {
		try {
			return await fs.lstat(path, { bigint: true });
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code === "ENOENT") return null;
			throw error;
		}
	}
	private async inspectAncestors(initial = false) {
		const paths = initial
			? [
					...new Set([
						...ancestorPaths(this.target.identity.canonicalPath),
						...ancestorPaths(this.target.identity.lexicalPath),
					]),
				]
			: [...this.ancestors.keys()];
		for (const path of paths) {
			this.check();
			const entry = await this.entry(path);
			if (entry && (!entry.isDirectory() || entry.isSymbolicLink())) fail("unsupported_object");
			const current = entry ? { identity: localObjectIdentity(entry), mode: entry.mode } : null;
			if (initial) this.ancestors.set(path, current);
			else if (
				current?.identity !== this.ancestors.get(path)?.identity ||
				current?.mode !== this.ancestors.get(path)?.mode
			)
				fail("target_changed");
		}
	}
	private async desiredBytes(): Promise<Uint8Array | null> {
		const state = this.target.desired;
		if (state.kind === "absent") return null;
		if (state.kind !== "regular") fail("unsupported_object");
		// prepareExpected must still read and fingerprint the live object; equal states do not write.
		if (fileChangeStatesEqual(this.target.expected, state)) return null;
		let raw: Uint8Array;
		try {
			raw = await this.input.readBlob(state.blob, {
				signal: this.signal,
				maxBytes: state.blob.sizeBytes,
			});
		} catch {
			this.check();
			fail("blob_invalid");
		}
		this.check();
		if (
			!(raw instanceof Uint8Array) ||
			raw.byteLength !== state.blob.sizeBytes ||
			raw.buffer instanceof SharedArrayBuffer
		)
			fail("blob_invalid");
		const copy = new Uint8Array(raw);
		if (createHash("sha256").update(copy).digest("hex") !== state.blob.digest) fail("blob_invalid");
		return copy;
	}
	private async open(flags: number, mode?: number) {
		this.file = await fs.open(
			this.target.identity.canonicalPath,
			flags | (constants.O_NOFOLLOW ?? 0) | (constants.O_NONBLOCK ?? 0),
			mode,
		);
	}
	private async readOpened(): Promise<LocalRestoreObservation> {
		if (!this.file) fail("io_failed");
		const before = await this.file.stat({ bigint: true });
		regular(before);
		const raw = new Uint8Array(Number(before.size));
		let offset = 0;
		while (offset < raw.length) {
			this.check();
			const { bytesRead } = await this.file.read(
				raw,
				offset,
				Math.min(LOCAL_RESTORE_LIMITS.ioChunkBytes, raw.length - offset),
				offset,
			);
			if (!bytesRead) fail("expected_mismatch");
			offset += bytesRead;
			this.bytesRead += bytesRead;
		}
		const after = await this.file.stat({ bigint: true });
		const entry = await this.entry(this.target.identity.canonicalPath);
		if (!entry || !sameStamp(before, after) || !sameStamp(after, entry)) fail("target_changed");
		this.stamp = after;
		return {
			state: {
				kind: "regular",
				mode: Number(after.mode & 0o7777n),
				blob: {
					algorithm: "sha256",
					digest: createHash("sha256").update(raw).digest("hex"),
					sizeBytes: raw.length,
				},
			},
			raw,
			objectIdentity: localObjectIdentity(after),
		};
	}
	private async verifyStamp() {
		await this.inspectAncestors();
		const entry = await this.entry(this.target.identity.canonicalPath);
		if (this.target.expected.kind === "absent") {
			if (entry) fail("expected_mismatch");
		} else {
			if (!this.file || !this.stamp || !entry) fail("target_changed");
			if (
				!sameStamp(this.stamp, entry) ||
				!sameStamp(this.stamp, await this.file.stat({ bigint: true }))
			)
				fail("expected_mismatch");
		}
		this.check();
	}
	private dispatch() {
		this.check();
		if (!this.dispatched) {
			const value: unknown = this.input.onDispatch(this.context("before_dispatch"));
			if (value !== undefined) fail("guard_failed");
			this.dispatched = true;
		}
		this.observation = null;
	}
	private async prepareExpected(byteChange: boolean, modeChange: boolean) {
		const { expected, desired, identity } = this.target;
		if (expected.kind === "absent") {
			if (await this.entry(identity.canonicalPath)) fail("expected_mismatch");
			this.observation = { state: { kind: "absent" }, raw: null, objectIdentity: null };
			return;
		}
		const entry = await this.entry(identity.canonicalPath);
		if (!entry) fail("expected_mismatch");
		regular(entry);
		if (
			!(entry.mode & 0o444n) ||
			(byteChange && desired.kind === "regular" && !(entry.mode & 0o222n))
		)
			fail("permission_denied");
		if (
			restoreSemantics === "win32" &&
			!(entry.mode & 0o222n) &&
			(desired.kind === "absent" || modeChange)
		)
			fail("permission_denied");
		const writable =
			desired.kind === "regular" && (byteChange || (restoreSemantics === "win32" && modeChange));
		await this.open(writable ? constants.O_RDWR : constants.O_RDONLY);
		this.observation = await this.readOpened();
		if (!fileChangeStatesEqual(expected, this.observation.state)) fail("expected_mismatch");
		if (
			this.target.expectedObjectIdentity !== undefined &&
			this.target.expectedObjectIdentity !== this.observation.objectIdentity
		)
			fail("target_changed");
	}
	private async permissions(byteChange: boolean, modeChange: boolean) {
		const { expected, desired, identity } = this.target;
		if (expected.kind === "regular") {
			await fs.access(
				identity.canonicalPath,
				constants.R_OK | (byteChange && desired.kind === "regular" ? constants.W_OK : 0),
			);
			if (
				modeChange &&
				this.stamp &&
				typeof process.geteuid === "function" &&
				process.geteuid() !== 0 &&
				BigInt(process.geteuid()) !== this.stamp.uid
			)
				fail("permission_denied");
		}
		if (expected.kind === "absent" || desired.kind === "absent") {
			const parent = [...ancestorPaths(identity.canonicalPath)]
				.reverse()
				.find((path) => this.ancestors.get(path));
			if (!parent) fail("target_changed");
			const mode = this.ancestors.get(parent)?.mode ?? 0n;
			if (!(mode & 0o222n) || (restoreSemantics !== "win32" && !(mode & 0o111n)))
				fail("permission_denied");
			await fs.access(parent, constants.W_OK | (restoreSemantics === "win32" ? 0 : constants.X_OK));
		}
	}
	async execute(preflight: boolean): Promise<"verified" | "no_change" | "preflight_verified"> {
		const { expected, desired } = this.target;
		await this.guard("preflight");
		await this.inspectAncestors(true);
		const missing = [...this.ancestors].filter(([, stamp]) => !stamp).map(([path]) => path);
		if (missing.length > LOCAL_RESTORE_LIMITS.createdParents) fail("budget_exceeded");
		for (const path of missing) {
			const within = relative(this.target.scope.canonicalRoot, path);
			if (!within || within === ".." || within.startsWith(`..${sep}`) || isAbsolute(within))
				fail("target_changed");
		}
		if (missing.length && desired.kind !== "absent" && !this.target.createParents)
			fail("target_changed");
		const bytes = await this.desiredBytes();
		const byteChange =
			expected.kind !== "regular" ||
			desired.kind !== "regular" ||
			expected.blob.digest !== desired.blob.digest ||
			expected.blob.sizeBytes !== desired.blob.sizeBytes;
		const modeChange =
			expected.kind === "regular" && desired.kind === "regular" && expected.mode !== desired.mode;
		if (
			restoreSemantics === "win32" &&
			desired.kind === "regular" &&
			(expected.kind === "regular"
				? (((expected.mode ?? 0) ^ (desired.mode ?? 0)) & ~0o222) !== 0
				: desired.mode !== 0o666 && desired.mode !== 0o444)
		)
			fail("unsupported_object");
		await this.prepareExpected(byteChange, modeChange);
		await this.permissions(byteChange, modeChange);
		if (bytes) await guardDiskWrite(this.target.identity.canonicalPath, bytes.byteLength);
		await this.guard("before_dispatch");
		await this.verifyStamp();
		if (preflight) return "preflight_verified";
		if (fileChangeStatesEqual(expected, desired)) return "no_change";
		if (desired.kind === "absent") return this.remove();
		if (desired.kind !== "regular") fail("unsupported_object");
		if (expected.kind === "absent") await this.create(missing);
		if (!this.file) fail("io_failed");
		if (byteChange) {
			this.dispatch();
			if (expected.kind === "regular") await this.file.truncate(0);
			let offset = 0;
			while (bytes && offset < bytes.length) {
				this.check();
				const { bytesWritten } = await this.file.write(
					bytes,
					offset,
					Math.min(LOCAL_RESTORE_LIMITS.ioChunkBytes, bytes.length - offset),
					offset,
				);
				if (!bytesWritten) fail("io_failed");
				offset += bytesWritten;
				this.bytesWritten += bytesWritten;
			}
		}
		this.dispatch();
		await this.file.chmod(desired.mode as number);
		await this.file.sync();
		this.check();
		await this.guard("after_dispatch");
		await this.inspectAncestors();
		this.observation = await this.readOpened();
		if (!fileChangeStatesEqual(desired, this.observation.state)) fail("expected_mismatch");
		return "verified";
	}
	private async remove(): Promise<"verified"> {
		if (process.platform === "win32") {
			await this.close();
			const entry = await this.entry(this.target.identity.canonicalPath);
			if (!entry || !this.stamp || !sameStamp(entry, this.stamp)) fail("target_changed");
		}
		this.dispatch();
		await fs.unlink(this.target.identity.canonicalPath);
		this.check();
		await this.guard("after_dispatch");
		if (await this.entry(this.target.identity.canonicalPath)) fail("target_changed");
		this.observation = { state: { kind: "absent" }, raw: null, objectIdentity: null };
		return "verified";
	}
	private async create(missing: string[]) {
		for (const path of missing) {
			this.dispatch();
			await fs.mkdir(path, { mode: 0o700 });
			const entry = await this.entry(path);
			if (!entry?.isDirectory() || entry.isSymbolicLink()) fail("target_changed");
			this.ancestors.set(path, { identity: localObjectIdentity(entry), mode: entry.mode });
		}
		await this.guard("before_dispatch");
		await this.verifyStamp();
		this.dispatch();
		await this.open(constants.O_RDWR | constants.O_CREAT | constants.O_EXCL, 0o600);
		this.observation = await this.readOpened();
		if (this.observation.state.kind !== "regular" || this.observation.state.blob.sizeBytes !== 0)
			fail("target_changed");
	}
	async close() {
		const file = this.file;
		this.file = undefined;
		if (file) await file.close();
	}
}
function freezeDescriptor(input: LocalRestoreDescriptor): LocalRestoreDescriptor {
	if (
		!input.identity ||
		!input.scope ||
		typeof input.mutationId !== "string" ||
		!/^[a-f0-9]{64}$/.test(input.requestDigest)
	)
		fail("invalid_input");
	const identity = { ...input.identity };
	const scope = { ...input.scope };
	for (const path of [identity.canonicalPath, identity.lexicalPath, scope.canonicalRoot])
		if (
			typeof path !== "string" ||
			path.includes("\0") ||
			!isAbsolute(path) ||
			normalize(path) !== path ||
			Buffer.byteLength(path) > FILE_CHANGE_LIMITS.metadataBytes
		)
			fail("invalid_input");
	if (
		fileChangeIdentityKey(createFileChangeIdentity(scope, identity)) !==
		fileChangeIdentityKey(identity)
	)
		fail("target_changed");
	return Object.freeze({
		mutationId: input.mutationId,
		requestDigest: input.requestDigest,
		identity: Object.freeze(identity),
		scope: Object.freeze(scope),
		expectedObjectIdentity: input.expectedObjectIdentity,
		expected: freezeState(input.expected),
		desired: freezeState(input.desired),
		createParents: input.createParents === true,
	});
}
function freezeState(state: KnownFileChangeState): KnownFileChangeState {
	if (state?.kind === "absent") return Object.freeze({ kind: "absent" });
	if (state?.kind !== "regular") fail("unsupported_object");
	if (state.mode === null || !Number.isInteger(state.mode) || state.mode < 0 || state.mode > 0o7777)
		fail("invalid_input");
	if (
		state.blob.algorithm !== "sha256" ||
		!/^[a-f0-9]{64}$/.test(state.blob.digest) ||
		!Number.isSafeInteger(state.blob.sizeBytes) ||
		state.blob.sizeBytes < 0 ||
		state.blob.sizeBytes > FILE_CHANGE_LIMITS.blobBytes
	)
		fail("blob_invalid");
	return Object.freeze({
		kind: "regular",
		mode: state.mode,
		blob: Object.freeze({ ...state.blob }),
	});
}
function ancestorPaths(path: string): string[] {
	const result: string[] = [];
	for (let current = dirname(path); ; current = dirname(current)) {
		result.push(current);
		if (result.length > LOCAL_RESTORE_LIMITS.pathComponents) fail("budget_exceeded");
		if (dirname(current) === current) return result.reverse();
	}
}
function regular(stat: BigIntStats) {
	if (!stat.isFile() || stat.nlink !== 1n || stat.isSymbolicLink()) fail("unsupported_object");
	if (stat.size < 0n || stat.size > BigInt(FILE_CHANGE_LIMITS.blobBytes)) fail("budget_exceeded");
}
function sameStamp(a: BigIntStats, b: BigIntStats): boolean {
	regular(a);
	regular(b);
	return (
		localObjectIdentity(a) === localObjectIdentity(b) &&
		a.size === b.size &&
		a.mode === b.mode &&
		a.mtimeNs === b.mtimeNs &&
		a.ctimeNs === b.ctimeNs
	);
}
function failureCode(error: unknown, signal: AbortSignal): LocalRestoreFailure {
	if (error instanceof RestoreError) return error.code;
	if (signal.aborted)
		return signal.reason instanceof RestoreError ? signal.reason.code : "cancelled";
	const code = (error as NodeJS.ErrnoException | undefined)?.code;
	if (code === "EBUSY" || code === "ETXTBSY") return "io_busy";
	if (code === "EACCES" || code === "EPERM") return "permission_denied";
	if (code === "ELOOP" || code === "ENOTDIR" || code === "EISDIR") return "unsupported_object";
	return "io_failed";
}
