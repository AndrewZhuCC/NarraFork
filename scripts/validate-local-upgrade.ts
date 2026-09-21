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
]);
const JOURNAL_TAIL_TAG = "0013_omniscient_dormammu";
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
): Record<string, unknown> {
	if (!tableExists(db, table)) throw new Error(`Missing required table: ${table}`);
	const selected = columns(db, table).filter((column) => !excluded.has(column));
	if (!selected.includes("id")) throw new Error(`Missing stable row key: ${table}.id`);
	const statement = db.prepare(
		`SELECT ${selected.map(quoteIdentifier).join(", ")} FROM ${quoteIdentifier(table)} ORDER BY id`,
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
	const summary: Record<string, unknown> = {
		narrators: tableDigest(db, "narrators", excludeNew ? new Set(["next_seq"]) : new Set()),
		narrator_tool_calls: tableDigest(
			db,
			"narrator_tool_calls",
			excludeNew ? new Set(["stream_completed_at"]) : new Set(),
		),
	};
	if (targetTag === "0013_omniscient_dormammu") {
		summary.file_change_scope_recoveries = tableDigest(
			db,
			"file_change_scope_recoveries",
			excludeNew ? new Set(["workspace_lease_id"]) : new Set(),
		);
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

function schemaShape(db: Database): Record<string, unknown> {
	return {
		fileChangeScopeRecoveries: tableSchemaShape(db, "file_change_scope_recoveries"),
		workspaceWriteLeases: tableSchemaShape(db, "workspace_write_leases"),
		narratorsNextSeq: columnShape(db, "narrators", "next_seq"),
		toolCallsStreamCompletedAt: columnShape(db, "narrator_tool_calls", "stream_completed_at"),
	};
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
	if (tags.length !== migrationFiles.length || tags.at(-1) !== JOURNAL_TAIL_TAG) {
		throw new Error(`${JOURNAL_TAIL_TAG} is not the journal tail`);
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
		const newTable =
			target.tag === "0012_curved_bloodstorm"
				? "file_change_scope_recoveries"
				: "workspace_write_leases";
		if (!(newTable in expectedCounts)) expectedCounts[newTable] = 0;
		if (canonical(rowCounts(db)) !== canonical(expectedCounts)) {
			throw new Error("Existing table row counts changed");
		}
		if (canonical(affectedSummary(db, target.tag, true)) !== canonical(beforeAffected)) {
			throw new Error("Existing affected-table fields changed");
		}
		if (target.tag === "0013_omniscient_dormammu") {
			if (columns(db, "workspace_write_leases").length !== 15) {
				throw new Error("workspace_write_leases does not have 15 columns");
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
		const badNextSeq = db
			.query(
				`SELECT count(*) AS count FROM narrators n WHERE n.next_seq < COALESCE((` +
					`SELECT r.seq + 1 FROM narrator_message_refs r WHERE r.narrator_id=n.id ` +
					`ORDER BY r.seq DESC LIMIT 1), 0)`,
			)
			.get() as { count: number };
		if (Number(badNextSeq.count) !== 0) throw new Error("next_seq backfill invariant failed");
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
			if (canonical(schemaShape(db)) !== canonical(schemaShape(fresh))) {
				throw new Error(`${target.tag} schema does not match a fresh migration schema`);
			}
		} finally {
			fresh.close();
		}

		const firstState = canonical({
			ledger: ledger(db),
			counts: rowCounts(db),
			affected: affectedSummary(db, target.tag, false),
			schema: schemaShape(db),
		});
		await runUpgrade(db);
		assertIntegrity(db);
		const secondState = canonical({
			ledger: ledger(db),
			counts: rowCounts(db),
			affected: affectedSummary(db, target.tag, false),
			schema: schemaShape(db),
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
