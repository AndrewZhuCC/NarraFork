import { Database } from "bun:sqlite";
import { afterEach, expect, test } from "bun:test";
import { randomUUID } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { generateSqliteMigrations } from "../../generate-sqlite-migrations";
import { modelHistoryTriggerSuffix } from "../generated-model-history-triggers";
import { readPgMetadata } from "../postgres-migration-metadata";
import { executePostgresMigrations } from "../postgres-migration-transaction";
import { modelHistorySnapshots } from "./model-history-migration-fixture";

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

function writeSchema(root: string, dialect: "sqlite" | "postgres", revisions: boolean) {
	const snapshots = modelHistorySnapshots();
	const snapshot = revisions ? snapshots.after : snapshots.before;
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
	writeSchema(root, "sqlite", false);
	await generateSqliteMigrations(["--name", "model_base"], { root });
	const baseline = readFileSync(join(folder, "0001_model_base.sql"), "utf8");
	writeSchema(root, "sqlite", true);
	await generateSqliteMigrations(["--name", "model_revision"], { root });
	const generated = readFileSync(join(folder, "0002_model_revision.sql"), "utf8");
	expect(generated.endsWith(modelHistoryTriggerSuffix("sqlite"))).toBe(true);
	const db = new Database(":memory:");
	try {
		db.exec(baseline);
		db.exec(generated);
		expect(db.query("SELECT count(*) AS n FROM sqlite_master WHERE type='trigger'").get()).toEqual({
			n: 11,
		});
	} finally {
		db.close();
	}
	await generateSqliteMigrations(["--name", "model_revision"], { root });
	expect(readFileSync(join(folder, "0001_model_base.sql"), "utf8")).toBe(baseline);
	expect(readFileSync(join(folder, "0002_model_revision.sql"), "utf8")).toBe(generated);
}, 30_000);

test("real PG Kit through the official wrapper binds fields and trigger functions to the same convergent snapshot", async () => {
	const { root, folder } = fixture("postgres");
	await executePostgresMigrations("baseline", [], { root });
	writeSchema(root, "postgres", false);
	await executePostgresMigrations("generate", ["--name", "model_base"], { root });
	const baseline = readPgMetadata(folder);
	const baselineSql = readFileSync(
		join(folder, `${baseline.journal.entries.at(-1)?.tag}.sql`),
		"utf8",
	);
	writeSchema(root, "postgres", true);
	await executePostgresMigrations("generate", ["--name", "model_revision"], { root });
	const metadata = readPgMetadata(folder);
	const generated = readFileSync(
		join(folder, `${metadata.journal.entries.at(-1)?.tag}.sql`),
		"utf8",
	);
	expect(generated.endsWith(modelHistoryTriggerSuffix("postgres"))).toBe(true);
	expect(metadata.journal.entries).toHaveLength(3);
	expect(metadata.snapshot.prevId).toBe(baseline.snapshot.id);
	expect(await executePostgresMigrations("generate", [], { root })).toMatchObject({
		changed: false,
	});
	expect(readFileSync(join(folder, `${baseline.journal.entries.at(-1)?.tag}.sql`), "utf8")).toBe(
		baselineSql,
	);
}, 30_000);
