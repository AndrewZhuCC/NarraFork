import { Database } from "bun:sqlite";
import { createHash } from "node:crypto";
import { createReadStream, readFileSync, realpathSync } from "node:fs";
import { resolve } from "node:path";
import { readMigrationFiles } from "drizzle-orm/migrator";
import { applySqliteDataBackfills } from "../server/db/data-backfills";
import { ensureColumns } from "../server/db/ensure-columns";
import { runMigrations } from "../server/db/run-migrations";

const ALLOWED_TARGET_TAGS = new Set([
	"0012_curved_bloodstorm",
	"0013_omniscient_dormammu",
	"0014_blue_tomorrow_man",
]);
const JOURNAL_TAIL_TAGS = new Set(["0013_omniscient_dormammu", "0014_blue_tomorrow_man"]);
const MAX_REPORT_BYTES = 64 * 1024;

type Args = {
	database: string;
	migrations: string;
	onlineDatabase: string;
	backupOriginal: string;
	backupSha256: string;
	sourceSha256: string;
	commit: string;
	version: string;
};

function parseArgs(argv: string[]): Args {
	const values = new Map<string, string>();
	for (let i = 0; i < argv.length; i += 2) {
		const key = argv[i];
		const value = argv[i + 1];
		if (!key?.startsWith("--") || value === undefined) throw new Error("Invalid arguments");
		values.set(key.slice(2), value);
	}
	const take = (key: string) => {
		const value = values.get(key)?.trim();
		if (!value) throw new Error(`Missing --${key}`);
		return value;
	};
	return {
		database: take("database"),
		migrations: take("migrations"),
		onlineDatabase: take("online-database"),
		backupOriginal: take("backup-original"),
		backupSha256: take("backup-sha256"),
		sourceSha256: take("source-sha256"),
		commit: take("commit"),
		version: take("version"),
	};
}

async function sha256(path: string): Promise<string> {
	const hash = createHash("sha256");
	for await (const chunk of createReadStream(path)) hash.update(chunk);
	return hash.digest("hex");
}

function quoteIdentifier(value: string): string {
	return `"${value.replaceAll('"', '""')}"`;
}

function tableExists(db: Database, table: string): boolean {
	return db.query("SELECT 1 FROM sqlite_master WHERE type='table' AND name=?").get(table) != null;
}

function columns(db: Database, table: string): string[] {
	return (
		db.query(`PRAGMA table_info(${quoteIdentifier(table)})`).all() as Array<{ name: string }>
	).map((row) => row.name);
}

function rowCounts(db: Database): Record<string, number> {
	const tables = db
		.query(
			"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name != '__drizzle_migrations' ORDER BY name",
		)
		.all() as Array<{ name: string }>;
	return Object.fromEntries(
		tables.map(({ name }) => {
			const row = db.query(`SELECT count(*) AS count FROM ${quoteIdentifier(name)}`).get() as {
				count: number;
			};
			return [name, Number(row.count)];
		}),
	);
}

function encodeSqlValue(value: unknown): unknown {
	if (value instanceof Uint8Array) return { blobBase64: Buffer.from(value).toString("base64") };
	if (typeof value === "bigint") return { integer: value.toString() };
	return value;
}

function tableDigest(
	db: Database,
	table: string,
	excluded: ReadonlySet<string> = new Set(),
	key = "id",
): Record<string, unknown> {
	if (!tableExists(db, table)) throw new Error(`Missing required table: ${table}`);
	const selected = columns(db, table).filter((column) => !excluded.has(column));
	if (!selected.includes(key)) throw new Error(`Missing stable row key: ${table}.${key}`);
	const statement = db.prepare(
		`SELECT ${selected.map(quoteIdentifier).join(", ")} FROM ${quoteIdentifier(table)} ORDER BY ${quoteIdentifier(key)}`,
	);
	const hash = createHash("sha256");
	hash.update(`${JSON.stringify(selected)}\n`);
	let rows = 0;
	try {
		for (const row of statement.iterate() as Iterable<Record<string, unknown>>) {
			hash.update(`${JSON.stringify(selected.map((column) => encodeSqlValue(row[column])))}\n`);
			rows++;
		}
	} finally {
		statement.finalize();
	}
	return { columns: selected, rows, sha256: hash.digest("hex") };
}

function affectedSummary(
	db: Database,
	targetTag: string,
	excludeNew: boolean,
): Record<string, unknown> {
	const is0014 = targetTag === "0014_blue_tomorrow_man";
	const summary: Record<string, unknown> = {
		narrators: tableDigest(db, "narrators", excludeNew ? new Set(["next_seq"]) : new Set()),
		narrator_tool_calls: tableDigest(
			db,
			"narrator_tool_calls",
			excludeNew
				? new Set(
						is0014
							? ["cost_status", "cost_missing_fields"]
							: ["stream_completed_at", "cost_status", "cost_missing_fields"],
						)
				: new Set(),
		),
	};
	if (targetTag === "0013_omniscient_dormammu" || is0014) {
		summary.file_change_scope_recoveries = tableDigest(
			db,
			"file_change_scope_recoveries",
			excludeNew
				? new Set(
						is0014
							? ["resolution_authority", "maintenance_evidence_json"]
							: ["workspace_lease_id", "resolution_authority", "maintenance_evidence_json"],
						)
				: new Set(),
		);
	}
	if (is0014) {
		for (const [table, added, key] of [
			["api_requests", ["user_id", "cost_status", "cost_missing_fields"], "id"],
			["credential_usage_totals", ["partial_request_count"], "id"],
			["narrator_messages", ["cost_status", "cost_missing_fields"], "id"],
			["workspace_write_leases", ["execution_class", "termination_evidence_json"], "lease_id"],
		] as const) {
			summary[table] = tableDigest(db, table, excludeNew ? new Set<string>(added) : new Set(), key);
		}
	}
	return summary;
}

function columnShape(db: Database, table: string, column: string): unknown[] {
	return (
		db.query(`PRAGMA table_info(${quoteIdentifier(table)})`).all() as Array<Record<string, unknown>>
	)
		.filter((row) => row.name === column)
		.map(({ cid: _cid, ...rest }) => rest);
}

function tableSchemaShape(db: Database, table: string): Record<string, unknown> {
	const indexes = (
		db.query(`PRAGMA index_list(${quoteIdentifier(table)})`).all() as Array<
			Record<string, unknown> & { name: string }
		>
	)
		.map(({ seq: _seq, ...index }) => ({
			...index,
			columns: db.query(`PRAGMA index_xinfo(${quoteIdentifier(index.name)})`).all(),
		}))
		.sort((left, right) => String(left.name).localeCompare(String(right.name)));
	return {
		tableXInfo: db.query(`PRAGMA table_xinfo(${quoteIdentifier(table)})`).all(),
		foreignKeys: db.query(`PRAGMA foreign_key_list(${quoteIdentifier(table)})`).all(),
		indexes,
	};
}

function schemaShape(db: Database, targetTag?: string): Record<string, unknown> {
	const shape: Record<string, unknown> = {
		fileChangeScopeRecoveries: tableSchemaShape(db, "file_change_scope_recoveries"),
		workspaceWriteLeases: tableSchemaShape(db, "workspace_write_leases"),
		narratorsNextSeq: columnShape(db, "narrators", "next_seq"),
		toolCallsStreamCompletedAt: columnShape(db, "narrator_tool_calls", "stream_completed_at"),
	};
	if (targetTag === "0014_blue_tomorrow_man") {
		for (const table of [
			"notifications",
			"user_usage_totals",
			"workspace_execution_owners",
			"api_requests",
			"credential_usage_totals",
			"narrator_tool_calls",
		]) shape[table] = tableSchemaShape(db, table);
		// Legacy message migrations arrange existing columns differently; compare the
		// two added fields by definition, not their physical cid/index positions.
		for (const column of ["cost_status", "cost_missing_fields"]) {
			shape[`narrator_messages.${column}`] = columnShape(db, "narrator_messages", column);
		}
	}
	return shape;
}

function ledger(db: Database): string[] {
	return (
		db.query("SELECT hash FROM __drizzle_migrations ORDER BY created_at, id").all() as Array<{
			hash: string;
		}>
	).map((row) => row.hash);
}

function assertIntegrity(db: Database): void {
	const quick = db.query("PRAGMA quick_check").all() as Array<Record<string, unknown>>;
	if (quick.length !== 1 || Object.values(quick[0])[0] !== "ok")
		throw new Error("quick_check failed");
	const foreignKeys = db.query("PRAGMA foreign_key_check").all();
	if (foreignKeys.length) throw new Error(`foreign_key_check failed (${foreignKeys.length})`);
}

function canonical(value: unknown): string {
	const normalize = (item: unknown): unknown => {
		if (Array.isArray(item)) return item.map(normalize);
		if (item && typeof item === "object") {
			return Object.fromEntries(
				Object.entries(item as Record<string, unknown>)
					.sort(([left], [right]) => left.localeCompare(right))
					.map(([key, child]) => [key, normalize(child)]),
			);
		}
		return item;
	};
	return JSON.stringify(normalize(value));
}

async function runUpgrade(db: Database): Promise<void> {
	await runMigrations(db);
	ensureColumns(db);
	await applySqliteDataBackfills(db);
}

async function main() {
	const args = parseArgs(Bun.argv.slice(2));
	const database = realpathSync(args.database);
	const online = realpathSync(args.onlineDatabase);
	const backupOriginal = realpathSync(args.backupOriginal);
	if (database === online || database === backupOriginal)
		throw new Error("Validation database is not isolated");
	if (!process.env.NARRAFORK_HOME?.trim()) throw new Error("NARRAFORK_HOME isolation is required");
	if (!/^[0-9a-f]{64}$/.test(args.backupSha256) || !/^[0-9a-f]{64}$/.test(args.sourceSha256)) {
		throw new Error("Invalid sha256 binding");
	}
	if ((await sha256(database)) !== args.backupSha256)
		throw new Error("Validation copy hash mismatch");

	const migrations = realpathSync(args.migrations);
	const repo = resolve(migrations, "..");
	if (migrations !== realpathSync(resolve(repo, "drizzle")))
		throw new Error("Unexpected migrations path");
	process.chdir(repo);
	const migrationFiles = readMigrationFiles({ migrationsFolder: migrations });
	const journal = JSON.parse(readFileSync(resolve(migrations, "meta/_journal.json"), "utf8")) as {
		entries?: Array<{ tag?: string }>;
	};
	const tags = journal.entries?.map((entry) => entry.tag) ?? [];
	if (tags.length !== migrationFiles.length || !JOURNAL_TAIL_TAGS.has(tags.at(-1) ?? "")) {
		throw new Error("Unexpected migration journal tail");
	}
	const migrationsWithTags = migrationFiles.map((migration, index) => ({
		...migration,
		tag: tags[index],
	}));

	const db = new Database(database, { strict: true });
	db.exec("PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON");
	try {
		const beforeLedger = new Set(ledger(db));
		const pending = migrationsWithTags.filter((migration) => !beforeLedger.has(migration.hash));
		const target = pending[0];
		if (pending.length !== 1 || !target?.tag || !ALLOWED_TARGET_TAGS.has(target.tag)) {
			throw new Error("Validation is limited to a single explicitly allowed migration");
		}
		const beforeCounts = rowCounts(db);
		const beforeAffected = affectedSummary(db, target.tag, true);
		const hadNextSeq = columns(db, "narrators").includes("next_seq");
		if (hadNextSeq) {
			db.exec("CREATE TEMP TABLE validation_next_seq_before AS SELECT id, next_seq FROM narrators");
		}

		await runUpgrade(db);
		assertIntegrity(db);
		const applied = new Set(ledger(db));
		if (!pending.every((migration) => applied.has(migration.hash))) {
			throw new Error("Target migration hash was not applied");
		}
		const expectedCounts = { ...beforeCounts };
		const newTables =
			target.tag === "0012_curved_bloodstorm"
				? ["file_change_scope_recoveries"]
				: target.tag === "0013_omniscient_dormammu"
					? ["workspace_write_leases"]
					: ["notifications", "user_usage_totals", "workspace_execution_owners"];
		for (const table of newTables) if (!(table in expectedCounts)) expectedCounts[table] = 0;
		if (canonical(rowCounts(db)) !== canonical(expectedCounts)) {
			throw new Error("Existing table row counts changed");
		}
		const afterAffected = affectedSummary(db, target.tag, true);
		if (canonical(afterAffected) !== canonical(beforeAffected)) {
			const changed = Object.keys(beforeAffected).filter(
				(table) => canonical(beforeAffected[table]) !== canonical(afterAffected[table]),
			);
			throw new Error(`Existing affected-table fields changed: ${changed.join(", ")}`);
		}
		if (target.tag === "0013_omniscient_dormammu") {
			const originalLeaseColumns = columns(db, "workspace_write_leases").filter(
				(column) => column !== "execution_class" && column !== "termination_evidence_json",
			);
			if (originalLeaseColumns.length !== 15) {
				throw new Error("workspace_write_leases does not have 15 original columns");
			}
			const populatedLeaseIds = db
				.query(
					"SELECT count(*) AS count FROM file_change_scope_recoveries WHERE workspace_lease_id IS NOT NULL",
				)
				.get() as { count: number };
			if (Number(populatedLeaseIds.count) !== 0) {
				throw new Error("Existing recovery rows gained workspace_lease_id values");
			}
		}
		// Historical gaps are repaired on the first claim, not during startup migration.
		const badNextSeq = db.query("SELECT count(*) AS count FROM narrators WHERE next_seq < 0").get() as {
			count: number;
		};
		if (Number(badNextSeq.count) !== 0) throw new Error("next_seq cannot be negative");
		if (hadNextSeq) {
			const lowered = db
				.query(
					"SELECT count(*) AS count FROM narrators n JOIN validation_next_seq_before b USING(id) WHERE n.next_seq < b.next_seq",
				)
				.get() as { count: number };
			if (Number(lowered.count) !== 0) throw new Error("next_seq was lowered");
		}

		const fresh = new Database(":memory:", { strict: true });
		fresh.exec("PRAGMA foreign_keys=ON");
		try {
			await runUpgrade(fresh);
			if (canonical(schemaShape(db, target.tag)) !== canonical(schemaShape(fresh, target.tag))) {
				throw new Error(`${target.tag} schema does not match a fresh migration schema`);
			}
		} finally {
			fresh.close();
		}

		const firstState = canonical({
			ledger: ledger(db),
			counts: rowCounts(db),
			affected: affectedSummary(db, target.tag, false),
			schema: schemaShape(db, target.tag),
		});
		await runUpgrade(db);
		assertIntegrity(db);
		const secondState = canonical({
			ledger: ledger(db),
			counts: rowCounts(db),
			affected: affectedSummary(db, target.tag, false),
			schema: schemaShape(db, target.tag),
		});
		if (firstState !== secondState) throw new Error("Upgrade is not idempotent");

		const report = {
			status: "ok",
			validator: 1,
			backupSha256: args.backupSha256,
			sourceSha256: args.sourceSha256,
			commit: args.commit,
			version: args.version,
			targetMigrations: pending.map((migration) => ({ tag: migration.tag, hash: migration.hash })),
			checks: {
				migrationApplied: true,
				quickCheck: true,
				foreignKeyCheck: true,
				schemaMatchesFresh: true,
				rowCountsPreserved: true,
				affectedFieldsPreserved: true,
				nextSeqValid: true,
				idempotent: true,
			},
		};
		const output = JSON.stringify(report);
		if (Buffer.byteLength(output) > MAX_REPORT_BYTES)
			throw new Error("Validation report too large");
		process.stdout.write(`${output}\n`);
	} finally {
		db.close();
	}
}

await main();
