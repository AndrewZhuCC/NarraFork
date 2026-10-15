import { describe, expect, test } from "bun:test";
import { EventEmitter } from "node:events";
import type { Worker } from "node:worker_threads";
import type { RepairEvidence } from "../lib/usage-token-repair-evidence";
import { createRepairEvidencePool } from "./usage-token-repair-evidence-client";
import type { RepairRequestRow } from "./usage-token-repair-store";

const row = {
	id: "one",
	provider: null,
	model: "custom",
	inputTokens: 100,
	cachedInputTokens: 0,
	cacheCreationInputTokens: 0,
	cacheCreation5mTokens: 0,
	cacheCreation1hTokens: 0,
} as RepairRequestRow;
const raw = JSON.stringify({
	request: { url: "/v1/responses" },
	response: {
		bodyText: JSON.stringify({
			object: "response",
			status: "completed",
			usage: { input_tokens: 100 },
		}),
	},
});
const result: RepairEvidence = {
	classification: "correct",
	protocol: "openai",
	reason: "matches_protocol_usage",
	before: row,
	after: row,
	evidence: "",
};
class FakeWorker extends EventEmitter {
	terminated = false;
	messages: { id: number }[] = [];
	constructor(
		readonly reply: boolean,
		readonly duplicate = false,
	) {
		super();
		queueMicrotask(() => this.emit("message", { ready: "token-repair-evidence-v1" }));
	}
	postMessage(message: { id: number }) {
		this.messages.push(message);
		if (this.reply)
			queueMicrotask(() => {
				this.emit("message", { id: message.id, result });
				if (this.duplicate) this.emit("message", { id: message.id, error: "stale duplicate" });
			});
	}
	async terminate() {
		this.terminated = true;
		this.emit("exit", 0);
		return 0;
	}
}
const signal = () => new AbortController().signal;

describe("persistent repair evidence pool", () => {
	test("real isolate handles repeated records without one worker per record", async () => {
		const pool = createRepairEvidencePool(".", 1);
		try {
			for (let i = 0; i < 10; i++)
				expect((await pool.analyze(row, raw, signal())).classification).toBe("correct");
		} finally {
			await pool.close();
		}
	});
	test("correlates duplicate replies and reuses a worker", async () => {
		const workers: FakeWorker[] = [];
		const pool = createRepairEvidencePool(".", 1, {
			createWorker: () => {
				const worker = new FakeWorker(true, true);
				workers.push(worker);
				return worker as unknown as Worker;
			},
		});
		try {
			const records = Array.from({ length: 8 }, () => pool.analyze(row, raw, signal()));
			expect(
				(await Promise.all(records)).every((entry) => entry.classification === "correct"),
			).toBe(true);
			expect(workers).toHaveLength(1);
			expect(workers[0].messages).toHaveLength(8);
		} finally {
			await pool.close();
		}
		expect(workers[0].terminated).toBe(true);
	});
	test("record timeout is an explicit skip and replaces worker", async () => {
		const workers: FakeWorker[] = [];
		const pool = createRepairEvidencePool(".", 1, {
			timeoutMs: 20,
			createWorker: () => {
				const worker = new FakeWorker(workers.length > 0);
				workers.push(worker);
				return worker as unknown as Worker;
			},
		});
		try {
			expect(await pool.analyze(row, raw, signal())).toMatchObject({
				classification: "insufficient",
				reason: "evidence_timeout",
				after: null,
			});
			expect(workers[0].terminated).toBe(true);
			expect((await pool.analyze(row, raw, signal())).classification).toBe("correct");
			expect(workers).toHaveLength(2);
		} finally {
			await pool.close();
		}
	});
	test("a crashed execution is skipped and replaced without poisoning subsequent tasks", async () => {
		const workers: FakeWorker[] = [];
		const pool = createRepairEvidencePool(".", 1, {
			createWorker: () => {
				const worker = new FakeWorker(workers.length > 0);
				if (!workers.length)
					worker.postMessage = () => {
						queueMicrotask(() => worker.emit("error", new Error("worker crashed")));
					};
				workers.push(worker);
				return worker as unknown as Worker;
			},
		});
		try {
			const failed = pool.analyze(row, raw, signal());
			expect(await failed).toMatchObject({
				classification: "insufficient",
				reason: "evidence_worker_failed",
				after: null,
			});
			expect(workers[0].terminated).toBe(true);
			expect((await pool.analyze(row, raw, signal())).classification).toBe("correct");
		} finally {
			await pool.close();
		}
	});
	test("startup is also covered by the hard timeout", async () => {
		class SilentWorker extends EventEmitter {
			terminated = false;
			async terminate() {
				this.terminated = true;
				this.emit("exit", 0);
				return 0;
			}
		}
		const worker = new SilentWorker();
		const pool = createRepairEvidencePool(".", 1, {
			timeoutMs: 20,
			createWorker: () => worker as unknown as Worker,
		});
		try {
			await expect(pool.analyze(row, raw, signal())).rejects.toThrow("five-second");
			expect(worker.terminated).toBe(true);
		} finally {
			await pool.close();
		}
	});
	test("cancellation terminates active isolate; close rejects queue and is idempotent", async () => {
		const workers: FakeWorker[] = [];
		const pool = createRepairEvidencePool(".", 1, {
			createWorker: () => {
				const worker = new FakeWorker(false);
				workers.push(worker);
				return worker as unknown as Worker;
			},
		});
		const controller = new AbortController();
		const active = pool.analyze(row, raw, controller.signal);
		controller.abort();
		await expect(active).rejects.toThrow("cancelled");
		expect(workers[0].terminated).toBe(true);
		const pending = pool.analyze(row, raw, signal());
		const queued = pool.analyze(row, raw, signal());
		const closing = pool.close();
		await expect(pending).rejects.toThrow("closed");
		await expect(queued).rejects.toThrow("closed");
		await closing;
		await pool.close();
		await expect(pool.analyze(row, raw, signal())).rejects.toThrow("closed");
	});
	test("four-slot configuration never exceeds the 64MiB in-flight reservation", async () => {
		const workers: FakeWorker[] = [];
		const pool = createRepairEvidencePool(".", 4, {
			createWorker: () => {
				const worker = new FakeWorker(false);
				workers.push(worker);
				return worker as unknown as Worker;
			},
		});
		const first = pool.analyze(row, raw, signal()).catch((error: Error) => error.message);
		const second = pool.analyze(row, raw, signal()).catch((error: Error) => error.message);
		try {
			await expect(pool.analyze(row, raw, signal())).rejects.toThrow("budget");
			expect(workers).toHaveLength(2);
		} finally {
			await pool.close();
		}
		expect(await first).toContain("closed");
		expect(await second).toContain("closed");
		expect(() => createRepairEvidencePool(".", 5)).toThrow("1..4");
	});
	test("budgets and missing dumps never start worker", async () => {
		let count = 0;
		const pool = createRepairEvidencePool(".", 1, {
			createWorker: () => {
				count++;
				return new FakeWorker(true) as unknown as Worker;
			},
		});
		try {
			expect((await pool.analyze(row, null, signal())).reason).toBe("dump_missing");
			await expect(pool.analyze(row, "x".repeat(32 * 1024 * 1024 + 1), signal())).rejects.toThrow(
				"budget",
			);
			expect(count).toBe(0);
		} finally {
			await pool.close();
		}
	});
});
