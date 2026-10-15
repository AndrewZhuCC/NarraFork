import { Database, type SQLQueryBindings } from "bun:sqlite";
import { generateId } from "@server/lib/id";
import {
	TOKEN_ACCOUNTING_VERSION,
	TOKEN_REPAIR_MAX_DUMP_BYTES,
	TOKEN_REPAIR_MAX_PROTOCOL_OVERRIDES,
	TOKEN_REPAIR_MAX_RECORDS,
	TOKEN_REPAIR_PAGE_SIZE,
	TOKEN_REPAIR_PROTOCOL_CONFIG_MAX_BYTES,
	type TokenRepairCandidate,
	type TokenRepairCandidatePage,
	type TokenRepairCounters,
	type TokenRepairEvidence,
	type TokenRepairJob,
	type TokenRepairJobStatus,
	type TokenRepairProbe,
	type TokenRepairProtocolOverride,
	tokenRepairProtocolFor,
} from "@shared/usage-token-repair";
import { SQL } from "bun";

export interface RepairRequestRow extends TokenRepairCounters {
	id: string;
	createdAt: string;
	provider: string | null;
	model: string | null;
	userId: string | null;
	credentialId: string | null;
	messageId: string | null;
	tokenAccountingVersion: number | null;
	tokenAccountingReviewVersion: number | null;
}
export interface TokenRepairStoreConfig {
	backend: "sqlite" | "postgres";
	sqlitePath?: string;
	postgresUrl?: string;
}

type Row = Record<string, unknown>;
type Query = (sql: string, args?: unknown[]) => Promise<Row[]>;
const counterColumns = [
	"input_tokens",
	"cached_input_tokens",
	"cache_creation_input_tokens",
	"cache_creation_5m_tokens",
	"cache_creation_1h_tokens",
] as const;
const jobColumns = `id, status, created_by, created_at, updated_at, scanned, repaired, conflicts,
 upper_created_at, upper_id, cursor_created_at, cursor_id, include_reviewed,
 protocol_overrides_json, source_job_id, error`;
const counterKeys = [
	"inputTokens",
	"cachedInputTokens",
	"cacheCreationInputTokens",
	"cacheCreation5mTokens",
	"cacheCreation1hTokens",
] as const;
const requestColumns = `id, created_at, provider, model, user_id, credential_id, message_id,
 token_accounting_version, token_accounting_review_version, ${counterColumns.join(", ")}`;
const cachePredicate =
	"(cached_input_tokens > 0 OR cache_creation_input_tokens > 0 OR cache_creation_5m_tokens > 0 OR cache_creation_1h_tokens > 0)";
const activePredicate = "status IN ('analyzing', 'ready', 'applying')";
// Drive scoped scans from the bounded source candidate index, not the request table.
const sourceFrom = `(SELECT request_id FROM usage_token_repair_candidates WHERE job_id = ? ORDER BY request_id LIMIT ${TOKEN_REPAIR_MAX_RECORDS}) source CROSS JOIN api_requests a`;
const sourceRequestColumns = requestColumns
	.split(",")
	.map((column) => `a.${column.trim()}`)
	.join(", ");
const sourceCachePredicate = `(${counterColumns
	.slice(1)
	.map((column) => `a.${column} > 0`)
	.join(" OR ")})`;
const now = () => new Date().toISOString();
const newId = generateId;
function camel(row: Row): Row {
	return Object.fromEntries(
		Object.entries(row).map(([key, value]) => [
			key.replace(/_([a-z0-9])/g, (_, char: string) => char.toUpperCase()),
			value,
		]),
	);
}
function normalizeOverrides(value: unknown): TokenRepairProtocolOverride[] {
	if (!Array.isArray(value) || value.length > TOKEN_REPAIR_MAX_PROTOCOL_OVERRIDES)
		throw new Error("Invalid token repair protocol configuration");
	const identities = new Set<string>();
	const entries = Array.from(value, (item: unknown): TokenRepairProtocolOverride => {
		if (!item || typeof item !== "object")
			throw new Error("Invalid token repair protocol override");
		const entry = item as Record<string, unknown>;
		if (
			!(
				entry.provider === null ||
				(typeof entry.provider === "string" && entry.provider.length <= 128)
			) ||
			typeof entry.model !== "string" ||
			entry.model.length === 0 ||
			entry.model.length > 256 ||
			typeof entry.protocol !== "string" ||
			!["openai", "anthropic", "gemini"].includes(entry.protocol)
		)
			throw new Error("Invalid token repair protocol override");
		const identity = JSON.stringify([entry.provider, entry.model]);
		if (identities.has(identity)) throw new Error("Duplicate token repair protocol identity");
		identities.add(identity);
		return {
			provider: entry.provider as string | null,
			model: entry.model,
			protocol: entry.protocol as TokenRepairProtocolOverride["protocol"],
		};
	});
	entries.sort((a, b) => {
		const left = JSON.stringify([a.provider, a.model]);
		const right = JSON.stringify([b.provider, b.model]);
		return left < right ? -1 : left > right ? 1 : 0;
	});
	boundedJson(entries, TOKEN_REPAIR_PROTOCOL_CONFIG_MAX_BYTES);
	return entries;
}
function protocolMatches(
	job: TokenRepairJob,
	row: Pick<RepairRequestRow, "provider" | "model">,
	evidence: TokenRepairEvidence,
): boolean {
	return (
		evidence.manualProtocol ===
		tokenRepairProtocolFor(job.protocolOverrides, row.provider, row.model)
	);
}
function jobFrom(row: Row | undefined): TokenRepairJob | null {
	if (!row) return null;
	const result = camel(row);
	result.includeReviewed = Boolean(result.includeReviewed);
	const protocolJson = result.protocolOverridesJson ?? "[]";
	if (
		typeof protocolJson !== "string" ||
		Buffer.byteLength(protocolJson) > TOKEN_REPAIR_PROTOCOL_CONFIG_MAX_BYTES
	)
		throw new Error("Invalid persisted token repair protocol configuration");
	result.protocolOverrides = normalizeOverrides(JSON.parse(protocolJson));
	delete result.protocolOverridesJson;
	return result as unknown as TokenRepairJob;
}
function requestFrom(row: Row | undefined): RepairRequestRow | null {
	return row ? (camel(row) as unknown as RepairRequestRow) : null;
}
function candidateFrom(row: Row): TokenRepairCandidate {
	const result = camel(row);
	const evidence = JSON.parse(String(result.evidenceJson)) as TokenRepairEvidence;
	delete result.evidenceJson;
	delete result.snapshotJson;
	return { ...result, ...evidence } as unknown as TokenRepairCandidate;
}
function counters(value: TokenRepairCounters): number[] {
	return counterKeys.map((key) => value[key]);
}
function validCounters(value: TokenRepairCounters): boolean {
	return counters(value).every((n) => Number.isSafeInteger(n) && n >= 0);
}
function snapshot(row: RepairRequestRow): RepairRequestRow {
	return Object.fromEntries(
		[
			"id",
			"createdAt",
			"provider",
			"model",
			"userId",
			"credentialId",
			"messageId",
			"tokenAccountingVersion",
			"tokenAccountingReviewVersion",
			...counterKeys,
		].map((key) => [key, row[key as keyof RepairRequestRow]]),
	) as unknown as RepairRequestRow;
}
function sameRequest(a: RepairRequestRow, b: RepairRequestRow): boolean {
	return (
		counters(a).every((n, index) => n === counters(b)[index]) &&
		["userId", "credentialId", "provider", "model", "messageId", "createdAt"].every(
			(key) => a[key as keyof RepairRequestRow] === b[key as keyof RepairRequestRow],
		)
	);
}
function boundedJson(value: unknown, max: number): string {
	const result = JSON.stringify(value);
	if (Buffer.byteLength(result) > max) throw new Error("Token repair audit exceeds byte limit");
	return result;
}
function required<T>(value: T | null | undefined): T {
	if (value == null) throw new Error("Token repair store invariant violated");
	return value;
}

/** Standalone worker connection. Deliberately never imports application db/settings. */
export function createTokenRepairStore(config: TokenRepairStoreConfig) {
	let sqlite: Database | undefined;
	let pg: SQL | undefined;
	let closed = false;
	let closing = false;
	let closePromise: Promise<void> | undefined;
	const running = new Set<{ cancel(): unknown }>();
	if (config.backend === "sqlite") {
		if (!config.sqlitePath) throw new Error("Explicit SQLite path required");
		sqlite = new Database(config.sqlitePath, { readwrite: true, create: false, strict: true });
		sqlite.exec("PRAGMA busy_timeout = 100; PRAGMA foreign_keys = ON");
	} else {
		if (!config.postgresUrl) throw new Error("Explicit PostgreSQL URL required");
		pg = new SQL(config.postgresUrl, {
			max: 1,
			connectionTimeout: 5,
			idleTimeout: 10,
			connection: {
				statement_timeout: 5000,
				lock_timeout: 100,
				idle_in_transaction_session_timeout: 5000,
			},
		});
	}
	function queryFor(client: SQL): Query {
		return async (text, args = []) => {
			let index = 0;
			const query = client.unsafe(
				text.replace(/\?/g, () => `$${++index}`),
				args,
			);
			running.add(query);
			try {
				return Array.from(await query) as Row[];
			} finally {
				running.delete(query);
			}
		};
	}
	const query: Query = sqlite
		? async (text, args = []) =>
				required(sqlite)
					.query(text)
					.all(...(args as SQLQueryBindings[])) as Row[]
		: queryFor(required(pg));
	// Serialize a connection's operations, including reads, so SQLite async transaction
	// callbacks cannot accidentally interleave. The PG pool is independently max=1.
	let queue: Promise<unknown> = Promise.resolve();
	function serial<T>(fn: () => Promise<T>): Promise<T> {
		if (closed || closing) return Promise.reject(new Error("Token repair store closed"));
		const next = queue.then(() => {
			if (closed) throw new Error("Token repair store closed");
			return fn();
		});
		queue = next.catch(() => {});
		return next;
	}
	async function transaction<T>(fn: (q: Query) => Promise<T>): Promise<T> {
		if (pg) return (await pg.begin(async (tx) => await fn(queryFor(tx)))) as T;
		required(sqlite).exec("BEGIN IMMEDIATE");
		try {
			const result = await fn(query);
			required(sqlite).exec("COMMIT");
			return result;
		} catch (error) {
			required(sqlite).exec("ROLLBACK");
			throw error;
		}
	}
	const repairFrom = (includeReviewed: boolean) =>
		`api_requests${sqlite ? ` INDEXED BY idx_api_requests_token_repair_${includeReviewed ? "reviewed" : "pending"}` : ""}`;
	const getJob = async (id: string, q = query, lock = false) =>
		jobFrom(
			(
				await q(
					`SELECT ${jobColumns} FROM usage_token_repair_jobs WHERE id = ?${pg && lock ? " FOR UPDATE" : ""}`,
					[id],
				)
			)[0],
		);
	const latestJob = async () =>
		jobFrom(
			(
				await query(
					`SELECT ${jobColumns} FROM usage_token_repair_jobs ORDER BY created_at DESC, id DESC LIMIT 1`,
				)
			)[0],
		);
	const getRequest = async (id: string, q = query) =>
		requestFrom((await q(`SELECT ${requestColumns} FROM api_requests WHERE id = ?`, [id]))[0]);
	async function requireStatus(q: Query, jobId: string, status: "analyzing" | "applying") {
		// PostgreSQL locks the job row until commit; SQLite already holds BEGIN IMMEDIATE.
		const job = await getJob(jobId, q, true);
		if (!job || job.status !== status) throw new Error(`Job is not ${status}`);
		return job;
	}
	async function transition(
		q: Query,
		jobId: string,
		status: TokenRepairJobStatus,
		from: readonly string[],
		error?: string,
	) {
		const result = await q(
			`UPDATE usage_token_repair_jobs SET status = ?, error = ?, updated_at = ? WHERE id = ? AND status IN (${from.map(() => "?").join(", ")}) RETURNING id`,
			[status, error?.slice(0, 2048) ?? null, now(), jobId, ...from],
		);
		if (!result.length) throw new Error("Invalid token repair state transition");
	}
	async function inSource(q: Query, job: TokenRepairJob, requestId: string): Promise<boolean> {
		return (
			!job.sourceJobId ||
			(
				await q(
					"SELECT id FROM usage_token_repair_candidates WHERE job_id = ? AND request_id = ? LIMIT 1",
					[job.sourceJobId, requestId],
				)
			).length === 1
		);
	}
	async function candidateRow(jobId: string, id: string, q = query) {
		return (
			await q("SELECT * FROM usage_token_repair_candidates WHERE job_id = ? AND id = ?", [
				jobId,
				id,
			])
		)[0];
	}
	async function conflict(q: Query, row: Row, reason: string) {
		const evidence = JSON.parse(String(row.evidence_json)) as TokenRepairEvidence;
		if (evidence.classification === "conflict") return;
		evidence.classification = "conflict";
		evidence.reason = reason;
		await q("UPDATE usage_token_repair_candidates SET evidence_json = ? WHERE id = ?", [
			boundedJson(evidence, 16384),
			row.id,
		]);
		await q(
			"UPDATE usage_token_repair_jobs SET conflicts = conflicts + 1, updated_at = ? WHERE id = ?",
			[now(), row.job_id],
		);
	}

	return {
		probe: () =>
			serial<TokenRepairProbe>(async () => ({
				hasPending:
					(
						await query(
							`SELECT id FROM ${repairFrom(false)} WHERE token_accounting_version IS NULL AND token_accounting_review_version IS NULL AND ${cachePredicate} ORDER BY created_at, id LIMIT 1`,
						)
					).length > 0,
				job: await latestJob(),
			})),
		createJob: (
			userId: string,
			includeReviewed: boolean,
			protocolOverrides: TokenRepairProtocolOverride[] = [],
			sourceJobId?: string | null,
		) =>
			serial(() =>
				transaction(async (q) => {
					const protocolJson = boundedJson(
						normalizeOverrides(protocolOverrides),
						TOKEN_REPAIR_PROTOCOL_CONFIG_MAX_BYTES,
					);
					if (sourceJobId != null) {
						if (!includeReviewed)
							throw new Error("Source-scoped reanalysis requires includeReviewed");
						const source = (
							await q("SELECT id, status FROM usage_token_repair_jobs WHERE id = ?", [sourceJobId])
						)[0];
						if (
							!source ||
							!["completed", "cancelled", "failed", "interrupted"].includes(String(source.status))
						)
							throw new Error("Source job is missing or must be ended before reanalysis");
						const count = (
							await q(
								`SELECT COUNT(*) AS n FROM (SELECT id FROM usage_token_repair_candidates WHERE job_id = ? LIMIT ${TOKEN_REPAIR_MAX_RECORDS + 1}) bounded_source`,
								[sourceJobId],
							)
						)[0];
						if (Number(count?.n) > TOKEN_REPAIR_MAX_RECORDS)
							throw new Error("Source job exceeds candidate limit");
					}
					const active = (
						await q(`SELECT id FROM usage_token_repair_jobs WHERE ${activePredicate} LIMIT 1`)
					)[0];
					if (active) throw new Error("A token repair job is already active");
					const upper = (
						await q(
							sourceJobId != null
								? `SELECT a.created_at, a.id FROM ${sourceFrom} WHERE a.id = source.request_id ORDER BY a.created_at DESC, a.id DESC LIMIT 1`
								: "SELECT created_at, id FROM api_requests ORDER BY created_at DESC, id DESC LIMIT 1",
							sourceJobId != null ? [sourceJobId] : [],
						)
					)[0];
					const id = newId();
					const at = now();
					await q(
						`INSERT INTO usage_token_repair_jobs (id, status, created_by, created_at, updated_at, include_reviewed, upper_created_at, upper_id, protocol_overrides_json, source_job_id) VALUES (?, 'analyzing', ?, ?, ?, ?, ?, ?, ?, ?)`,
						[
							id,
							userId,
							at,
							at,
							pg ? includeReviewed : Number(includeReviewed),
							upper?.created_at ?? null,
							upper?.id ?? null,
							protocolJson,
							sourceJobId ?? null,
						],
					);
					return required(await getJob(id, q));
				}),
			),
		getJob: (id: string) => serial(() => getJob(id)),
		latestJob: () => serial(latestJob),
		cancelJob: (jobId: string) =>
			serial(() =>
				transaction(async (q) => {
					await q(
						"UPDATE usage_token_repair_jobs SET status = 'cancelled', updated_at = ? WHERE id = ? AND status IN ('analyzing', 'ready', 'applying', 'interrupted', 'failed')",
						[now(), jobId],
					);
				}),
			),
		scanPage: (jobId: string) =>
			serial(async (): Promise<RepairRequestRow[]> => {
				const job = await getJob(jobId);
				if (!job || job.status !== "analyzing") throw new Error("Job is not analyzing");
				if (!job.upperId || job.scanned >= TOKEN_REPAIR_MAX_RECORDS) return [];
				if (job.sourceJobId) {
					const conditions = [
						"a.id = source.request_id",
						"a.token_accounting_version IS NULL",
						sourceCachePredicate,
						"(a.created_at, a.id) <= (?, ?)",
					];
					const args: unknown[] = [job.sourceJobId, job.upperCreatedAt, job.upperId];
					if (job.cursorId) {
						conditions.push("(a.created_at, a.id) > (?, ?)");
						args.push(job.cursorCreatedAt, job.cursorId);
					}
					args.push(Math.min(TOKEN_REPAIR_PAGE_SIZE, TOKEN_REPAIR_MAX_RECORDS - job.scanned));
					return (
						await query(
							`SELECT ${sourceRequestColumns} FROM ${sourceFrom} WHERE ${conditions.join(" AND ")} ORDER BY a.created_at, a.id LIMIT ?`,
							args,
						)
					).map((row) => required(requestFrom(row)));
				}
				const conditions = [
					"token_accounting_version IS NULL",
					cachePredicate,
					"(created_at, id) <= (?, ?)",
				];
				const args: unknown[] = [job.upperCreatedAt, job.upperId];
				if (!job.includeReviewed) conditions.push("token_accounting_review_version IS NULL");
				if (job.cursorId) {
					conditions.push("(created_at, id) > (?, ?)");
					args.push(job.cursorCreatedAt, job.cursorId);
				}
				args.push(Math.min(TOKEN_REPAIR_PAGE_SIZE, TOKEN_REPAIR_MAX_RECORDS - job.scanned));
				return (
					await query(
						`SELECT ${requestColumns} FROM ${repairFrom(job.includeReviewed)} WHERE ${conditions.join(" AND ")} ORDER BY created_at, id LIMIT ?`,
						args,
					)
				).map((row) => required(requestFrom(row)));
			}),
		getRequest: (id: string) => serial(() => getRequest(id)),
		getRawDump: (id: string, maxBytes: number) =>
			serial(async (): Promise<string | null> => {
				const limit = Math.min(TOKEN_REPAIR_MAX_DUMP_BYTES, Math.max(0, Math.floor(maxBytes)));
				if (!Number.isFinite(limit)) return null;
				// SQL enforces the byte cap before transferring/materializing a large field.
				const length = pg ? "octet_length(raw_dump_json)" : "length(CAST(raw_dump_json AS BLOB))";
				const row = (
					await query(`SELECT raw_dump_json FROM api_requests WHERE id = ? AND ${length} <= ?`, [
						id,
						limit,
					])
				)[0];
				return typeof row?.raw_dump_json === "string" ? row.raw_dump_json : null;
			}),
		saveCandidate: (jobId: string, row: RepairRequestRow, evidence: TokenRepairEvidence) =>
			serial(() =>
				transaction(async (q) => {
					const job = await requireStatus(q, jobId, "analyzing");
					if (!(await inSource(q, job, row.id)))
						throw new Error("Request is outside the immutable source scope");
					if (!protocolMatches(job, row, evidence))
						throw new Error("Evidence manual protocol does not match immutable job configuration");
					if (!validCounters(evidence.before) || (evidence.after && !validCounters(evidence.after)))
						throw new Error("Invalid token counters");
					if (counters(row).some((n, index) => n !== counters(evidence.before)[index]))
						throw new Error("Evidence snapshot mismatch");
					if (
						(
							await q(
								"SELECT id FROM usage_token_repair_candidates WHERE job_id = ? AND request_id = ?",
								[jobId, row.id],
							)
						).length
					)
						return;
					if (
						!job.upperCreatedAt ||
						!job.upperId ||
						row.createdAt > job.upperCreatedAt ||
						(row.createdAt === job.upperCreatedAt && row.id > job.upperId) ||
						(job.cursorCreatedAt &&
							(row.createdAt < job.cursorCreatedAt ||
								(row.createdAt === job.cursorCreatedAt && row.id <= required(job.cursorId))))
					)
						throw new Error("Candidate is outside the scan cursor");
					if (job.scanned >= TOKEN_REPAIR_MAX_RECORDS)
						throw new Error("Token repair scan limit reached");
					const current = await getRequest(row.id, q);
					const unchanged =
						current &&
						current.tokenAccountingVersion === null &&
						sameRequest(current, row) &&
						current.tokenAccountingReviewVersion === row.tokenAccountingReviewVersion;
					const audited = unchanged
						? evidence
						: {
								...evidence,
								classification: "conflict" as const,
								reason: "Request changed during analysis",
							};
					await q(
						`INSERT INTO usage_token_repair_candidates (id, job_id, request_id, created_at, provider, model, snapshot_json, evidence_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (job_id, request_id) DO NOTHING RETURNING id`,
						[
							newId(),
							jobId,
							row.id,
							now(),
							row.provider,
							row.model,
							boundedJson(snapshot(row), 8192),
							boundedJson(audited, 16384),
						],
					);
					if (unchanged)
						await q(
							"UPDATE api_requests SET token_accounting_review_version = ? WHERE id = ? AND token_accounting_version IS NULL",
							[TOKEN_ACCOUNTING_VERSION, row.id],
						);
					await q(
						"UPDATE usage_token_repair_jobs SET cursor_created_at = ?, cursor_id = ?, scanned = scanned + 1, conflicts = conflicts + ?, updated_at = ? WHERE id = ?",
						[row.createdAt, row.id, Number(audited.classification === "conflict"), now(), jobId],
					);
				}),
			),
		finishAnalysis: (jobId: string) =>
			serial(() => transaction((q) => transition(q, jobId, "ready", ["analyzing"]))),
		setJobStatus: (jobId: string, status: TokenRepairJobStatus, error?: string) =>
			serial(() =>
				transaction(async (q) => {
					const from =
						status === "applying"
							? ["ready"]
							: status === "ready"
								? ["applying"]
								: status === "failed"
									? ["analyzing", "applying"]
									: [];
					if (!from.length) throw new Error("Unsupported token repair state transition");
					await transition(q, jobId, status, from, error);
				}),
			),
		listCandidates: (jobId: string, cursor?: string) =>
			serial(async (): Promise<TokenRepairCandidatePage> => {
				const rows = await query(
					`SELECT * FROM usage_token_repair_candidates WHERE job_id = ? ${cursor ? "AND id > ?" : ""} ORDER BY id LIMIT ?`,
					cursor
						? [jobId, cursor, TOKEN_REPAIR_PAGE_SIZE + 1]
						: [jobId, TOKEN_REPAIR_PAGE_SIZE + 1],
				);
				return {
					records: rows.slice(0, TOKEN_REPAIR_PAGE_SIZE).map(candidateFrom),
					nextCursor:
						rows.length > TOKEN_REPAIR_PAGE_SIZE
							? String(required(rows[TOKEN_REPAIR_PAGE_SIZE - 1]).id)
							: null,
				};
			}),
		getCandidate: (jobId: string, id: string) =>
			serial(async () => {
				const row = await candidateRow(jobId, id);
				return row ? candidateFrom(row) : null;
			}),
		markCandidateConflict: (jobId: string, candidateId: string, reason: string) =>
			serial(() =>
				transaction(async (q) => {
					await requireStatus(q, jobId, "applying");
					const row = await candidateRow(jobId, candidateId, q);
					if (row && !row.applied_at) await conflict(q, row, reason.slice(0, 2048));
				}),
			),
		applyCandidate: (jobId: string, candidateId: string, userId: string) =>
			serial(() =>
				transaction(async (q): Promise<"applied" | "conflict"> => {
					const job = await requireStatus(q, jobId, "applying");
					const row = await candidateRow(jobId, candidateId, q);
					if (!row) return "conflict";
					const evidence = JSON.parse(String(row.evidence_json)) as TokenRepairEvidence;
					const before = JSON.parse(String(row.snapshot_json)) as RepairRequestRow;
					const scoped = before.id === row.request_id && (await inSource(q, job, before.id));
					if (row.applied_at && !scoped) return "conflict";
					if (row.applied_at)
						return protocolMatches(job, before, evidence) ? "applied" : "conflict";
					if (!scoped) {
						await conflict(q, row, "Candidate is outside the immutable source scope");
						return "conflict";
					}
					if (
						evidence.classification !== "repairable" ||
						!evidence.after ||
						!validCounters(evidence.after)
					) {
						await conflict(q, row, "Candidate is not repairable");
						return "conflict";
					}
					if (!protocolMatches(job, before, evidence)) {
						await conflict(
							q,
							row,
							"Persisted manual protocol does not match immutable job configuration",
						);
						return "conflict";
					}
					const current = await getRequest(before.id, q);
					if (
						!current ||
						current.tokenAccountingVersion !== null ||
						before.tokenAccountingVersion !== null ||
						!sameRequest(current, before)
					) {
						await conflict(q, row, "Request deleted, changed, or already normalized");
						return "conflict";
					}
					const after = evidence.after;
					const updated = await q(
						`UPDATE api_requests SET ${counterColumns.map((col) => `${col} = ?`).join(", ")}, token_accounting_version = ?, token_accounting_review_version = ? WHERE id = ? AND token_accounting_version IS NULL AND token_accounting_review_version = ? AND ${counterColumns.map((col) => `${col} = ?`).join(" AND ")} AND ${["user_id", "credential_id", "provider", "model", "message_id", "created_at"].map((col) => `${col} IS NOT DISTINCT FROM ?`).join(" AND ")} RETURNING id`,
						[
							...counters(after),
							TOKEN_ACCOUNTING_VERSION,
							TOKEN_ACCOUNTING_VERSION,
							before.id,
							TOKEN_ACCOUNTING_VERSION,
							...counters(before),
							before.userId,
							before.credentialId,
							before.provider,
							before.model,
							before.messageId,
							before.createdAt,
						],
					);
					if (!updated.length) {
						await conflict(q, row, "Request compare-and-swap failed");
						return "conflict";
					}
					await q(
						"UPDATE usage_token_repair_candidates SET applied_at = ?, applied_by = ?, totals_status = 'pending' WHERE id = ?",
						[now(), userId, candidateId],
					);
					await q(
						"UPDATE usage_token_repair_jobs SET repaired = repaired + 1, updated_at = ? WHERE id = ?",
						[now(), jobId],
					);
					return "applied";
				}),
			),
		/** Interrupt active PostgreSQL statements; a worker can also be terminated externally. */
		cancel: async () => {
			for (const operation of running) operation.cancel();
		},
		close: () => {
			if (closePromise) return closePromise;
			closing = true;
			closePromise = (async () => {
				await queue;
				closed = true;
				sqlite?.close();
				await pg?.close({ timeout: 1 });
			})();
			return closePromise;
		},
	};
}
export type TokenRepairStore = ReturnType<typeof createTokenRepairStore>;
