import { describe, expect, test } from "bun:test";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { FileHistoryTarget } from "@shared/file-change-protocol";
import { FILE_WRITE_QUEUE_LIMITS, FileWriteQueue, withFileWrite } from "./file-write-queue";

const target = (path: string, deviceId = "local"): FileHistoryTarget => ({
	deviceId,
	pathFlavor: "posix",
	canonicalPath: path,
});
const tick = () => new Promise<void>((resolve) => setImmediate(resolve));

function gate() {
	let open!: () => void;
	const promise = new Promise<void>((resolve) => {
		open = resolve;
	});
	return { promise, open };
}

describe("single-file write queue", () => {
	test("same canonical file is FIFO while other paths and devices run", async () => {
		const queue = new FileWriteQueue();
		const hold = gate();
		const order: string[] = [];
		const first = queue.run(target("/repo/a"), async () => {
			order.push("first");
			await hold.promise;
		});
		const second = queue.run(target("/repo/./a"), () => {
			order.push("second");
		});
		const third = queue.run(target("/repo/a"), () => {
			order.push("third");
		});
		try {
			await queue.run(target("/repo/b"), () => order.push("other-file"));
			await queue.run(target("/repo/a", "remote"), () => order.push("other-device"));
			expect(order).toEqual(["first", "other-file", "other-device"]);
			expect(queue.waitingCount).toBe(2);
		} finally {
			hold.open();
		}
		await Promise.all([first, second, third]);
		expect(order.slice(-2)).toEqual(["second", "third"]);
		expect(queue.activeFileCount).toBe(0);
		expect(queue.waitingCount).toBe(0);
	});

	test("cancelled waiter is removed without releasing the running file", async () => {
		const queue = new FileWriteQueue();
		const hold = gate();
		const controller = new AbortController();
		const first = queue.run(target("/cancel"), () => hold.promise);
		const cancelled = queue.run(
			target("/cancel"),
			() => {
				throw new Error("cancelled body ran");
			},
			{ signal: controller.signal },
		);
		controller.abort(new Error("cancelled"));
		try {
			await expect(cancelled).rejects.toThrow("cancelled");
			expect(queue.waitingCount).toBe(0);
			await expect(
				queue.run(target("/cancel"), () => {}, { waitTimeoutMs: 0 }),
			).rejects.toMatchObject({ code: "file_busy" });
		} finally {
			hold.open();
		}
		await first;
		await expect(queue.run(target("/cancel"), () => "next")).resolves.toBe("next");
		expect(queue.activeFileCount).toBe(0);
	});

	test("a running body's cancellation waits for actual completion", async () => {
		const queue = new FileWriteQueue();
		const started = gate();
		const hold = gate();
		const controller = new AbortController();
		let nextRan = false;
		const first = queue.run(
			target("/running"),
			async () => {
				started.open();
				await hold.promise;
			},
			{ signal: controller.signal },
		);
		await started.promise;
		controller.abort();
		const next = queue.run(target("/running"), () => {
			nextRan = true;
		});
		try {
			await tick();
			expect(nextRan).toBe(false);
		} finally {
			hold.open();
		}
		await Promise.all([first, next]);
		expect(nextRan).toBe(true);
	});

	test("timeout releases only its waiting slot", async () => {
		const queue = new FileWriteQueue();
		const hold = gate();
		const first = queue.run(target("/timeout"), () => hold.promise);
		try {
			await expect(
				queue.run(target("/timeout"), () => {}, { waitTimeoutMs: 5 }),
			).rejects.toMatchObject({ code: "file_busy" });
			expect(queue.activeFileCount).toBe(1);
			expect(queue.waitingCount).toBe(0);
			await expect(queue.run(target("/unrelated"), () => 42)).resolves.toBe(42);
		} finally {
			hold.open();
		}
		await first;
		expect(queue.activeFileCount).toBe(0);
	});

	test("synchronous and asynchronous errors both drain the queue", async () => {
		const queue = new FileWriteQueue();
		const first = queue.run(target("/errors"), () => {
			throw new Error("sync");
		});
		const second = queue.run(target("/errors"), async () => {
			throw new Error("async");
		});
		const last = queue.run(target("/errors"), () => "ok");
		const results = await Promise.allSettled([first, second, last]);
		expect(results.map((result) => result.status)).toEqual(["rejected", "rejected", "fulfilled"]);
		await expect(last).resolves.toBe("ok");
		expect(queue.activeFileCount).toBe(0);
		expect(queue.waitingCount).toBe(0);
	});

	test("Windows aliases share a queue; POSIX backslashes remain filename data", async () => {
		const queue = new FileWriteQueue();
		const hold = gate();
		const windows = {
			deviceId: "windows",
			pathFlavor: "windows" as const,
			canonicalPath: "C:\\Repo\\A.txt",
		};
		const first = queue.run(windows, () => hold.promise);
		try {
			await expect(
				queue.run({ ...windows, canonicalPath: "c:/repo/a.txt" }, () => {}, { waitTimeoutMs: 0 }),
			).rejects.toMatchObject({ code: "file_busy" });
			await queue.run(target("/repo/a\\b"), () =>
				queue.run(target("/repo/a/b"), () => "independent"),
			);
		} finally {
			hold.open();
		}
		await first;
	});

	test("queue capacity is temporary and has no persistent aftermath", async () => {
		const queue = new FileWriteQueue();
		const hold = gate();
		const first = queue.run(target("/capacity"), () => hold.promise);
		const waiting = Array.from({ length: FILE_WRITE_QUEUE_LIMITS.maxWaitersPerFile }, () =>
			queue.run(target("/capacity"), () => {}),
		);
		try {
			await expect(queue.run(target("/capacity"), () => {})).rejects.toMatchObject({
				code: "queue_full",
			});
			await expect(queue.run(target("/free"), () => true)).resolves.toBe(true);
		} finally {
			hold.open();
		}
		await Promise.all([first, ...waiting]);
		expect(queue.waitingCount).toBe(0);
		await expect(queue.run(target("/capacity"), () => true)).resolves.toBe(true);
	});

	test("module reevaluation shares live queues", async () => {
		const hold = gate();
		const path = target("/hot-reload-test");
		const first = withFileWrite(path, () => hold.promise);
		try {
			const specifier = "./file-write-queue.ts?file-write-queue-reload-test";
			const reloaded: typeof import("./file-write-queue") = await import(specifier);
			await expect(
				reloaded.withFileWrite(path, () => {}, { waitTimeoutMs: 0 }),
			).rejects.toMatchObject({ code: "file_busy" });
		} finally {
			hold.open();
		}
		await first;
	});

	test("serializes actual read-modify-write without losing updates", async () => {
		const root = await mkdtemp(join(tmpdir(), "file-write-queue-"));
		const file = join(root, "counter");
		const queue = new FileWriteQueue();
		await writeFile(file, "0");
		try {
			await Promise.all(
				Array.from({ length: 20 }, () =>
					queue.run(
						{
							deviceId: "local",
							pathFlavor: process.platform === "win32" ? "windows" : "posix",
							canonicalPath: file,
						},
						async () => {
							const before = Number(await readFile(file, "utf8"));
							await Bun.write(file, String(before + 1));
						},
					),
				),
			);
			expect(await readFile(file, "utf8")).toBe("20");
			expect(queue.activeFileCount).toBe(0);
		} finally {
			await rm(root, { recursive: true, force: true });
		}
	});

	test("invalid targets and pre-aborted requests never allocate queues", async () => {
		const queue = new FileWriteQueue();
		await expect(queue.run(target("relative"), () => {})).rejects.toThrow("absolute");
		await expect(queue.run(target("/a"), () => {}, { waitTimeoutMs: Infinity })).rejects.toThrow(
			"timeout",
		);
		const signal = AbortSignal.abort(new Error("already cancelled"));
		await expect(queue.run(target("/a"), () => {}, { signal })).rejects.toThrow(
			"already cancelled",
		);
		expect(queue.activeFileCount).toBe(0);
	});
});
