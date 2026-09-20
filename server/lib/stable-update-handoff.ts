import { createHash, randomBytes } from "node:crypto";
import {
	closeSync,
	constants,
	createReadStream,
	fsyncSync,
	linkSync,
	lstatSync,
	mkdirSync,
	openSync,
	readFileSync,
	realpathSync,
	renameSync,
	unlinkSync,
	writeFileSync,
} from "node:fs";
import { homedir } from "node:os";
import { dirname, resolve } from "node:path";
import { logger } from "./logger";
import { getNarraforkPath } from "./narrafork-home";
import { isValidLaunchdLabel } from "./stable-update-install";
import { APP_VERSION, GIT_COMMIT } from "./version";

export const STABLE_SERVER_EXEC_PATH = resolve(homedir(), ".local/bin/narrafork-server");
export const STABLE_UPDATE_HANDOFF_MAX_BYTES = 16 * 1024;
export const STABLE_UPDATE_HANDOFF_MAX_AGE_MS = 10 * 60 * 1000;
const STABLE_SERVER_MAX_BYTES = 512 * 1024 * 1024;
const STABLE_SERVER_HASH_TIMEOUT_MS = 30_000;
const STABLE_UPDATE_HANDOFF_FILENAME = "stable-path-handoff.json";

interface StableUpdateHandoffClaim {
	format: 1;
	createdAt: number;
	markerNonce: string;
	markerPath: string;
	updateEpoch: string;
	targetSha512: string;
	targetVersion: string;
	targetCommit: string;
	sourcePid: number;
	sourceCommit: string;
	launchdLabel?: string;
	execPath: string;
}

export interface WriteStableUpdateHandoffInput {
	markerNonce: string;
	markerPath: string;
	updateEpoch: string;
	targetSha512: string;
	targetVersion: string;
	targetCommit: string;
	launchdLabel?: string;
	sourcePid?: number;
	sourceCommit?: string;
}

/** @internal Dependency overrides keep filesystem/process tests isolated from the real launchd path. */
export interface StableUpdateHandoffOverrides {
	claimPath?: string;
	restartHandoffDir?: string;
	expectedExecPath?: string;
	execPath?: string;
	platform?: NodeJS.Platform;
	ppid?: number;
	xpcServiceName?: string;
	appVersion?: string;
	gitCommit?: string;
	now?: number;
	uid?: number;
	isPidAlive?: (pid: number) => boolean;
}

function claimPath(overrides?: StableUpdateHandoffOverrides): string {
	return overrides?.claimPath ?? getNarraforkPath("updates", STABLE_UPDATE_HANDOFF_FILENAME);
}

function restartHandoffDir(overrides?: StableUpdateHandoffOverrides): string {
	return overrides?.restartHandoffDir ?? getNarraforkPath("restart-handoff");
}

function requiredString(value: unknown, name: string): string {
	if (typeof value !== "string" || value.length === 0 || value.length > 4096) {
		throw new Error(`Invalid stable update handoff ${name}`);
	}
	return value;
}

function requiredLaunchdLabel(value: unknown): string {
	if (!isValidLaunchdLabel(value)) throw new Error("Invalid stable update handoff launchd label");
	return value;
}

function parseClaim(raw: string): StableUpdateHandoffClaim {
	const value = JSON.parse(raw) as Partial<StableUpdateHandoffClaim>;
	if (
		value.format !== 1 ||
		!Number.isSafeInteger(value.createdAt) ||
		!Number.isSafeInteger(value.sourcePid) ||
		(value.sourcePid ?? 0) <= 0
	) {
		throw new Error("Invalid stable update handoff claim");
	}
	return {
		format: 1,
		createdAt: value.createdAt as number,
		markerNonce: requiredString(value.markerNonce, "marker nonce"),
		markerPath: requiredString(value.markerPath, "marker path"),
		updateEpoch: requiredString(value.updateEpoch, "update epoch"),
		targetSha512: requiredString(value.targetSha512, "target hash"),
		targetVersion: requiredString(value.targetVersion, "target version"),
		targetCommit: requiredString(value.targetCommit, "target commit"),
		sourcePid: value.sourcePid as number,
		sourceCommit: requiredString(value.sourceCommit, "source commit"),
		// Legacy v1 claims omitted the label; nonce, target hash and fixed path remain mandatory.
		launchdLabel:
			value.launchdLabel === undefined ? undefined : requiredLaunchdLabel(value.launchdLabel),
		execPath: requiredString(value.execPath, "exec path"),
	};
}

function fsyncDirectory(path: string): void {
	const fd = openSync(path, "r");
	try {
		fsyncSync(fd);
	} finally {
		closeSync(fd);
	}
}

function atomicWrite(path: string, content: string): void {
	if (Buffer.byteLength(content) > STABLE_UPDATE_HANDOFF_MAX_BYTES) {
		throw new Error("Stable update handoff claim exceeds size limit");
	}
	const dir = dirname(path);
	mkdirSync(dir, { recursive: true, mode: 0o700 });
	const tempPath = `${path}.${process.pid}.${randomBytes(8).toString("hex")}.tmp`;
	let fd: number | null = null;
	try {
		fd = openSync(tempPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600);
		writeFileSync(fd, content, "utf8");
		fsyncSync(fd);
		closeSync(fd);
		fd = null;
		renameSync(tempPath, path);
		fsyncDirectory(dir);
	} catch (error) {
		if (fd !== null) closeSync(fd);
		try {
			unlinkSync(tempPath);
		} catch {}
		throw error;
	}
}

export function writeStableUpdateHandoff(
	input: WriteStableUpdateHandoffInput,
	overrides?: StableUpdateHandoffOverrides,
): void {
	const claim: StableUpdateHandoffClaim = {
		format: 1,
		createdAt: overrides?.now ?? Date.now(),
		markerNonce: requiredString(input.markerNonce, "marker nonce"),
		markerPath: resolve(requiredString(input.markerPath, "marker path")),
		updateEpoch: requiredString(input.updateEpoch, "update epoch"),
		targetSha512: requiredString(input.targetSha512, "target hash"),
		targetVersion: requiredString(input.targetVersion, "target version"),
		targetCommit: requiredString(input.targetCommit, "target commit"),
		sourcePid: input.sourcePid ?? process.pid,
		sourceCommit: requiredString(input.sourceCommit ?? GIT_COMMIT, "source commit"),
		launchdLabel: requiredLaunchdLabel(
			input.launchdLabel ?? overrides?.xpcServiceName ?? process.env.XPC_SERVICE_NAME,
		),
		execPath: resolve(overrides?.expectedExecPath ?? STABLE_SERVER_EXEC_PATH),
	};
	if (!Number.isSafeInteger(claim.sourcePid) || claim.sourcePid <= 0) {
		throw new Error("Invalid stable update handoff source pid");
	}
	if (dirname(claim.markerPath) !== resolve(restartHandoffDir(overrides))) {
		throw new Error("Stable update handoff marker is outside restart handoff directory");
	}
	if (claim.targetCommit === claim.sourceCommit) {
		throw new Error("Stable update handoff target commit matches source commit");
	}
	atomicWrite(claimPath(overrides), JSON.stringify(claim));
}

function quarantine(path: string): void {
	try {
		renameSync(path, `${path}.${Date.now()}.${randomBytes(4).toString("hex")}.invalid`);
	} catch {
		try {
			unlinkSync(path);
		} catch {}
	}
}

function restoreClaim(path: string, heldPath: string): void {
	try {
		linkSync(heldPath, path);
		unlinkSync(heldPath);
	} catch {
		quarantine(heldPath);
	}
}

export function removeStableUpdateHandoff(
	markerNonce: string,
	overrides?: StableUpdateHandoffOverrides,
): void {
	const path = claimPath(overrides);
	const heldPath = `${path}.${process.pid}.${randomBytes(8).toString("hex")}.remove`;
	try {
		renameSync(path, heldPath);
	} catch {
		return;
	}
	try {
		const stat = lstatSync(heldPath);
		const claim =
			stat.isFile() && stat.size <= STABLE_UPDATE_HANDOFF_MAX_BYTES
				? parseClaim(readFileSync(heldPath, "utf8"))
				: null;
		if (claim?.markerNonce === markerNonce) {
			unlinkSync(heldPath);
			return;
		}
		restoreClaim(path, heldPath);
	} catch {
		restoreClaim(path, heldPath);
	}
}

function defaultIsPidAlive(pid: number): boolean {
	try {
		process.kill(pid, 0);
		return true;
	} catch (error) {
		return (error as NodeJS.ErrnoException).code !== "ESRCH";
	}
}

async function sha512File(path: string): Promise<string> {
	const hash = createHash("sha512");
	const signal = AbortSignal.timeout(STABLE_SERVER_HASH_TIMEOUT_MS);
	for await (const chunk of createReadStream(path, { signal })) hash.update(chunk as Buffer);
	return hash.digest("base64");
}

function currentUid(overrides?: StableUpdateHandoffOverrides): number {
	const uid = overrides?.uid ?? process.getuid?.();
	if (!Number.isSafeInteger(uid) || (uid ?? -1) < 0) {
		throw new Error("stable update handoff requires a POSIX uid");
	}
	return uid as number;
}

function readMarker(path: string, uid: number): { nonce: string; pid: number } {
	const stat = lstatSync(path);
	if (
		!stat.isFile() ||
		stat.isSymbolicLink() ||
		stat.size > STABLE_UPDATE_HANDOFF_MAX_BYTES ||
		stat.uid !== uid ||
		(stat.mode & 0o022) !== 0
	) {
		throw new Error("Invalid stable update handoff marker");
	}
	const value = JSON.parse(readFileSync(path, "utf8")) as { nonce?: unknown; pid?: unknown };
	return {
		nonce: requiredString(value.nonce, "marker nonce"),
		pid: Number.isSafeInteger(value.pid) ? (value.pid as number) : 0,
	};
}

function currentLaunchdLabel(overrides?: StableUpdateHandoffOverrides): string | null {
	if (
		(overrides?.platform ?? process.platform) !== "darwin" ||
		(overrides?.ppid ?? process.ppid) !== 1
	)
		return null;
	const service = overrides?.xpcServiceName ?? process.env.XPC_SERVICE_NAME;
	return isValidLaunchdLabel(service) ? service : null;
}

/** Consume the fixed-path launchd update claim at most once. */
export async function consumeStableUpdateHandoff(
	overrides?: StableUpdateHandoffOverrides,
): Promise<{ markerNonce: string } | null> {
	const launchdLabel = currentLaunchdLabel(overrides);
	if (!launchdLabel) return null;

	const path = claimPath(overrides);
	const consumedPath = `${path}.${process.pid}.${randomBytes(8).toString("hex")}.consumed`;
	try {
		renameSync(path, consumedPath);
	} catch {
		return null;
	}

	try {
		const uid = currentUid(overrides);
		const claimStat = lstatSync(consumedPath);
		if (
			!claimStat.isFile() ||
			claimStat.isSymbolicLink() ||
			claimStat.size > STABLE_UPDATE_HANDOFF_MAX_BYTES ||
			claimStat.uid !== uid ||
			(claimStat.mode & 0o077) !== 0
		) {
			throw new Error("invalid claim file");
		}
		const claim = parseClaim(readFileSync(consumedPath, "utf8"));
		if (claim.launchdLabel && claim.launchdLabel !== launchdLabel)
			throw new Error("launchd service mismatch");
		const now = overrides?.now ?? Date.now();
		if (claim.createdAt > now || now - claim.createdAt > STABLE_UPDATE_HANDOFF_MAX_AGE_MS) {
			throw new Error("expired claim");
		}
		if ((overrides?.isPidAlive ?? defaultIsPidAlive)(claim.sourcePid)) {
			restoreClaim(path, consumedPath);
			logger.info("Stable update handoff source process is still running");
			return null;
		}

		const expectedExecPath = resolve(overrides?.expectedExecPath ?? STABLE_SERVER_EXEC_PATH);
		const currentExecPath = realpathSync(overrides?.execPath ?? process.execPath);
		if (claim.execPath !== expectedExecPath || currentExecPath !== expectedExecPath) {
			throw new Error("unexpected executable path");
		}
		const execStat = lstatSync(currentExecPath);
		if (
			!execStat.isFile() ||
			execStat.isSymbolicLink() ||
			execStat.size > STABLE_SERVER_MAX_BYTES ||
			execStat.uid !== uid ||
			(execStat.mode & 0o022) !== 0 ||
			realpathSync(currentExecPath) !== expectedExecPath
		) {
			throw new Error("executable path or permissions are invalid");
		}
		if (
			claim.targetCommit === claim.sourceCommit ||
			claim.targetVersion !== (overrides?.appVersion ?? APP_VERSION) ||
			claim.targetCommit !== (overrides?.gitCommit ?? GIT_COMMIT)
		) {
			throw new Error("target build mismatch");
		}
		if ((await sha512File(currentExecPath)) !== claim.targetSha512) {
			throw new Error("target hash mismatch");
		}

		const markerPath = resolve(claim.markerPath);
		if (dirname(markerPath) !== resolve(restartHandoffDir(overrides))) {
			throw new Error("marker path is outside restart handoff directory");
		}
		const marker = readMarker(markerPath, uid);
		if (marker.nonce !== claim.markerNonce || marker.pid !== claim.sourcePid) {
			throw new Error("shutdown marker mismatch");
		}

		unlinkSync(consumedPath);
		try {
			unlinkSync(markerPath);
		} catch {}
		logger.info("Consumed stable update handoff claim");
		return { markerNonce: claim.markerNonce };
	} catch (error) {
		quarantine(consumedPath);
		logger.warn("Rejected stable update handoff claim", {
			reason: error instanceof Error ? error.message : "unknown error",
		});
		return null;
	}
}
