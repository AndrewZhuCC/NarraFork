/**
 * Per-category storage scan jobs.
 *
 * An admin can scan every category from the summary tab, or one category from its
 * own tab. Each category has its own slot and abort controller, and running
 * categories proceed together — nothing waits for the database step to finish
 * before a directory walk starts.
 *
 * Dedupe is per category. Starting a category that is already running returns the
 * current state instead of stacking a second walk on the worker pool.
 */

import {
	STORAGE_CATEGORY_KEYS,
	STORAGE_CATEGORY_PROGRESS_MESSAGE,
	type StorageCategoryKey,
} from "@shared/storage-categories";
import { logger } from "../lib/logger";
import {
	rememberStorageCategory,
	type StorageCategoryResult,
	StorageScanAbortedError,
	type StorageScanResult,
	scanStorageCategory,
} from "./storage-service";

export type StorageScanJobStatus = "idle" | "running" | "complete" | "error" | "cancelled";

export interface StorageCategoryJobState {
	key: StorageCategoryKey;
	status: StorageScanJobStatus;
	progressMessage: string | null;
	progressDetail: { done: number; total: number | null } | null;
	result: StorageCategoryResult | null;
	error: string | null;
	startedAt: number | null;
	finishedAt: number | null;
}

export interface StorageScanJobState {
	/** `running` while any category is still running. */
	status: StorageScanJobStatus;
	progressMessage: string | null;
	progressDetail: { done: number; total: number | null } | null;
	/** Category results gathered so far, including ones kept from an earlier scan. */
	categories: StorageCategoryResult[];
	categoryJobs: StorageCategoryJobState[];
	result: StorageScanResult | null;
	error: string | null;
	startedAt: number | null;
	finishedAt: number | null;
}

export interface StorageScanStartInput {
	/** Omit to scan every category. */
	categories?: StorageCategoryKey[];
}

const idleCategory = (key: StorageCategoryKey): StorageCategoryJobState => ({
	key,
	status: "idle",
	progressMessage: null,
	progressDetail: null,
	result: null,
	error: null,
	startedAt: null,
	finishedAt: null,
});

const jobs = new Map<StorageCategoryKey, StorageCategoryJobState>(
	STORAGE_CATEGORY_KEYS.map((key) => [key, idleCategory(key)]),
);
const abortControllers = new Map<StorageCategoryKey, AbortController>();

function selectedKeys(categories?: StorageCategoryKey[]): StorageCategoryKey[] {
	if (!categories || categories.length === 0) return [...STORAGE_CATEGORY_KEYS];
	const seen = new Set<StorageCategoryKey>();
	const keys: StorageCategoryKey[] = [];
	for (const key of categories) {
		if (!STORAGE_CATEGORY_KEYS.includes(key) || seen.has(key)) continue;
		seen.add(key);
		keys.push(key);
	}
	return keys;
}

function deriveStatus(list: StorageCategoryJobState[]): StorageScanJobStatus {
	if (list.some((job) => job.status === "running")) return "running";
	if (list.some((job) => job.status === "error")) return "error";
	if (list.some((job) => job.status === "complete")) return "complete";
	if (list.some((job) => job.status === "cancelled")) return "cancelled";
	return "idle";
}

function snapshot(): StorageScanJobState {
	const categoryJobs = STORAGE_CATEGORY_KEYS.map((key) => jobs.get(key) ?? idleCategory(key));
	const categories = categoryJobs.flatMap((job) => (job.result ? [job.result] : []));
	const status = deriveStatus(categoryJobs);
	const running = categoryJobs.filter((job) => job.status === "running");
	const errors = categoryJobs.flatMap((job) => (job.error ? [job.error] : []));
	const startedAt = categoryJobs.reduce<number | null>((earliest, job) => {
		if (job.startedAt == null) return earliest;
		return earliest == null ? job.startedAt : Math.min(earliest, job.startedAt);
	}, null);
	const finishedAt =
		status === "running"
			? null
			: categoryJobs.reduce<number | null>((latest, job) => {
					if (job.finishedAt == null) return latest;
					return latest == null ? job.finishedAt : Math.max(latest, job.finishedAt);
				}, null);
	const single = running.length === 1 ? running[0] : null;
	const result: StorageScanResult | null =
		categories.length === 0
			? null
			: {
					categories,
					totalBytes: categories
						.filter((category) => !category.unavailable)
						.reduce((sum, category) => sum + category.sizeBytes, 0),
					...(categories.some((category) => category.truncated || category.unavailable)
						? { truncated: true }
						: {}),
					scannedAt: finishedAt ?? Date.now(),
				};
	return {
		status,
		progressMessage: single?.progressMessage ?? null,
		progressDetail: single?.progressDetail ?? null,
		categories,
		categoryJobs,
		result,
		error: errors.length > 0 ? errors.join("\n") : null,
		startedAt,
		finishedAt,
	};
}

export function getStorageScanJob(): StorageScanJobState {
	return snapshot();
}

/**
 * Start the requested categories, or every category when none are named.
 *
 * `started` is false only when every requested category was already running and
 * had not been aborted. A category whose abort was requested does not hold its
 * slot: Cancel then Scan starts a new run immediately.
 */
export function startStorageScan(input: StorageScanStartInput = {}): {
	started: boolean;
	state: StorageScanJobState;
} {
	let started = false;
	for (const key of selectedKeys(input.categories)) {
		const current = jobs.get(key) ?? idleCategory(key);
		const controller = abortControllers.get(key);
		if (current.status === "running" && controller && !controller.signal.aborted) continue;

		const next = new AbortController();
		abortControllers.set(key, next);
		jobs.set(key, {
			...current,
			status: "running",
			progressMessage: STORAGE_CATEGORY_PROGRESS_MESSAGE[key],
			progressDetail: null,
			error: null,
			startedAt: Date.now(),
			finishedAt: null,
		});
		started = true;
		void runCategory(key, next);
	}
	return { started, state: snapshot() };
}

async function runCategory(key: StorageCategoryKey, controller: AbortController): Promise<void> {
	/** The controller identity is the slot's owner token. A replacement run invalidates it. */
	const ownsSlot = () => abortControllers.get(key) === controller;
	try {
		const result = await scanStorageCategory(key, {
			signal: controller.signal,
			onProgress: (detail) => {
				if (!ownsSlot()) return;
				const current = jobs.get(key);
				if (!current || current.status !== "running") return;
				jobs.set(key, {
					...current,
					progressMessage: STORAGE_CATEGORY_PROGRESS_MESSAGE[key],
					progressDetail: detail,
				});
			},
		});
		if (!ownsSlot()) return;
		rememberStorageCategory(result);
		const current = jobs.get(key) ?? idleCategory(key);
		jobs.set(key, {
			...current,
			status: "complete",
			result,
			progressMessage: null,
			progressDetail: null,
			error: null,
			finishedAt: Date.now(),
		});
	} catch (error) {
		const aborted = controller.signal.aborted || error instanceof StorageScanAbortedError;
		if (!aborted) {
			logger.error("Background storage category scan failed", {
				category: key,
				error: error instanceof Error ? error.message : String(error),
			});
		}
		if (!ownsSlot()) return;
		const current = jobs.get(key) ?? idleCategory(key);
		jobs.set(key, {
			...current,
			status: aborted ? "cancelled" : "error",
			error: aborted ? null : error instanceof Error ? error.message : String(error),
			progressMessage: null,
			progressDetail: null,
			finishedAt: Date.now(),
		});
	} finally {
		if (abortControllers.get(key) === controller) abortControllers.delete(key);
	}
}

/**
 * How long `cancelStorageScan` waits for the abort to actually land.
 *
 * Short, because it blocks the response. A category blocked inside one synchronous
 * query can outlive this wait; the caller still gets the latest state, and a
 * follow-up scan is not deduped onto an abort that was already requested.
 */
const CANCEL_SETTLE_TIMEOUT_MS = 1500;
const CANCEL_SETTLE_POLL_MS = 25;

export async function cancelStorageScan(
	input: StorageScanStartInput = {},
): Promise<StorageScanJobState> {
	const keys = selectedKeys(input.categories);
	let abortedAny = false;
	for (const key of keys) {
		const job = jobs.get(key);
		const controller = abortControllers.get(key);
		if (job?.status !== "running" || !controller || controller.signal.aborted) continue;
		controller.abort();
		abortedAny = true;
	}
	if (!abortedAny) return snapshot();
	const deadline = Date.now() + CANCEL_SETTLE_TIMEOUT_MS;
	while (Date.now() < deadline) {
		const stillRunning = keys.some((key) => {
			const job = jobs.get(key);
			const controller = abortControllers.get(key);
			return job?.status === "running" && controller && !controller.signal.aborted;
		});
		// Also wait while a controller is aborted but the job has not published cancelled yet.
		const settling = keys.some((key) => jobs.get(key)?.status === "running");
		if (!stillRunning && !settling) break;
		await new Promise((resolve) => setTimeout(resolve, CANCEL_SETTLE_POLL_MS));
	}
	return snapshot();
}
