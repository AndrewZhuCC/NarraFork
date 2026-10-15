import {
	TOKEN_REPAIR_MAX_DUMP_BYTES,
	TOKEN_REPAIR_MAX_RECORDS,
	TOKEN_REPAIR_TIMEOUT_MS,
	type TokenRepairApplyResult,
	type TokenRepairEvidence,
	type TokenRepairJob,
	type TokenRepairProtocolOverride,
	tokenRepairProtocolFor,
} from "@shared/usage-token-repair";
import { analyzeRepairRecord } from "./usage-token-repair-evidence-client";
import type { RepairRequestRow, TokenRepairStore } from "./usage-token-repair-store";

type Analyzer = (
	row: RepairRequestRow,
	raw: string | null,
	directory: string,
	signal: AbortSignal,
	protocolHint?: TokenRepairProtocolOverride["protocol"],
) => Promise<TokenRepairEvidence>;
const counterKeys = [
	"inputTokens",
	"cachedInputTokens",
	"cacheCreationInputTokens",
	"cacheCreation5mTokens",
	"cacheCreation1hTokens",
] as const;

/** One manual deployment-level job, bounded pages and per-record cancellable evidence isolates. */
export class TokenRepairRunner {
	private active: { id: string; controller: AbortController; done: Promise<unknown> } | null = null;
	private preparing: Promise<void> | null = null;
	private async prepare<T>(fn: () => Promise<T>): Promise<T> {
		if (this.active || this.preparing)
			throw new Error("A token repair operation is already running");
		let release = () => {};
		this.preparing = new Promise<void>((resolve) => {
			release = resolve;
		});
		try {
			return await fn();
		} finally {
			this.preparing = null;
			release();
		}
	}
	constructor(
		readonly store: TokenRepairStore,
		private directory: string,
		private analyze: Analyzer = analyzeRepairRecord,
	) {}
	async start(
		userId: string,
		includeReviewed: boolean,
		protocolOverrides?: TokenRepairProtocolOverride[],
		sourceJobId?: string,
	): Promise<TokenRepairJob> {
		return this.prepare(async () => {
			const job = await this.store.createJob(
				userId,
				includeReviewed,
				protocolOverrides,
				sourceJobId,
			);
			this.launch(job);
			return job;
		});
	}
	private launch(job: TokenRepairJob): void {
		const controller = new AbortController();
		const done = Promise.resolve()
			.then(() => this.scan(job, controller.signal))
			.catch(async () => {
				if (!controller.signal.aborted)
					await this.store.setJobStatus(
						job.id,
						"failed",
						"Analysis stopped. End this review and start a new analysis.",
					);
			})
			.finally(() => {
				if (this.active?.controller === controller) this.active = null;
			});
		// A database failure must not become an unhandled rejection in a detached task.
		void done.catch(() => {});
		this.active = { id: job.id, controller, done };
	}
	private async scan(job: TokenRepairJob, signal: AbortSignal): Promise<void> {
		const deadline = Date.now() + TOKEN_REPAIR_TIMEOUT_MS;
		let scanned = job.scanned;
		while (scanned < TOKEN_REPAIR_MAX_RECORDS) {
			signal.throwIfAborted();
			if (Date.now() >= deadline) throw new Error("Analysis exceeded the thirty-minute limit");
			const rows = await this.store.scanPage(job.id);
			if (!rows.length) break;
			for (const row of rows) {
				signal.throwIfAborted();
				if (Date.now() >= deadline) throw new Error("Analysis exceeded the thirty-minute limit");
				const raw = await this.store.getRawDump(row.id, TOKEN_REPAIR_MAX_DUMP_BYTES);
				const evidence = await this.analyze(
					row,
					raw,
					this.directory,
					signal,
					tokenRepairProtocolFor(job.protocolOverrides, row.provider, row.model),
				);
				signal.throwIfAborted();
				await this.store.saveCandidate(job.id, row, evidence);
				if (++scanned >= TOKEN_REPAIR_MAX_RECORDS) break;
			}
		}
		signal.throwIfAborted();
		await this.store.finishAnalysis(job.id);
	}
	async cancel(id: string): Promise<TokenRepairJob | null> {
		// Cancellation must also cover the short async validation/startup window.
		await this.preparing;
		if (!(await this.store.getJob(id))) return null;
		// Explicit administrator action is distinct from an old worker's failure handler.
		await this.store.cancelJob(id);
		if (this.active?.id === id) {
			const active = this.active;
			active.controller.abort();
			await this.store.cancel();
			await active.done.catch(() => {});
		}
		return this.store.getJob(id);
	}
	async apply(id: string, ids: string[], userId: string): Promise<TokenRepairApplyResult> {
		const operation = await this.prepare(async () => {
			if (!ids.length || ids.length > 50 || new Set(ids).size !== ids.length)
				throw new Error("Select between one and fifty distinct candidates");
			const job = await this.store.getJob(id);
			if (!job || job.status !== "ready")
				throw new Error("Analysis must be ready before applying repairs");
			await this.store.setJobStatus(id, "applying");
			const controller = new AbortController();
			const result: TokenRepairApplyResult = { applied: 0, conflicts: 0 };
			const done = (async () => {
				for (const candidateId of ids) {
					controller.signal.throwIfAborted();
					const candidate = await this.store.getCandidate(id, candidateId);
					if (
						!candidate ||
						candidate.classification !== "repairable" ||
						candidate.appliedAt ||
						!candidate.after
					) {
						result.conflicts++;
						continue;
					}
					const row = await this.store.getRequest(candidate.requestId);
					if (!row || !counterKeys.every((key) => row[key] === candidate.before[key])) {
						await this.store.markCandidateConflict(
							id,
							candidateId,
							"Stored counters changed after analysis, or the request was deleted.",
						);
						result.conflicts++;
						continue;
					}
					const evidence = await this.analyze(
						row,
						await this.store.getRawDump(row.id, TOKEN_REPAIR_MAX_DUMP_BYTES),
						this.directory,
						controller.signal,
						tokenRepairProtocolFor(job.protocolOverrides, row.provider, row.model),
					);
					if (
						evidence.classification !== "repairable" ||
						!evidence.after ||
						!counterKeys.every((key) => evidence.after?.[key] === candidate.after?.[key])
					) {
						await this.store.markCandidateConflict(
							id,
							candidateId,
							"Usage evidence disappeared or changed after the preview; no repair was applied.",
						);
						result.conflicts++;
						continue;
					}
					controller.signal.throwIfAborted();
					const outcome = await this.store.applyCandidate(id, candidateId, userId);
					if (outcome === "conflict") result.conflicts++;
					else result.applied++;
				}
				await this.store.setJobStatus(id, "ready");
				return result;
			})()
				.catch(async (error: unknown) => {
					if (!controller.signal.aborted)
						await this.store.setJobStatus(
							id,
							"failed",
							"Repair stopped. Inspect committed results, then start a new analysis.",
						);
					throw error;
				})
				.finally(() => {
					if (this.active?.controller === controller) this.active = null;
				});
			this.active = { id, controller, done };
			return { done };
		});
		return operation.done;
	}
	async close(): Promise<void> {
		await this.preparing;
		if (this.active) await this.cancel(this.active.id);
		await this.store.close();
	}
}
