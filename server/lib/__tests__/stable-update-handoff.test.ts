import { afterEach, describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import {
	chmodSync,
	existsSync,
	lstatSync,
	mkdirSync,
	mkdtempSync,
	readdirSync,
	readFileSync,
	realpathSync,
	rmSync,
	symlinkSync,
	writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import {
	consumeStableUpdateHandoff,
	removeStableUpdateHandoff,
	STABLE_UPDATE_HANDOFF_MAX_AGE_MS,
	type StableUpdateHandoffOverrides,
	writeStableUpdateHandoff,
} from "../stable-update-handoff";

const cleanups: Array<() => void> = [];

interface Fixture {
	dir: string;
	claimPath: string;
	markerPath: string;
	execPath: string;
	nonce: string;
	targetHash: string;
	overrides: StableUpdateHandoffOverrides;
}

function fixture(): Fixture {
	const dir = mkdtempSync(resolve(tmpdir(), "nf-stable-handoff-"));
	cleanups.push(() => rmSync(dir, { recursive: true, force: true }));
	const updatesDir = resolve(dir, "updates");
	const restartDir = resolve(dir, "restart-handoff");
	const claimPath = resolve(updatesDir, "stable-path-handoff.json");
	const markerPath = resolve(restartDir, "marker.json");
	const execPath = resolve(dir, "narrafork-server");
	const nonce = "test-recovery-nonce";
	mkdirSync(restartDir, { recursive: true });
	writeFileSync(execPath, "stable binary bytes", { mode: 0o700 });
	writeFileSync(markerPath, JSON.stringify({ nonce, pid: 4242, reason: "replacement_started" }), {
		mode: 0o600,
	});
	const targetHash = createHash("sha512").update("stable binary bytes").digest("base64");
	const canonicalExecPath = realpathSync(execPath);
	return {
		dir,
		claimPath,
		markerPath,
		execPath: canonicalExecPath,
		nonce,
		targetHash,
		overrides: {
			claimPath,
			restartHandoffDir: restartDir,
			expectedExecPath: canonicalExecPath,
			execPath: canonicalExecPath,
			platform: "darwin",
			ppid: 1,
			xpcServiceName: "dev.narrafork.server",
			appVersion: "2.0.0",
			gitCommit: "target-commit",
			now: 10_000,
			isPidAlive: () => false,
		},
	};
}

function writeClaim(f: Fixture, changes: Record<string, unknown> = {}): void {
	writeStableUpdateHandoff(
		{
			markerNonce: f.nonce,
			markerPath: f.markerPath,
			updateEpoch: "epoch-1",
			targetSha512: f.targetHash,
			targetVersion: "2.0.0",
			targetCommit: "target-commit",
			sourcePid: 4242,
			sourceCommit: "source-commit",
			...changes,
		},
		f.overrides,
	);
}

function invalidFiles(f: Fixture): string[] {
	const dir = resolve(f.dir, "updates");
	return existsSync(dir) ? readdirSync(dir).filter((name) => name.endsWith(".invalid")) : [];
}

afterEach(() => {
	while (cleanups.length > 0) cleanups.pop()?.();
});

describe("stable update handoff", () => {
	test("consumes a valid launchd claim exactly once", async () => {
		const f = fixture();
		writeClaim(f);
		expect(lstatSync(f.claimPath).mode & 0o777).toBe(0o600);

		expect(await consumeStableUpdateHandoff(f.overrides)).toEqual({ markerNonce: f.nonce });
		expect(await consumeStableUpdateHandoff(f.overrides)).toBeNull();
		expect(existsSync(f.claimPath)).toBe(false);
		expect(existsSync(f.markerPath)).toBe(false);
	});

	test("matches full target hashes to valid short build identities", async () => {
		for (const [gitCommit, accepted] of [
			["abcdef012", true],
			["abcdef013", false],
			["abcdef0", false],
		] as const) {
			const f = fixture();
			f.overrides.gitCommit = gitCommit;
			writeClaim(f, { targetCommit: `abcdef012${"3".repeat(31)}` });
			const result = await consumeStableUpdateHandoff(f.overrides);
			expect(result).toEqual(accepted ? { markerNonce: f.nonce } : null);
			if (!accepted) expect(invalidFiles(f)).toHaveLength(1);
		}
	});

	test("allows at most one concurrent consumer", async () => {
		const f = fixture();
		writeClaim(f);

		const results = await Promise.all([
			consumeStableUpdateHandoff(f.overrides),
			consumeStableUpdateHandoff(f.overrides),
		]);
		expect(results.filter((result) => result !== null)).toEqual([{ markerNonce: f.nonce }]);
	});

	test("leaves the claim available while the source pid is alive", async () => {
		const f = fixture();
		writeClaim(f);

		expect(await consumeStableUpdateHandoff({ ...f.overrides, isPidAlive: () => true })).toBeNull();
		expect(existsSync(f.claimPath)).toBe(true);
		expect(await consumeStableUpdateHandoff(f.overrides)).toEqual({ markerNonce: f.nonce });
	});

	test("ordinary manual startup does not touch the claim", async () => {
		const f = fixture();
		writeClaim(f);

		expect(await consumeStableUpdateHandoff({ ...f.overrides, ppid: 99 })).toBeNull();
		expect(existsSync(f.claimPath)).toBe(true);
		expect(
			await consumeStableUpdateHandoff({ ...f.overrides, xpcServiceName: "other.service" }),
		).toBeNull();
		expect(existsSync(f.claimPath)).toBe(true);
		expect(await consumeStableUpdateHandoff(f.overrides)).toEqual({ markerNonce: f.nonce });
	});

	test("quarantines expired claims so they cannot trigger a restart loop", async () => {
		const f = fixture();
		writeClaim(f);
		const expired = {
			...f.overrides,
			now: 10_000 + STABLE_UPDATE_HANDOFF_MAX_AGE_MS + 1,
		};

		expect(await consumeStableUpdateHandoff(expired)).toBeNull();
		expect(await consumeStableUpdateHandoff(expired)).toBeNull();
		expect(existsSync(f.claimPath)).toBe(false);
		expect(invalidFiles(f)).toHaveLength(1);
	});

	test("rejects target hash mismatch once", async () => {
		const f = fixture();
		writeClaim(f, { targetSha512: "wrong-hash" });

		expect(await consumeStableUpdateHandoff(f.overrides)).toBeNull();
		expect(await consumeStableUpdateHandoff(f.overrides)).toBeNull();
		expect(invalidFiles(f)).toHaveLength(1);
	});

	test("rejects marker nonce mismatch once", async () => {
		const f = fixture();
		writeClaim(f, { markerNonce: "different-nonce" });

		expect(await consumeStableUpdateHandoff(f.overrides)).toBeNull();
		expect(await consumeStableUpdateHandoff(f.overrides)).toBeNull();
		expect(existsSync(f.markerPath)).toBe(true);
		expect(invalidFiles(f)).toHaveLength(1);
	});

	test("rejects a build from a different target commit", async () => {
		const f = fixture();
		writeClaim(f, { targetCommit: "other-target-commit" });

		expect(await consumeStableUpdateHandoff(f.overrides)).toBeNull();
		expect(invalidFiles(f)).toHaveLength(1);
	});

	test("rejects a claim readable by group or world", async () => {
		const f = fixture();
		writeClaim(f);
		chmodSync(f.claimPath, 0o640);

		expect(await consumeStableUpdateHandoff(f.overrides)).toBeNull();
		expect(invalidFiles(f)).toHaveLength(1);
	});

	test("rejects a group-writable shutdown marker", async () => {
		const f = fixture();
		writeClaim(f);
		chmodSync(f.markerPath, 0o620);

		expect(await consumeStableUpdateHandoff(f.overrides)).toBeNull();
		expect(invalidFiles(f)).toHaveLength(1);
	});

	test("rejects a group-writable executable", async () => {
		const f = fixture();
		writeClaim(f);
		chmodSync(f.execPath, 0o720);

		expect(await consumeStableUpdateHandoff(f.overrides)).toBeNull();
		expect(invalidFiles(f)).toHaveLength(1);
	});

	test("rejects an executable reached through a symlinked parent path", async () => {
		const f = fixture();
		const aliasDir = resolve(f.dir, "stable-alias");
		symlinkSync(f.dir, aliasDir, "dir");
		const aliasedExecPath = resolve(aliasDir, "narrafork-server");
		const overrides = {
			...f.overrides,
			expectedExecPath: aliasedExecPath,
			execPath: aliasedExecPath,
		};
		writeStableUpdateHandoff(
			{
				markerNonce: f.nonce,
				markerPath: f.markerPath,
				updateEpoch: "epoch-1",
				targetSha512: f.targetHash,
				targetVersion: "2.0.0",
				targetCommit: "target-commit",
				sourcePid: 4242,
				sourceCommit: "source-commit",
			},
			overrides,
		);

		expect(await consumeStableUpdateHandoff(overrides)).toBeNull();
		expect(invalidFiles(f)).toHaveLength(1);
	});

	test("remove only deletes the claim with the matching nonce", () => {
		const f = fixture();
		writeClaim(f);

		removeStableUpdateHandoff("foreign-nonce", f.overrides);
		expect(JSON.parse(readFileSync(f.claimPath, "utf8")).markerNonce).toBe(f.nonce);
		removeStableUpdateHandoff(f.nonce, f.overrides);
		expect(existsSync(f.claimPath)).toBe(false);
	});
});
