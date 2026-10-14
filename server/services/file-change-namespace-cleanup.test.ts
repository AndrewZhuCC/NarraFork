import { expect, spyOn } from "bun:test";
import { opendir as callbackOpendir, unlink as callbackUnlink, type Dir } from "node:fs";
import * as fs from "node:fs/promises";
import { join } from "node:path";
import { setImmediate as yieldToEventLoop } from "node:timers/promises";
import { promisify } from "node:util";
import {
	closeFixture,
	db,
	type FixtureLifetime,
	root,
	test,
} from "./__tests__/revert-transaction.fixture";
import {
	resetFileChangeNamespace,
	waitForRetiredFileChangeCleanup,
} from "./file-change-namespace-reset";

// Bun may retain a mutable spy wrapper on promises exports across isolated files.
// Delegate to independent callback APIs, never to an export that we spy on below.
const realUnlink = promisify(callbackUnlink);
const realOpendir = promisify(callbackOpendir);

function barrier() {
	let release!: () => void;
	const promise = new Promise<void>((done) => {
		release = done;
	});
	return { promise, release };
}
async function seedCache(name: string) {
	const ownerRoot = join(root, name);
	const privateRoot = join(ownerRoot, "private");
	const directory = join(privateRoot, "file-change-blobs", "nested");
	await fs.mkdir(directory, { recursive: true });
	await fs.writeFile(join(directory, "blob"), "retired bytes");
	return { ownerRoot, privateRoot };
}
async function reset(privateRoot: string) {
	await resetFileChangeNamespace({ db, privateRoot, assertBoundary: async () => {} });
}

for (const fail of [false, true]) {
	test(`retired cleanup ${fail ? "failure" : "success"} settles only after real directory handles close`, async () => {
		const { ownerRoot, privateRoot } = await seedCache("cleanup-owner");
		const entered = barrier();
		const gate = barrier();
		const directories: Dir[] = [];
		const directorySpy = spyOn(fs, "opendir").mockImplementation(async (path, options) => {
			const directory = await realOpendir(path, options);
			directories.push(directory);
			return directory;
		});
		let retiredFile: string | undefined;
		const spy = spyOn(fs, "unlink").mockImplementation(async (path) => {
			if (String(path).includes("file-change-blobs.retired-")) {
				retiredFile = String(path);
				// Production for-await owns both parent and nested directory handles here.
				entered.release();
				await gate.promise;
				if (fail) throw new Error("Injected retired unlink failure");
			}
			return realUnlink(path);
		});
		let waiting: Promise<void> | undefined;
		let closing: Promise<void> | undefined;
		try {
			await reset(privateRoot);
			await entered.promise;
			let settled = false;
			waiting = waitForRetiredFileChangeCleanup(privateRoot).then(() => {
				settled = true;
			});
			const owner: FixtureLifetime = {
				controller: new AbortController(),
				work: new Set(),
				holds: new Set(),
				restores: [],
				root: ownerRoot,
			};
			let closed = false;
			closing = closeFixture(owner).then(() => {
				closed = true;
			});
			await yieldToEventLoop();
			expect(settled).toBe(false);
			expect(closed).toBe(false);
			if (!retiredFile) throw new Error("Cleanup did not reach the real retired file");
			expect(await fs.readFile(retiredFile, "utf8")).toBe("retired bytes");
			gate.release();
			await waiting;
			expect(directories.length).toBe(2);
			for (const directory of directories)
				await expect(directory.read()).rejects.toMatchObject({ code: "ERR_DIR_CLOSED" });
			await closing;
			// Real fixture rm, including the failed cleanup's remaining cache, is required.
			expect(await fs.stat(ownerRoot).catch((error) => error.code)).toBe("ENOENT");
		} finally {
			gate.release();
			await waiting;
			await closing;
			await waitForRetiredFileChangeCleanup(privateRoot);
			spy.mockRestore();
			directorySpy.mockRestore();
		}
	});
}

test("retired cleanup waits for queued jobs of its root without blocking a drained different root", async () => {
	const other = await seedCache("other-owner");
	await reset(other.privateRoot);
	await waitForRetiredFileChangeCleanup(other.privateRoot);
	const { privateRoot } = await seedCache("blocked-owner");
	const entered = barrier();
	const gate = barrier();
	let removed = 0;
	const spy = spyOn(fs, "unlink").mockImplementation(async (path) => {
		if (String(path).startsWith(privateRoot)) {
			entered.release();
			await gate.promise;
			removed++;
		}
		return realUnlink(path);
	});
	try {
		await reset(privateRoot);
		await entered.promise;
		await fs.writeFile(join(privateRoot, "file-change-blobs", "second"), "queued bytes");
		await reset(privateRoot);
		let settled = false;
		const waiting = waitForRetiredFileChangeCleanup(privateRoot).then(() => {
			settled = true;
		});
		await waitForRetiredFileChangeCleanup(other.privateRoot);
		expect(settled).toBe(false);
		gate.release();
		await waiting;
		expect(removed).toBe(2);
		expect((await fs.readdir(privateRoot)).some((name) => name.includes(".retired-"))).toBe(false);
	} finally {
		gate.release();
		await waitForRetiredFileChangeCleanup(privateRoot);
		spy.mockRestore();
	}
});
