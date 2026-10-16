/**
 * Polling-tick cost control in the worktree watcher.
 *
 * The native watcher is opt-in (`NARRAFORK_ENABLE_NATIVE_WATCHER`), so *polling is
 * the default path* for every attached narrator. Each tick used to run a whole-tree
 * `git add -A` capture plus a snapshot-DAG advance plus a `chapters` UPDATE that has
 * no index to use — unconditionally, every interval, per chapter, on a workspace
 * nobody had touched.
 *
 * These cases pin the two halves of the fix that matter: an idle polled tick does no
 * snapshot work, and a tick where anything actually changed still does. The capture is
 * observed by counting attempts rather than by inspecting the resulting DAG, because
 * `linkSnapshot` deduplicates an unchanged tree — so the DAG looks identical whether
 * the expensive work ran or was skipped, which is exactly the bug being fixed.
 */
import { afterEach, beforeEach, describe, expect, spyOn, test } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { eq } from "drizzle-orm";
import { db } from "../db";
import { worktreeTreeSnapshots } from "../db/schema";
import { eventBus, type NarraForkEvent } from "../lib/event-bus";
import { normalizePathForComparison } from "../lib/platform-path";
import { safeSpawn } from "../lib/spawn";
import { gitService } from "./git-service";
import * as statusCache from "./git-status-cache";
import { clearStatusCache } from "./git-status-cache";
import * as statusStream from "./git-status-stream";
import { worktreeTreeSnapshot } from "./worktree-tree-snapshot";
import { MAX_SKIPPED_POLLS, worktreeWatcher } from "./worktree-watcher";
import { worktreeWatcherMetadata } from "./worktree-watcher-metadata";

const tempDirs: string[] = [];
const snapshotPaths: string[] = [];
const capturedTrees = new Map<string, string>();
const statusEvents: NarraForkEvent[] = [];
const fileEvents: NarraForkEvent[] = [];
const onStatus = (event: NarraForkEvent) => {
	if (event.type === "narrator:ws_broadcast" && event.message.type === "git_status")
		statusEvents.push(event);
};
const onFiles = (event: NarraForkEvent) => fileEvents.push(event);

beforeEach(() => {
	statusEvents.length = 0;
	fileEvents.length = 0;
	eventBus.on("narrator:ws_broadcast", onStatus);
	eventBus.on("chapter:files_changed", onFiles);
});

async function createRepo(prefix: string): Promise<string> {
	const dir = mkdtempSync(join(tmpdir(), prefix));
	tempDirs.push(dir);
	snapshotPaths.push(normalizePathForComparison(dir));
	await safeSpawn({ cmd: ["git", "init"], cwd: dir, timeout: 15_000 });
	await safeSpawn({ cmd: ["git", "config", "user.email", "test@example.com"], cwd: dir });
	await safeSpawn({ cmd: ["git", "config", "user.name", "Test"], cwd: dir });
	writeFileSync(join(dir, "seed.txt"), "seed\n");
	await safeSpawn({ cmd: ["git", "add", "-A"], cwd: dir, timeout: 15_000 });
	await safeSpawn({ cmd: ["git", "commit", "-m", "seed"], cwd: dir, timeout: 15_000 });
	return dir;
}

/**
 * Register a watcher entry without starting a timer or a native subscription.
 *
 * `watch()` would install a real 5 s interval, and the test would then be racing it.
 * Ticks are driven explicitly instead, which is also what makes the polled/event
 * distinction observable at all.
 */
function registerEntry(worktreePath: string): void {
	worktreeWatcher._entries.set(worktreePath, {
		chapterId: `chapter-${Math.random().toString(36).slice(2, 8)}`,
		narratorIds: new Set(["narrator-test"]),
		locale: "en",
		processing: false,
		pendingProcess: false,
		rateLimit: { windowStart: Date.now(), count: 0, warned: false },
	});
}

/** Count `tryCaptureHot` calls for the duration of `run`, keeping the real behaviour. */
async function countCaptures(run: () => Promise<void>): Promise<number> {
	const original = worktreeTreeSnapshot.tryCaptureHot;
	let calls = 0;
	worktreeTreeSnapshot.tryCaptureHot = async function (...args) {
		calls += 1;
		const tree = await original.apply(this, args);
		expect(tree).toBeString();
		if (tree) capturedTrees.set(args[0], tree);
		return tree;
	};
	try {
		await run();
	} finally {
		worktreeTreeSnapshot.tryCaptureHot = original;
	}
	return calls;
}

afterEach(async () => {
	eventBus.off("narrator:ws_broadcast", onStatus);
	eventBus.off("chapter:files_changed", onFiles);
	clearStatusCache();
	capturedTrees.clear();
	for (const dir of tempDirs.splice(0)) {
		worktreeWatcher.unwatchAll(dir);
		await worktreeTreeSnapshot.destroy(dir, undefined, { force: true }).catch(() => {});
		rmSync(dir, { recursive: true, force: true });
	}
	for (const path of snapshotPaths.splice(0)) {
		await db.delete(worktreeTreeSnapshots).where(eq(worktreeTreeSnapshots.worktreePath, path));
	}
});

describe("watcher polling ticks", () => {
	for (const triggers of [
		[true, true],
		[true, false, true],
		[false, true],
	]) {
		test(`coalesced triggers retain poll-only provenance: ${JSON.stringify(triggers)}`, async () => {
			const repo = await createRepo("nf-watch-coalesced-");
			registerEntry(repo);
			const entry = worktreeWatcher._entries.get(repo);
			if (!entry) throw new Error("entry was not registered");
			let release!: () => void;
			const pending = new Promise<void>((resolve) => {
				release = resolve;
			});
			const process = spyOn(worktreeWatcher, "_processChange")
				.mockImplementationOnce(() => pending)
				.mockResolvedValue(undefined);
			try {
				worktreeWatcher._enqueueProcessChange(repo, entry, true);
				for (const polled of triggers) {
					worktreeWatcher._enqueueProcessChange(repo, entry, polled);
				}
				expect(process).toHaveBeenCalledTimes(1);
				release();
				await new Promise<void>((resolve) => setImmediate(resolve));
				expect(process).toHaveBeenCalledTimes(2);
				expect(process.mock.calls[1][2]).toBe(triggers.every(Boolean));
				expect(entry.pendingProcess).toBe(false);
			} finally {
				release();
				process.mockRestore();
			}
		});
	}

	test("idle polls preserve shared caches; writes and sweeps invalidate once without rereading", async () => {
		const repo = await createRepo("nf-watch-cache-");
		registerEntry(repo);
		const entry = worktreeWatcher._entries.get(repo);
		if (!entry) throw new Error("entry was not registered");
		await worktreeWatcher._processChange(repo, entry, true);
		const invalidate = spyOn(statusCache, "invalidateStatus");
		const query = spyOn(statusStream, "readGitStatusStream");
		try {
			for (let tick = 0; tick < 3; tick++) {
				await worktreeWatcher._processChange(repo, entry, true);
			}
			expect(invalidate).not.toHaveBeenCalled();
			expect(query).toHaveBeenCalledTimes(3);
			// The latest idle probe remains reusable by legacy narrator readers.
			await statusCache.getStatusSummaryCached(repo);
			expect(query).toHaveBeenCalledTimes(3);

			writeFileSync(join(repo, "seed.txt"), "external write\n");
			await worktreeWatcher._processChange(repo, entry, true);
			expect(invalidate).toHaveBeenCalledTimes(1);
			expect(query).toHaveBeenCalledTimes(4);

			entry.skippedPolls = MAX_SKIPPED_POLLS;
			await worktreeWatcher._processChange(repo, entry, true);
			expect(invalidate).toHaveBeenCalledTimes(2);
			expect(query).toHaveBeenCalledTimes(5);

			// Native events are write evidence even if the structural status is unchanged.
			await worktreeWatcher._processChange(repo, entry, false);
			expect(invalidate).toHaveBeenCalledTimes(3);
			expect(query).toHaveBeenCalledTimes(6);
		} finally {
			invalidate.mockRestore();
			query.mockRestore();
		}
	});

	test("an idle polled tick takes no snapshot", async () => {
		const repo = await createRepo("nf-watch-idle-");
		registerEntry(repo);
		const registered = worktreeWatcher._entries.get(repo);
		if (!registered) throw new Error("entry was not registered");

		// The first tick has no previous signature to compare against, so it must still
		// capture — otherwise a workspace would never get an initial boundary.
		const first = await countCaptures(async () => {
			await worktreeWatcher._processChange(repo, registered, true);
		});
		expect(first).toBe(1);

		// Nothing was written between the ticks, which is the state a polled watcher is in
		// essentially all the time.
		const idle = await countCaptures(async () => {
			await worktreeWatcher._processChange(repo, registered, true);
			await worktreeWatcher._processChange(repo, registered, true);
			await worktreeWatcher._processChange(repo, registered, true);
		});
		expect(idle).toBe(0);
		expect(statusEvents).toHaveLength(1);
		expect(fileEvents).toHaveLength(1);
	});

	test("a polled tick after a real edit still captures", async () => {
		const repo = await createRepo("nf-watch-changed-");
		registerEntry(repo);
		const entry = worktreeWatcher._entries.get(repo);
		if (!entry) throw new Error("entry was not registered");
		await worktreeWatcher._processChange(repo, entry, true);

		// A write by anything outside the tool path — a terminal command, the user's
		// editor, a build script — is precisely what the boundary exists to make
		// revertable, so skipping here would be a data-safety regression rather than a
		// saving.
		writeFileSync(join(repo, "seed.txt"), "edited outside the tool path\n");
		const captures = await countCaptures(async () => {
			await worktreeWatcher._processChange(repo, entry, true);
		});
		expect(captures).toBe(1);
		const captured = capturedTrees.get(repo);
		if (!captured) throw new Error("No actual watcher capture");
		expect(await worktreeTreeSnapshot.readFileAtTree(repo, captured, "seed.txt")).toBe(
			"edited outside the tool path\n",
		);
	});

	test("native ignored-file events publish paths before status and retain external attribution", async () => {
		const repo = await createRepo("nf-watch-native-ignored-");
		writeFileSync(join(repo, ".gitignore"), "build/\n");
		await safeSpawn({ cmd: ["git", "add", ".gitignore"], cwd: repo, timeout: 15_000 });
		await safeSpawn({ cmd: ["git", "commit", "-m", "ignore"], cwd: repo, timeout: 15_000 });
		registerEntry(repo);
		const entry = worktreeWatcher._entries.get(repo);
		if (!entry) throw new Error("entry was not registered");
		await worktreeWatcher._processChange(repo, entry, true);
		const signature = entry.lastStatusSignature;
		mkdirSync(join(repo, "build"));
		const path = join(repo, "build", "asset.txt");
		writeFileSync(path, "ignored artifact\n");
		entry.pendingPaths = new Map([[path, "added"]]);
		const changedPaths = entry.pendingPaths;
		const paths: NarraForkEvent[] = [];
		const listener = (event: NarraForkEvent) => {
			if (
				event.type === "narrator:ws_broadcast" &&
				event.message.type === "workspace_paths_changed"
			)
				paths.push(event);
		};
		eventBus.on("narrator:ws_broadcast", listener);
		const original = gitService.getStatusSummary;
		const query = spyOn(gitService, "getStatusSummary").mockImplementation((...args) => {
			expect(paths).toHaveLength(1);
			return original.apply(gitService, args);
		});
		const attribute = spyOn(worktreeWatcher, "_recordExternalChanges");
		try {
			await worktreeWatcher._processChange(repo, entry, false);
			expect(entry.lastStatusSignature).toBe(signature);
			expect(attribute).toHaveBeenCalledWith(repo, entry, changedPaths, false);
			expect(paths[0]).toMatchObject({
				type: "narrator:ws_broadcast",
				narratorId: "narrator-test",
				message: {
					type: "workspace_paths_changed",
					changes: [{ path: "build/asset.txt", kind: "added" }],
					truncated: false,
				},
			});
		} finally {
			eventBus.off("narrator:ws_broadcast", listener);
			query.mockRestore();
			attribute.mockRestore();
		}
	});

	test("a watcher event captures even when git status is unchanged", async () => {
		const repo = await createRepo("nf-watch-event-");
		registerEntry(repo);
		const entry = worktreeWatcher._entries.get(repo);
		if (!entry) throw new Error("entry was not registered");
		await worktreeWatcher._processChange(repo, entry, true);

		// `polled = false` means the native watcher reported an actual filesystem event,
		// which is evidence of a write. The skip is only ever applied to a speculative
		// timer tick, so this path stays unconditional.
		const signature = entry.lastStatusSignature;
		const captures = await countCaptures(async () => {
			await worktreeWatcher._processChange(repo, entry, false);
		});
		expect(captures).toBe(1);
		expect(entry.lastStatusSignature).toBe(signature);
		expect(statusEvents).toHaveLength(2);
		expect(fileEvents).toHaveLength(2);
	});

	test("a change to a tracked-but-ignored file is not mistaken for an idle tick", async () => {
		const repo = await createRepo("nf-watch-tracked-ignored-");
		writeFileSync(join(repo, ".gitignore"), ".env\n");
		writeFileSync(join(repo, ".env"), "SECRET=1\n");
		await safeSpawn({
			cmd: ["git", "add", "-f", ".env", ".gitignore"],
			cwd: repo,
			timeout: 15_000,
		});
		await safeSpawn({ cmd: ["git", "commit", "-m", "track"], cwd: repo, timeout: 15_000 });
		registerEntry(repo);
		const entry = worktreeWatcher._entries.get(repo);
		if (!entry) throw new Error("entry was not registered");
		await worktreeWatcher._processChange(repo, entry, true);

		// The one case where "ignored files are invisible to git status" could have been a
		// hole in the skip reasoning: a tracked-but-ignored file *is* in the snapshot tree,
		// so a missed change would be unrevertable. git reports it as ` M`, so the
		// signature moves and the tick is not treated as idle.
		writeFileSync(join(repo, ".env"), "SECRET=rotated\n");
		const captures = await countCaptures(async () => {
			await worktreeWatcher._processChange(repo, entry, true);
		});
		expect(captures).toBe(1);
		const captured = capturedTrees.get(repo);
		if (!captured) throw new Error("No tracked-ignored watcher capture");
		expect(await worktreeTreeSnapshot.readFileAtTree(repo, captured, ".env")).toBe(
			"SECRET=rotated\n",
		);
	});

	for (const content of ["bbbb\n", "bbbb\nanother line\n"]) {
		test(`dirty-to-dirty edit immediately captures and notifies (${content.length} bytes)`, async () => {
			const repo = await createRepo("nf-watch-dirty-edit-");
			registerEntry(repo);
			const entry = worktreeWatcher._entries.get(repo);
			if (!entry) throw new Error("entry was not registered");
			writeFileSync(join(repo, "seed.txt"), "aaaa\n");
			await worktreeWatcher._processChange(repo, entry, true);
			const signature = entry.lastStatusSignature;

			writeFileSync(join(repo, "seed.txt"), content);
			const immediate = await countCaptures(async () => {
				await worktreeWatcher._processChange(repo, entry, true);
			});
			expect(entry.lastStatusSignature).toBe(signature);
			expect(immediate).toBe(1);
			expect(statusEvents).toHaveLength(2);
			expect(fileEvents).toHaveLength(2);
			const tree = capturedTrees.get(repo);
			if (!tree) throw new Error("No actual watcher capture");
			expect(await worktreeTreeSnapshot.readFileAtTree(repo, tree, "seed.txt")).toBe(content);
		});
	}

	test("subdirectory polling captures repeated dirty edits using real CWD-relative status", async () => {
		const repo = await createRepo("nf-watch-subdir-");
		const cwd = join(repo, "nested");
		mkdirSync(cwd);
		writeFileSync(join(cwd, "seed.txt"), "seed\n");
		await safeSpawn({ cmd: ["git", "add", "nested/seed.txt"], cwd: repo, timeout: 15_000 });
		await safeSpawn({ cmd: ["git", "commit", "-m", "nested"], cwd: repo, timeout: 15_000 });
		registerEntry(cwd);
		snapshotPaths.push(normalizePathForComparison(cwd));
		const entry = worktreeWatcher._entries.get(cwd);
		if (!entry) throw new Error("entry was not registered");
		try {
			writeFileSync(join(cwd, "seed.txt"), "aaaa\n");
			await worktreeWatcher._processChange(cwd, entry, true);
			const signature = entry.lastStatusSignature;
			expect((await gitService.getStatusSummary(cwd)).files).toEqual([
				{ path: "seed.txt", status: " M" },
			]);
			for (const content of ["bbbb\n", "bbbb\nanother line\n"]) {
				writeFileSync(join(cwd, "seed.txt"), content);
				const captures = await countCaptures(async () => {
					await worktreeWatcher._processChange(cwd, entry, true);
				});
				expect(captures).toBe(1);
				expect(entry.lastStatusSignature).toBe(signature);
				const tree = capturedTrees.get(cwd);
				if (!tree) throw new Error("No actual subdirectory watcher capture");
				expect(await worktreeTreeSnapshot.readFileAtTree(cwd, tree, "seed.txt")).toBe(content);
			}
			expect(statusEvents).toHaveLength(3);
			expect(fileEvents).toHaveLength(3);
		} finally {
			worktreeWatcher.unwatchAll(cwd);
			await worktreeTreeSnapshot.destroy(cwd, undefined, { force: true });
		}
	});

	test("the sweep does not fire again until another run of skips accumulates", async () => {
		// The counter has to reset on a capture, not merely stop growing: leaving it at the
		// threshold would make every subsequent idle tick a forced capture, which is the
		// unconditional behaviour the skip exists to avoid.
		const repo = await createRepo("nf-watch-sweep-reset-");
		registerEntry(repo);
		const entry = worktreeWatcher._entries.get(repo);
		if (!entry) throw new Error("entry was not registered");
		await worktreeWatcher._processChange(repo, entry, true);

		// Identical metadata is not proof of identical content: keep a bounded sweep,
		// and notify clients when it fires even though the structural status is unchanged.
		const swept = await countCaptures(async () => {
			for (let tick = 0; tick <= MAX_SKIPPED_POLLS; tick++) {
				await worktreeWatcher._processChange(repo, entry, true);
			}
		});
		expect(swept).toBe(1);
		expect(entry.skippedPolls).toBe(0);
		expect(statusEvents).toHaveLength(2);
		expect(fileEvents).toHaveLength(2);

		const afterSweep = await countCaptures(async () => {
			for (let tick = 0; tick < MAX_SKIPPED_POLLS - 1; tick++) {
				await worktreeWatcher._processChange(repo, entry, true);
			}
		});
		expect(afterSweep).toBe(0);
	});

	test("index-only metadata changes capture and notify without a structural change", async () => {
		const repo = await createRepo("nf-watch-index-");
		registerEntry(repo);
		const entry = worktreeWatcher._entries.get(repo);
		if (!entry) throw new Error("entry was not registered");
		await worktreeWatcher._processChange(repo, entry, true);
		const signature = entry.lastStatusSignature;
		const changed = await safeSpawn({
			cmd: ["git", "update-index", "--assume-unchanged", "seed.txt"],
			cwd: repo,
			timeout: 15_000,
		});
		expect(changed.exitCode).toBe(0);
		const captures = await countCaptures(async () => {
			await worktreeWatcher._processChange(repo, entry, true);
		});
		expect(entry.lastStatusSignature).toBe(signature);
		expect(captures).toBe(1);
		expect(statusEvents).toHaveLength(2);
		expect(fileEvents).toHaveLength(2);
	});

	for (const partial of [{ truncated: true }, { totalFilesExact: false }]) {
		test(`partial status evidence refreshes conservatively: ${JSON.stringify(partial)}`, async () => {
			const repo = await createRepo("nf-watch-partial-");
			registerEntry(repo);
			const entry = worktreeWatcher._entries.get(repo);
			if (!entry) throw new Error("entry was not registered");
			const summary = { ...(await gitService.getStatusSummary(repo)), ...partial };
			const summarySpy = spyOn(gitService, "getStatusSummary").mockResolvedValue(summary);
			try {
				await worktreeWatcher._processChange(repo, entry, true);
				const captures = await countCaptures(async () => {
					await worktreeWatcher._processChange(repo, entry, true);
				});
				expect(captures).toBe(1);
				expect(statusEvents).toHaveLength(2);
				expect(fileEvents).toHaveLength(2);
			} finally {
				summarySpy.mockRestore();
			}
		});
	}

	test("the sweep captures bytes hidden by unchanged metadata and notifies", async () => {
		const repo = await createRepo("nf-watch-sweep-hidden-");
		registerEntry(repo);
		const entry = worktreeWatcher._entries.get(repo);
		if (!entry) throw new Error("entry was not registered");
		writeFileSync(join(repo, "seed.txt"), "aaaa\n");
		const metadataSpy = spyOn(worktreeWatcherMetadata, "probe").mockResolvedValue({
			fingerprint: "same-metadata",
			uncertain: false,
		});
		try {
			await worktreeWatcher._processChange(repo, entry, true);
			writeFileSync(join(repo, "seed.txt"), "bbbb\n");
			const idle = await countCaptures(async () => {
				for (let tick = 0; tick < MAX_SKIPPED_POLLS; tick++) {
					await worktreeWatcher._processChange(repo, entry, true);
				}
			});
			expect(idle).toBe(0);
			expect(statusEvents).toHaveLength(1);
			const sweep = await countCaptures(async () => {
				await worktreeWatcher._processChange(repo, entry, true);
			});
			expect(sweep).toBe(1);
			expect(statusEvents).toHaveLength(2);
			expect(fileEvents).toHaveLength(2);
			const tree = capturedTrees.get(repo);
			if (!tree) throw new Error("Sweep did not capture a tree");
			expect(await worktreeTreeSnapshot.readFileAtTree(repo, tree, "seed.txt")).toBe("bbbb\n");
		} finally {
			metadataSpy.mockRestore();
		}
	});

	test("a purely ignored file changing does leave a polled tick idle", async () => {
		const repo = await createRepo("nf-watch-ignored-");
		writeFileSync(join(repo, ".gitignore"), "build/\n");
		await safeSpawn({ cmd: ["git", "add", "-A"], cwd: repo, timeout: 15_000 });
		await safeSpawn({ cmd: ["git", "commit", "-m", "ignore"], cwd: repo, timeout: 15_000 });
		registerEntry(repo);
		const entry = worktreeWatcher._entries.get(repo);
		if (!entry) throw new Error("entry was not registered");
		await worktreeWatcher._processChange(repo, entry, true);

		// Skipping is correct rather than merely acceptable: the file is excluded from
		// snapshots, so a capture would have produced the identical tree and recorded
		// nothing. Losing no information is what makes the git-status signature a
		// sufficient judgement and not just a cheap heuristic.
		writeFileSync(join(repo, "build-artifact-ignored.tmp"), "x\n");
		rmSync(join(repo, "build-artifact-ignored.tmp"));
		const captures = await countCaptures(async () => {
			await worktreeWatcher._processChange(repo, entry, true);
		});
		expect(captures).toBe(0);
	});
});
