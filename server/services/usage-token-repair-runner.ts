import { randomUUID } from "node:crypto";
import { setTimeout as delay } from "node:timers/promises";
import { resolveRepairEvidence } from "@server/lib/usage-token-repair-evidence";
import {
	TOKEN_REPAIR_MAX_DUMP_BYTES,
	type TokenRepairEvidence,
	type TokenRepairJob,
	type TokenRepairProtocolOverride,
	tokenRepairProtocolFor,
} from "@shared/usage-token-repair";
import { createRepairEvidencePool } from "./usage-token-repair-evidence-client";
import {
	addTotals,
	emptySummary,
	fingerprint,
	repairCounterKeys,
	retireRepairScratch,
	TokenRepairScratch,
	validModelIdentity,
} from "./usage-token-repair-scratch";
import type { RepairRequestRow, TokenRepairStore } from "./usage-token-repair-store";

type Analyzer = (
	row: RepairRequestRow,
	raw: string | null,
	directory: string,
	signal: AbortSignal,
	hint?: TokenRepairProtocolOverride["protocol"],
) => Promise<TokenRepairEvidence>;
export class TokenRepairRunner {
	private active: { id: string; controller: AbortController; done: Promise<void> } | null = null;
	private preparing = false;
	constructor(
		readonly store: TokenRepairStore,
		private directory: string,
		private analyze?: Analyzer,
	) {}
	private async prepare<T>(fn: () => Promise<T>): Promise<T> {
		if (this.preparing) throw new Error("A token repair operation is already running");
		this.preparing = true;
		try {
			if (this.active) {
				const current = await this.store.getJob(this.active.id);
				if (!current || !["ready", "completed"].includes(current.status))
					throw new Error("A token repair operation is already running");
				await this.active.done;
			}
			return await fn();
		} finally {
			this.preparing = false;
		}
	}
	private launch(job: TokenRepairJob, work: (signal: AbortSignal) => Promise<void>) {
		const controller = new AbortController();
		const timer = setTimeout(
			() => controller.abort(new Error("Maintenance exceeded two hours")),
			2 * 60 * 60 * 1000,
		);
		const done = Promise.resolve()
			.then(() => work(controller.signal))
			.catch(async () => {
				await this.store.bulkFail(job.id);
			})
			.finally(() => {
				clearTimeout(timer);
				if (this.active?.controller === controller) this.active = null;
			});
		void done.catch(() => {});
		this.active = { id: job.id, controller, done };
	}
	async start(
		userId: string,
		_includeReviewed: boolean,
		_overrides?: TokenRepairProtocolOverride[],
		sourceJobId?: string,
	): Promise<TokenRepairJob> {
		return this.prepare(async () => {
			if (sourceJobId) throw new Error("Bulk scans cannot use legacy candidate scopes");
			const retired = retireRepairScratch(this.directory, await this.store.retiredJobs());
			await this.store.markScratchRetired(retired);
			const job = await this.store.createBulkJob(userId);
			this.launch(job, (s) => this.scan(job, s));
			return job;
		});
	}
	private pool() {
		const analyze = this.analyze;
		return analyze
			? {
					analyze: (
						row: RepairRequestRow,
						raw: string | null,
						signal: AbortSignal,
						hint?: TokenRepairProtocolOverride["protocol"],
					) => analyze(row, raw, this.directory, signal, hint),
					close: async () => {},
				}
			: createRepairEvidencePool(this.directory, 2);
	}
	private async scan(job: TokenRepairJob, signal: AbortSignal) {
		const scratch = new TokenRepairScratch(this.directory, job.id, true),
			pool = this.pool(),
			started = Date.now();
		let scanned = 0,
			cursor: RepairRequestRow | undefined,
			last = 0;
		const report = async (count: number) => {
			if (Date.now() - last < 1000) return;
			await this.store.bulkState(
				job.id,
				"scanning",
				"analyzing",
				null,
				count,
				(count * 1000) / Math.max(1, Date.now() - started),
			);
			last = Date.now();
		};
		try {
			while (true) {
				signal.throwIfAborted();
				const rows = await this.store.bulkPage(job, cursor);
				if (!rows.length) break;
				for (let offset = 0; offset < rows.length; offset += 100) {
					const chunk = rows.slice(offset, offset + 100);
					const dumps = await this.store.getRawDumps(chunk.map((r) => r.id));
					const pending: RepairRequestRow[] = [];
					const staged: { row: RepairRequestRow; evidence: TokenRepairEvidence; hash: string }[] =
						[];
					const analyze = async (row: RepairRequestRow, raw: string | null) => {
						signal.throwIfAborted();
						const evidence: TokenRepairEvidence =
							row.tokenAccountingVersion !== null
								? {
										classification: "correct",
										protocol: "unknown",
										reason: "already_normalized",
										evidence: "",
										before: row,
										after: null,
									}
								: raw === null
									? {
											classification: "insufficient",
											protocol: "unknown",
											reason: "dump_missing",
											evidence: "",
											before: row,
											after: null,
										}
									: await pool.analyze(row, raw, signal);
						return { row, evidence, hash: fingerprint(raw) };
					};
					const included = chunk.filter((r) => {
						if (dumps.has(r.id)) return true;
						pending.push(r);
						return false;
					});
					for (let i = 0; i < included.length; i += 2) {
						staged.push(
							...(await Promise.all(
								included.slice(i, i + 2).map((r) => analyze(r, dumps.get(r.id) ?? null)),
							)),
						);
						await report(scanned + staged.length);
					}
					dumps.clear();
					// Only unusually large rows excluded by the SQL batch byte budget need a reread.
					for (const row of pending) {
						staged.push(
							await analyze(row, await this.store.getRawDump(row.id, TOKEN_REPAIR_MAX_DUMP_BYTES)),
						);
						await report(scanned + staged.length);
					}
					staged.sort((a, b) => chunk.indexOf(a.row) - chunk.indexOf(b.row));
					signal.throwIfAborted();
					scratch.append(staged);
					scanned += staged.length;
				}
				cursor = rows[rows.length - 1];
				await report(scanned);
			}
			signal.throwIfAborted();
			const summary = emptySummary();
			summary.requestCount = scanned;
			summary.modelCount = scratch.modelCount();
			await this.store.bulkState(
				job.id,
				"models",
				"ready",
				summary,
				scanned,
				(scanned * 1000) / Math.max(1, Date.now() - started),
			);
		} finally {
			await pool.close();
			scratch.close();
		}
	}
	async models(id: string, cursor?: string) {
		const job = await this.store.getJob(id);
		if (job?.workflowVersion !== 2 || !["models", "preview"].includes(job.phase ?? ""))
			throw new Error("Models are available only after a complete scan");
		const scratch = new TokenRepairScratch(this.directory, id);
		try {
			return scratch.models(cursor, job.protocolOverrides);
		} finally {
			scratch.close();
		}
	}
	async dryRun(id: string, overrides: TokenRepairProtocolOverride[]): Promise<TokenRepairJob> {
		return this.prepare(async () => {
			const job = await this.store.getJob(id);
			if (
				job?.workflowVersion !== 2 ||
				job.status !== "ready" ||
				!["models", "preview"].includes(job.phase ?? "")
			)
				throw new Error("Scan must finish before dry-run");
			const next = await this.store.bulkClaim(id, "dry-running", overrides);
			if (!next) throw new Error("Missing job");
			this.launch(next, (s) => this.preview(next, s));
			return next;
		});
	}
	private async preview(job: TokenRepairJob, signal: AbortSignal) {
		const scratch = new TokenRepairScratch(this.directory, job.id);
		const summary = emptySummary();
		summary.modelCount = scratch.modelCount();
		let cursor = 0;
		try {
			while (true) {
				signal.throwIfAborted();
				const page = scratch.page(cursor);
				if (!page.length) break;
				scratch.db.transaction(() => {
					for (const entry of page) {
						const evidence = resolveRepairEvidence(
							entry.row,
							entry.evidence,
							entry.evidence.reason === "needs_protocol"
								? tokenRepairProtocolFor(job.protocolOverrides, entry.row.provider, entry.row.model)
								: undefined,
						);
						scratch.plan(entry.seq, evidence);
						summary.requestCount++;
						addTotals(summary.before, entry.row);
						addTotals(
							summary.after,
							evidence.classification === "repairable" && evidence.after
								? evidence.after
								: entry.row,
						);
						if (evidence.classification === "repairable" && evidence.after) {
							summary.repairable++;
							addTotals(summary.repairBefore, entry.row);
							addTotals(summary.repairAfter, evidence.after);
						} else if (evidence.classification === "correct") summary.correct++;
						else if (evidence.reason === "needs_protocol" && validModelIdentity(entry.row))
							summary.unresolved++;
						else if (evidence.classification === "conflict") summary.conflicts++;
						else summary.skipped++;
						if (evidence.classification !== "repairable" && evidence.classification !== "correct") {
							const reason = /^[a-z_]{1,64}$/.test(evidence.reason)
								? evidence.reason
								: "evidence_invalid";
							summary.reasons[reason] = (summary.reasons[reason] ?? 0) + 1;
						}
					}
				})();
				const tail = page.at(-1);
				if (!tail) throw new Error("Missing scratch cursor");
				cursor = tail.seq;
				await delay(0, undefined, { signal });
			}
			addTotals(summary.delta, summary.after);
			addTotals(summary.delta, summary.before, -1);
			summary.previewVersion = randomUUID();
			signal.throwIfAborted();
			await this.store.bulkState(job.id, "preview", "ready", summary, job.scanned);
		} finally {
			scratch.close();
		}
	}
	async applyPreview(id: string, previewVersion: string, userId: string): Promise<TokenRepairJob> {
		return this.prepare(async () => {
			const job = await this.store.getJob(id);
			if (
				job?.workflowVersion !== 2 ||
				job.status !== "ready" ||
				job.phase !== "preview" ||
				!previewVersion ||
				job.summary?.previewVersion !== previewVersion
			)
				throw new Error("Stale or incomplete preview");
			const next = await this.store.bulkClaim(id, "applying", undefined, previewVersion);
			if (!next) throw new Error("Missing job");
			this.launch(next, (s) => this.applyAll(next, userId, s));
			return next;
		});
	}
	private async applyAll(job: TokenRepairJob, userId: string, signal: AbortSignal) {
		const scratch = new TokenRepairScratch(this.directory, job.id),
			pool = this.pool();
		if (!job.summary) throw new Error("Missing immutable summary");
		const summary = structuredClone(job.summary);
		this.store.resetWriteMetrics();
		let cursor = 0,
			batchSize = 50;
		try {
			while (true) {
				signal.throwIfAborted();
				const page = scratch.page(cursor, true);
				if (!page.length) break;
				for (let i = 0; i < page.length; ) {
					signal.throwIfAborted();
					const candidates = page.slice(i, i + batchSize);
					const batch: typeof candidates = [];
					let rawBytes = 0;
					const validated: {
						row: RepairRequestRow;
						evidence: TokenRepairEvidence;
						valid: boolean;
						expectedRaw: string | null;
					}[] = [];
					for (const e of candidates) {
						const planned = scratch.planned(e.seq);
						const raw = await this.store.getRawDump(e.row.id, TOKEN_REPAIR_MAX_DUMP_BYTES);
						const bytes = Buffer.byteLength(raw ?? "");
						if (batch.length && rawBytes + bytes > TOKEN_REPAIR_MAX_DUMP_BYTES) break;
						rawBytes += bytes;
						batch.push(e);
						const current = await pool.analyze(e.row, raw, signal, planned.manualProtocol);
						validated.push({
							row: e.row,
							evidence: planned,
							expectedRaw: raw,
							valid:
								fingerprint(raw) === e.hash &&
								current.classification === "repairable" &&
								repairCounterKeys.every((k) => current.after?.[k] === planned.after?.[k]),
						});
					}
					signal.throwIfAborted();
					let outcomes: Awaited<ReturnType<TokenRepairStore["applyBulk"]>> | undefined;
					const started = Date.now();
					for (let attempt = 0; attempt < 4; attempt++) {
						try {
							outcomes = await this.store.applyBulk(job.id, validated, userId);
							break;
						} catch (error) {
							if (
								attempt === 3 ||
								!/busy|locked|lock timeout|deadlock|serialization/i.test(String(error))
							)
								throw error;
							await delay(25 * 2 ** attempt, undefined, { signal });
						}
					}
					if (!outcomes) throw new Error("No apply result");
					for (const [n, e] of batch.entries()) {
						const outcome = outcomes[n];
						const item = validated[n];
						if (!outcome || !item) throw new Error("Incomplete committed batch");
						scratch.outcome(e.seq, outcome);
						if (outcome === "applied" && item.evidence.after) {
							addTotals(summary.appliedDelta, item.evidence.after);
							addTotals(summary.appliedDelta, e.row, -1);
						} else if (outcome === "conflict") summary.conflicts++;
					}
					i += batch.length;
					if (Date.now() - started > 30) batchSize = Math.max(5, Math.floor(batchSize / 2));
					summary.writeTransactions = this.store.writeMetrics();
					await this.store.bulkState(job.id, "applying", "applying", summary, job.scanned);
					await delay(0, undefined, { signal });
				}
				const tail = page.at(-1);
				if (!tail) throw new Error("Missing scratch cursor");
				cursor = tail.seq;
			}
			signal.throwIfAborted();
			summary.writeTransactions = this.store.writeMetrics();
			await this.store.bulkState(job.id, "done", "completed", summary, job.scanned);
		} finally {
			await pool.close();
			scratch.close();
		}
	}
	async apply(_id: string, _ids: string[], _userId: string): Promise<never> {
		throw new Error("Per-request apply is disabled; confirm an immutable preview");
	}
	async cancel(id: string) {
		if (this.active?.id === id) {
			this.active.controller.abort();
			await this.store.cancel();
		}
		await this.store.cancelJob(id);
		if (this.active?.id === id) await this.active.done;
		return this.store.getJob(id);
	}
	async close() {
		if (this.active) await this.cancel(this.active.id);
		await this.store.close();
	}
}
