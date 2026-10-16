import { Database } from "bun:sqlite";
import { createHash } from "node:crypto";
import { chmodSync, closeSync, lstatSync, mkdirSync, openSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import type {
	TokenRepairEvidence,
	TokenRepairModelPage,
	TokenRepairProtocolOverride,
	TokenRepairSummary,
	TokenRepairTotals,
} from "@shared/usage-token-repair";
import type { RepairRequestRow } from "./usage-token-repair-store";

export const repairCounterKeys = [
	"inputTokens",
	"cachedInputTokens",
	"cacheCreationInputTokens",
	"cacheCreation5mTokens",
	"cacheCreation1hTokens",
] as const;
export const fingerprint = (raw: string | null) =>
	createHash("sha256")
		.update(raw ?? "null")
		.digest("hex");
export function zeroTotals(): TokenRepairTotals {
	return Object.fromEntries(repairCounterKeys.map((k) => [k, "0"])) as TokenRepairTotals;
}
export function addTotals(
	target: TokenRepairTotals,
	value: Record<(typeof repairCounterKeys)[number], number | string>,
	sign = 1,
) {
	for (const key of repairCounterKeys) {
		const n = value[key];
		if (typeof n === "number" && !Number.isSafeInteger(n))
			throw new Error("Unsafe source token counter");
		target[key] = (BigInt(target[key]) + BigInt(n) * BigInt(sign)).toString();
	}
}
export function emptySummary(): TokenRepairSummary {
	return {
		previewVersion: null,
		requestCount: 0,
		repairable: 0,
		correct: 0,
		unresolved: 0,
		skipped: 0,
		conflicts: 0,
		modelCount: 0,
		reasons: {},
		before: zeroTotals(),
		after: zeroTotals(),
		delta: zeroTotals(),
		repairBefore: zeroTotals(),
		repairAfter: zeroTotals(),
		appliedDelta: zeroTotals(),
	};
}
export interface ScratchEntry {
	seq: number;
	row: RepairRequestRow;
	evidence: TokenRepairEvidence;
	hash: string;
}
export function validModelIdentity(row: RepairRequestRow) {
	return (
		typeof row.model === "string" &&
		row.model.trim().length > 0 &&
		row.model.length <= 256 &&
		(row.provider === null || row.provider.length <= 128)
	);
}
export function retireRepairScratch(directory: string, ids: string[]): string[] {
	const candidates = ids.slice(0, 20).filter((id) => /^[A-Za-z0-9_-]{21}$/.test(id));
	const root = join(dirname(directory), "token-repair-jobs");
	try {
		const info = lstatSync(root);
		if (!info.isDirectory() || info.isSymbolicLink()) return [];
	} catch (error) {
		// Missing files also acknowledge old jobs and retries after a crash between
		// filesystem deletion and its durable database acknowledgement.
		if ((error as NodeJS.ErrnoException).code === "ENOENT") return candidates;
		throw error;
	}
	const retired: string[] = [];
	for (const id of candidates) {
		let complete = true;
		for (const suffix of [".sqlite", ".sqlite-journal", ".sqlite-wal", ".sqlite-shm"]) {
			const path = join(root, id + suffix);
			try {
				const info = lstatSync(path);
				if (info.isFile() && !info.isSymbolicLink()) rmSync(path);
				else complete = false;
			} catch (error) {
				if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
			}
		}
		if (complete) retired.push(id);
	}
	return retired;
}
/** Private lightweight evidence only. No request/response bodies are persisted here. */
export class TokenRepairScratch {
	readonly db: Database;
	constructor(directory: string, id: string, create = false) {
		if (!/^[A-Za-z0-9_-]{21}$/.test(id)) throw new Error("Invalid maintenance identity");
		const root = join(dirname(directory), "token-repair-jobs");
		mkdirSync(root, { recursive: true, mode: 0o700 });
		if (!lstatSync(root).isDirectory() || lstatSync(root).isSymbolicLink())
			throw new Error("Unsafe maintenance directory");
		chmodSync(root, 0o700);
		const path = join(root, `${id}.sqlite`);
		if (!create && (!lstatSync(path).isFile() || lstatSync(path).isSymbolicLink()))
			throw new Error("Missing maintenance scratch");
		if (create) closeSync(openSync(path, "wx", 0o600));
		for (const suffix of ["-journal", "-wal", "-shm"]) {
			try {
				if (lstatSync(path + suffix).isSymbolicLink()) throw new Error("Unsafe scratch sidecar");
			} catch (error) {
				if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
			}
		}
		this.db = new Database(path, { create: false, readwrite: true, strict: true });
		chmodSync(path, 0o600);
		this.db.exec(
			// Reserve room for the worst-case rollback journal: main + journal < 2 GiB.
			"PRAGMA page_size=4096; PRAGMA journal_mode=DELETE; PRAGMA max_page_count=250000; PRAGMA cache_size=-8192",
		);
		if (create)
			this.db.exec(
				"CREATE TABLE entries(seq INTEGER PRIMARY KEY,request_id TEXT UNIQUE NOT NULL,row_json TEXT NOT NULL,evidence_json TEXT NOT NULL,raw_hash TEXT NOT NULL,plan_json TEXT, outcome TEXT); CREATE TABLE models(identity TEXT PRIMARY KEY,provider TEXT,model TEXT NOT NULL,n INTEGER NOT NULL); PRAGMA user_version=2",
			);
		if ((this.db.query("PRAGMA user_version").get() as { user_version: number }).user_version !== 2)
			throw new Error("Unsupported scratch version");
	}
	append(entries: Omit<ScratchEntry, "seq">[]) {
		this.db.transaction(() => {
			const insert = this.db.query(
				"INSERT INTO entries(request_id,row_json,evidence_json,raw_hash) VALUES (?,?,?,?)",
			);
			const model = this.db.query(
				"INSERT INTO models VALUES (?,?,?,1) ON CONFLICT(identity) DO UPDATE SET n=n+1",
			);
			for (const e of entries) {
				const data = JSON.stringify(e.evidence);
				if (Buffer.byteLength(data) > 16384) throw new Error("Oversized structured evidence");
				insert.run(e.row.id, JSON.stringify(e.row), data, e.hash);
				if (e.evidence.reason === "needs_protocol" && validModelIdentity(e.row))
					model.run(JSON.stringify([e.row.provider, e.row.model]), e.row.provider, e.row.model);
			}
		})();
	}
	page(after = 0, repairOnly = false): ScratchEntry[] {
		return (
			this.db
				.query(
					`SELECT seq,row_json,evidence_json,raw_hash FROM entries WHERE seq>? ${repairOnly ? "AND plan_json IS NOT NULL AND outcome IS NULL" : ""} ORDER BY seq LIMIT 1000`,
				)
				.all(after) as { seq: number; row_json: string; evidence_json: string; raw_hash: string }[]
		).map((e) => ({
			seq: e.seq,
			row: JSON.parse(e.row_json),
			evidence: JSON.parse(e.evidence_json),
			hash: e.raw_hash,
		}));
	}
	plan(seq: number, evidence: TokenRepairEvidence) {
		this.db
			.query("UPDATE entries SET plan_json=? WHERE seq=?")
			.run(evidence.classification === "repairable" ? JSON.stringify(evidence) : null, seq);
	}
	planned(seq: number): TokenRepairEvidence {
		return JSON.parse(
			(this.db.query("SELECT plan_json FROM entries WHERE seq=?").get(seq) as { plan_json: string })
				.plan_json,
		);
	}
	outcome(seq: number, value: string) {
		this.db.query("UPDATE entries SET outcome=? WHERE seq=?").run(value, seq);
	}
	models(cursor?: string, overrides: TokenRepairProtocolOverride[] = []): TokenRepairModelPage {
		const rows = this.db
			.query(
				"SELECT identity,provider,model,n FROM models WHERE identity>? ORDER BY identity LIMIT 101",
			)
			.all(cursor ?? "") as {
			identity: string;
			provider: string | null;
			model: string;
			n: number;
		}[];
		return {
			models: rows.slice(0, 100).map((r) => ({
				provider: r.provider,
				model: r.model,
				requestCount: r.n,
				protocol:
					overrides.find((o) => o.provider === r.provider && o.model === r.model)?.protocol ?? null,
			})),
			nextCursor: rows.length > 100 ? (rows[99]?.identity ?? null) : null,
		};
	}
	modelCount() {
		return Number((this.db.query("SELECT COUNT(*) AS n FROM models").get() as { n: number }).n);
	}
	close() {
		this.db.close();
	}
}
