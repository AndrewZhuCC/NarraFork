import { describe, expect, mock, test } from "bun:test";
import { type GitDiscovery, GitDiscoveryCache } from "./git-discovery-cache";

const ready: GitDiscovery = {
	state: "ready",
	rootPath: "/repo",
	repositoryPath: "/repo/.git",
	branch: "main",
};
const deferred = () => {
	let resolve!: (value: GitDiscovery) => void;
	const promise = new Promise<GitDiscovery>((done) => {
		resolve = done;
	});
	return { promise, resolve };
};

describe("Git discovery stale-while-revalidate", () => {
	test("peek returns cloned FS facts; stale requests return immediately and refresh once", async () => {
		let now = 0;
		const cache = new GitDiscoveryCache(8, 10, () => now, true);
		await cache.get("local", 0, "/repo", async () => ready);
		const peek = cache.peek("local", 0, "/repo");
		if (peek) peek.branch = "edited";
		expect(cache.peek("local", 0, "/repo")?.branch).toBe("main");
		const wait = deferred();
		const refresh = mock(() => wait.promise);
		now = 11;
		expect((await cache.get("local", 0, "/repo", refresh)).branch).toBe("main");
		expect((await cache.get("local", 0, "/repo", refresh)).branch).toBe("main");
		expect(refresh).toHaveBeenCalledTimes(1);
		wait.resolve({ ...ready, branch: "new" });
		// Joining the same probe with a cold alias is unnecessary: flush microtasks only.
		for (let i = 0; i < 8; i++) await Promise.resolve();
		expect(cache.peek("local", 0, "/repo")?.branch).toBe("new");
	});

	test("all foreground and background probes share a concurrency ceiling and queue bound", async () => {
		const cache = new GitDiscoveryCache(64, 10, Date.now, true, 2);
		let active = 0;
		let maximum = 0;
		const waits = Array.from({ length: 8 }, deferred);
		const tasks = waits.map((wait, index) =>
			cache.get("local", 0, `/repo-${index}`, async () => {
				active++;
				maximum = Math.max(maximum, active);
				const result = await wait.promise;
				active--;
				return result;
			}),
		);
		await Promise.resolve();
		expect(active).toBe(2);
		for (const wait of waits) wait.resolve(ready);
		await Promise.all(tasks);
		expect(maximum).toBe(2);
		const blocked = deferred();
		const queued = Array.from({ length: 34 }, (_, i) =>
			cache.get("local", 0, `/blocked-${i}`, () => blocked.promise),
		);
		await expect(cache.get("local", 0, "/overflow", () => blocked.promise)).rejects.toThrow(
			"queue is full",
		);
		blocked.resolve(ready);
		await Promise.all(queued);
	});
});
