import { randomUUID } from "node:crypto";
import { logger } from "../logger";

export interface UsageQueueItem {
	id: string;
	credentialId: string;
	status: "pending" | "processing" | "done" | "failed";
	error?: string;
	addedAt: number;
	startedAt?: number;
	finishedAt?: number;
}

export interface UsageQueueSnapshot {
	items: UsageQueueItem[];
	isRunning: boolean;
}

type Executor = (credentialId: string) => Promise<unknown>;

const INTER_TASK_DELAY_MS = 500;
const COMPLETED_TTL_MS = 60_000;

const items: UsageQueueItem[] = [];
let running = false;
let executor: Executor | null = null;
let cleanupTimer: ReturnType<typeof setTimeout> | null = null;

function getSnapshot(): UsageQueueSnapshot {
	return { items: [...items], isRunning: running };
}

function safeProcessNext(): void {
	processNext().catch((err) => {
		logger.error("kiro usage queue: unexpected error", {
			error: err instanceof Error ? err.message : String(err),
		});
		running = false;
	});
}

function scheduleCleanup(): void {
	if (cleanupTimer) return;
	cleanupTimer = setTimeout(() => {
		cleanupTimer = null;
		const now = Date.now();
		for (let i = items.length - 1; i >= 0; i--) {
			const item = items[i];
			if (
				(item.status === "done" || item.status === "failed") &&
				item.finishedAt != null &&
				now - item.finishedAt > COMPLETED_TTL_MS
			) {
				items.splice(i, 1);
			}
		}
		if (items.some((item) => item.status === "done" || item.status === "failed")) {
			scheduleCleanup();
		}
	}, COMPLETED_TTL_MS);
}

async function processNext(): Promise<void> {
	const next = items.find((item) => item.status === "pending");
	if (!next) {
		running = false;
		return;
	}
	if (!executor) {
		logger.warn("kiro usage queue: no executor registered");
		running = false;
		return;
	}

	running = true;
	next.status = "processing";
	next.startedAt = Date.now();

	try {
		await executor(next.credentialId);
		next.status = "done";
	} catch (err) {
		next.status = "failed";
		next.error = err instanceof Error ? err.message : String(err);
		logger.warn(`kiro usage queue: failed for ${next.credentialId}: ${next.error}`);
	}

	next.finishedAt = Date.now();
	scheduleCleanup();
	await new Promise((resolve) => setTimeout(resolve, INTER_TASK_DELAY_MS));
	safeProcessNext();
}

export const kiroUsageFetchQueue = {
	setExecutor(fn: Executor): void {
		executor = fn;
	},
	enqueue(credentialId: string): void {
		const existing = items.find(
			(item) =>
				item.credentialId === credentialId &&
				(item.status === "pending" || item.status === "processing"),
		);
		if (existing) return;
		items.push({
			id: randomUUID().slice(0, 8),
			credentialId,
			status: "pending",
			addedAt: Date.now(),
		});
		if (!running) safeProcessNext();
	},
	enqueueMany(credentialIds: string[]): void {
		for (const credentialId of credentialIds) this.enqueue(credentialId);
	},
	getSnapshot,
	clearCompleted(): void {
		for (let i = items.length - 1; i >= 0; i--) {
			if (items[i].status === "done" || items[i].status === "failed") items.splice(i, 1);
		}
	},
};
