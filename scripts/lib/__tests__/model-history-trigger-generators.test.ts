import { Database } from "bun:sqlite";
import { afterEach, expect, test } from "bun:test";
import { randomUUID } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import {
	MODEL_HISTORY_SQLITE_TRIGGERS,
	MODEL_HISTORY_V2_SQLITE_TRIGGERS,
} from "../../../server/services/agent-runtime/model-history-revision-sql";
import { generateSqliteMigrations } from "../../generate-sqlite-migrations";
import {
	MODEL_HISTORY_REVISION_CONTRACT,
	MODEL_HISTORY_REVISION_CONTRACT_V1,
	MODEL_HISTORY_REVISION_CONTRACT_V2,
	modelHistoryReinstall,
	modelHistoryTriggerSuffix,
} from "../generated-model-history-triggers";
import { readPgMetadata } from "../postgres-migration-metadata";
import { executePostgresMigrations } from "../postgres-migration-transaction";
import {
	modelHistoryBlockSnapshot,
	modelHistorySnapshots,
	modelHistoryStateSnapshot,
} from "./model-history-migration-fixture";

const repository = resolve(import.meta.dir, "../../..");
const roots: string[] = [];
afterEach(() => {
	for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true });
});

function fixture(dialect: "sqlite" | "postgres") {
	const root = mkdtempSync(join(repository, ".narrafork/model-history-kit-fixture-"));
	roots.push(root);
	mkdirSync(join(root, "server/db"), { recursive: true });
	symlinkSync(join(repository, "node_modules"), join(root, "node_modules"), "junction");
	const folder = join(root, dialect === "sqlite" ? "drizzle" : "drizzle-postgres");
	mkdirSync(join(folder, "meta"), { recursive: true });
	writeFileSync(join(folder, "0000_empty.sql"), "-- immutable empty fixture baseline\n");
	writeFileSync(
		join(folder, "meta/_journal.json"),
		JSON.stringify({
			version: "7",
			dialect: dialect === "sqlite" ? "sqlite" : "postgresql",
			entries: [
				{
					idx: 0,
					version: dialect === "sqlite" ? "6" : "7",
					when: 1700000000000,
					tag: "0000_empty",
					breakpoints: true,
				},
			],
		}),
	);
	writeFileSync(
		join(folder, "meta/0000_snapshot.json"),
		JSON.stringify({
			version: dialect === "sqlite" ? "6" : "7",
			dialect: dialect === "sqlite" ? "sqlite" : "postgresql",
			id: randomUUID(),
			prevId: "00000000-0000-0000-0000-000000000000",
			tables: {},
			views: {},
			enums: {},
			_meta: { schemas: {}, tables: {}, columns: {} },
			...(dialect === "sqlite"
				? { internal: { indexes: {} } }
				: { schemas: {}, sequences: {}, roles: {}, policies: {} }),
		}),
	);
	if (dialect === "sqlite")
		writeFileSync(
			join(root, "drizzle.config.ts"),
			`import { defineConfig } from "drizzle-kit"; export default defineConfig({ dialect: "sqlite", schema: "./server/db/schema.ts", out: "./drizzle" });`,
		);
	else
		writeFileSync(
			join(root, "drizzle.pg.config.ts"),
			`import { defineConfig } from "drizzle-kit"; import { resolvePgStageOut, resolvePgSchemaPath } from ${JSON.stringify(join(repository, "scripts/lib/postgres-kit.ts"))}; export default defineConfig({ dialect: "postgresql", schema: resolvePgSchemaPath(process.cwd()), out: resolvePgStageOut(process.cwd()) });`,
		);
	return { root, folder };
}

function writeSchema(
	root: string,
	dialect: "sqlite" | "postgres",
	stage: "base" | "revisions" | "blocks" | "state" | "rebuild",
) {
	const snapshots = modelHistorySnapshots();
	const snapshot =
		stage === "state" || stage === "rebuild"
			? modelHistoryStateSnapshot()
			: stage === "blocks"
				? modelHistoryBlockSnapshot()
				: stage === "revisions"
					? snapshots.after
					: snapshots.before;
	// SQLite cannot alter a default in place, so Kit rebuilds the trigger table `narrators`.
	if (stage === "rebuild")
		snapshot.tables.narrators.columns.title = {
			...snapshot.tables.narrators.columns.title,
			default: "untitled",
		};
	const tableFunction = dialect === "sqlite" ? "sqliteTable" : "pgTable";
	const schema = [
		`import { ${tableFunction}, integer, text } from "drizzle-orm/${dialect === "sqlite" ? "sqlite" : "pg"}-core";`,
		...Object.entries(snapshot.tables).map(([name, table]) => {
			const columns = Object.entries(table.columns).map(
				([column, meta]) =>
					`${JSON.stringify(column)}: ${meta.type === "integer" ? "integer" : "text"}(${JSON.stringify(column)})${meta.primaryKey ? ".primaryKey()" : ""}${meta.notNull ? ".notNull()" : ""}${meta.default !== undefined ? `.default(${JSON.stringify(meta.default)})` : ""}`,
			);
			return `export const ${name} = ${tableFunction}(${JSON.stringify(name)}, { ${columns.join(", ")} });`;
		}),
	].join("\n");
	writeFileSync(
		join(root, dialect === "sqlite" ? "server/db/schema.ts" : "server/db/postgres-schema.ts"),
		schema,
	);
}

test("real SQLite Kit through the official wrapper publishes fields and triggers in one new immutable batch", async () => {
	const { root, folder } = fixture("sqlite");
	writeSchema(root, "sqlite", "base");
	await generateSqliteMigrations(["--name", "model_base"], { root });
	const baseline = readFileSync(join(folder, "0001_model_base.sql"), "utf8");
	writeSchema(root, "sqlite", "revisions");
	await generateSqliteMigrations(["--name", "model_revision"], { root });
	const generated = readFileSync(join(folder, "0002_model_revision.sql"), "utf8");
	expect(
		generated.endsWith(modelHistoryTriggerSuffix("sqlite", MODEL_HISTORY_REVISION_CONTRACT_V1)),
	).toBe(true);
	writeSchema(root, "sqlite", "blocks");
	await generateSqliteMigrations(["--name", "model_blocks"], { root });
	const reinstall = readFileSync(join(folder, "0003_model_blocks.sql"), "utf8");
	expect(
		reinstall.endsWith(modelHistoryTriggerSuffix("sqlite", MODEL_HISTORY_REVISION_CONTRACT_V2)),
	).toBe(true);
	writeSchema(root, "sqlite", "state");
	await generateSqliteMigrations(["--name", "model_state"], { root });
	const stateReinstall = readFileSync(join(folder, "0004_model_state.sql"), "utf8");
	expect(stateReinstall.endsWith(modelHistoryTriggerSuffix("sqlite"))).toBe(true);
	const db = new Database(":memory:");
	try {
		db.exec(baseline);
		db.exec(generated);
		const triggers = () =>
			db
				.query<{ name: string }, []>(
					"SELECT name FROM sqlite_master WHERE type='trigger' ORDER BY name",
				)
				.all()
				.map((row) => row.name);
		const contract = () =>
			db.query("SELECT installed_version AS v FROM model_history_revision_contracts").all();
		expect(triggers()).toHaveLength(11);
		// The block-storage migration is a versioned reinstall: v1 is dropped, v2 replaces it.
		db.exec(reinstall);
		expect(triggers()).toEqual([...MODEL_HISTORY_V2_SQLITE_TRIGGERS].sort());
		expect(triggers().every((name) => name.startsWith("model_history_v2_"))).toBe(true);
		expect(contract()).toEqual([{ v: MODEL_HISTORY_REVISION_CONTRACT_V2 }]);
		// So is the state-write migration: v2 is dropped, v3 replaces it.
		db.exec(stateReinstall);
		expect(triggers()).toEqual([...MODEL_HISTORY_SQLITE_TRIGGERS].sort());
		expect(triggers().every((name) => name.startsWith("model_history_v3_"))).toBe(true);
		expect(contract()).toEqual([{ v: MODEL_HISTORY_REVISION_CONTRACT }]);
	} finally {
		db.close();
	}
	await generateSqliteMigrations(["--name", "model_state"], { root });
	expect(readFileSync(join(folder, "0001_model_base.sql"), "utf8")).toBe(baseline);
	expect(readFileSync(join(folder, "0002_model_revision.sql"), "utf8")).toBe(generated);
	expect(readFileSync(join(folder, "0003_model_blocks.sql"), "utf8")).toBe(reinstall);
	expect(readFileSync(join(folder, "0004_model_state.sql"), "utf8")).toBe(stateReinstall);
}, 60_000);

test("real SQLite Kit wraps a rebuild of an installed contract's table in its versioned reinstall", async () => {
	const { root, folder } = fixture("sqlite");
	const migrations: string[] = [];
	for (const [stage, name] of [
		["base", "model_base"],
		["revisions", "model_revision"],
		["blocks", "model_blocks"],
		["state", "model_state"],
		["rebuild", "model_rebuild"],
	] as const) {
		writeSchema(root, "sqlite", stage);
		await generateSqliteMigrations(["--name", name], { root });
		migrations.push(readFileSync(join(folder, `000${migrations.length + 1}_${name}.sql`), "utf8"));
	}
	const rebuild = migrations[4];
	const state = modelHistoryStateSnapshot();
	const wrapper = modelHistoryReinstall(state, state, "sqlite");
	if (!wrapper) throw new Error("v3 schema must offer a reinstall");
	expect(rebuild.startsWith(wrapper.prefix)).toBe(true);
	expect(rebuild.endsWith(wrapper.suffix)).toBe(true);
	expect(rebuild).toContain("DROP TABLE `narrators`;");
	const db = new Database(":memory:");
	const clean = new Database(":memory:");
	const catalog = (database: Database) =>
		database
			.query("SELECT name, tbl_name, sql FROM sqlite_master WHERE type='trigger' ORDER BY name")
			.all();
	try {
		for (const migration of migrations.slice(0, 4)) db.exec(migration);
		db.exec("INSERT INTO narrators(id, title) VALUES ('owner', 'kept')");
		const kept = db.query("SELECT id, title, model_history_revision FROM narrators").all();
		db.exec(rebuild);
		expect(db.query("SELECT id, title, model_history_revision FROM narrators").all()).toEqual(kept);
		// Byte-identical to a database that installed the contract without any rebuild.
		for (const migration of migrations.slice(0, 4)) clean.exec(migration);
		expect(catalog(db)).toEqual(catalog(clean));
		expect(catalog(db).map((row) => (row as { name: string }).name)).toEqual(
			[...MODEL_HISTORY_SQLITE_TRIGGERS].sort(),
		);
		expect(
			db.query("SELECT installed_version AS v FROM model_history_revision_contracts").all(),
		).toEqual([{ v: MODEL_HISTORY_REVISION_CONTRACT }]);
	} finally {
		clean.close();
		db.close();
	}
	// History stays immutable: a no-change run only validates the wrapped batch read-only.
	await generateSqliteMigrations(["--name", "model_rebuild"], { root });
	expect(readFileSync(join(folder, "0005_model_rebuild.sql"), "utf8")).toBe(rebuild);
}, 60_000);

test("real PG Kit through the official wrapper binds fields and trigger functions to the same convergent snapshot", async () => {
	const { root, folder } = fixture("postgres");
	await executePostgresMigrations("baseline", [], { root });
	writeSchema(root, "postgres", "base");
	await executePostgresMigrations("generate", ["--name", "model_base"], { root });
	const baseline = readPgMetadata(folder);
	const baselineSql = readFileSync(
		join(folder, `${baseline.journal.entries.at(-1)?.tag}.sql`),
		"utf8",
	);
	writeSchema(root, "postgres", "revisions");
	await executePostgresMigrations("generate", ["--name", "model_revision"], { root });
	const metadata = readPgMetadata(folder);
	const generated = readFileSync(
		join(folder, `${metadata.journal.entries.at(-1)?.tag}.sql`),
		"utf8",
	);
	expect(
		generated.endsWith(modelHistoryTriggerSuffix("postgres", MODEL_HISTORY_REVISION_CONTRACT_V1)),
	).toBe(true);
	expect(metadata.journal.entries).toHaveLength(3);
	writeSchema(root, "postgres", "blocks");
	await executePostgresMigrations("generate", ["--name", "model_blocks"], { root });
	const blocks = readPgMetadata(folder);
	const reinstall = readFileSync(join(folder, `${blocks.journal.entries.at(-1)?.tag}.sql`), "utf8");
	expect(
		reinstall.endsWith(modelHistoryTriggerSuffix("postgres", MODEL_HISTORY_REVISION_CONTRACT_V2)),
	).toBe(true);
	expect(blocks.journal.entries).toHaveLength(4);
	expect(blocks.snapshot.prevId).toBe(metadata.snapshot.id);
	writeSchema(root, "postgres", "state");
	await executePostgresMigrations("generate", ["--name", "model_state"], { root });
	const state = readPgMetadata(folder);
	const stateReinstall = readFileSync(
		join(folder, `${state.journal.entries.at(-1)?.tag}.sql`),
		"utf8",
	);
	expect(stateReinstall.endsWith(modelHistoryTriggerSuffix("postgres"))).toBe(true);
	expect(state.journal.entries).toHaveLength(5);
	expect(state.snapshot.prevId).toBe(blocks.snapshot.id);
	expect(await executePostgresMigrations("generate", [], { root })).toMatchObject({
		changed: false,
	});
	expect(readFileSync(join(folder, `${baseline.journal.entries.at(-1)?.tag}.sql`), "utf8")).toBe(
		baselineSql,
	);
}, 60_000);
