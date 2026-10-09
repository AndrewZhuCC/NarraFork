import { Database } from "bun:sqlite";
import { describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { applySqliteDataBackfills } from "../server/db/data-backfills";
import { applyPendingMigrationsByHash } from "../server/db/run-migrations";

const ROOT = resolve(import.meta.dir, "..");

function sha256(path: string): string {
	return createHash("sha256").update(readFileSync(path)).digest("hex");
}

describe("validate-local-upgrade", () => {
	test("validates migration 0013 on an isolated fixture copy", async () => {
		const root = mkdtempSync(join(tmpdir(), "nf-local-upgrade-validator-"));
		try {
			const preMigrations = join(root, "drizzle");
			cpSync(join(ROOT, "drizzle"), preMigrations, { recursive: true });
			const journalPath = join(preMigrations, "meta/_journal.json");
			const journal = JSON.parse(readFileSync(journalPath, "utf8"));
			const migrationIndex = journal.entries.findIndex(
				(entry: { tag: string }) => entry.tag === "0013_omniscient_dormammu",
			);
			if (migrationIndex < 0) throw new Error("Missing 0013 migration fixture");
			const target = journal.entries[migrationIndex] as { tag: string };
			for (const entry of journal.entries.splice(migrationIndex) as Array<{ tag: string }>) {
				rmSync(join(preMigrations, `${entry.tag}.sql`));
			}
			writeFileSync(journalPath, JSON.stringify(journal));

			const original = join(root, "backup.db");
			const db = new Database(original);
			db.exec("PRAGMA foreign_keys=ON");
			applyPendingMigrationsByHash(db, preMigrations);
			await applySqliteDataBackfills(db);
			// Current ensureColumns would add 0015's nullable field; pre-seed it to isolate 0013.
			db.exec("ALTER TABLE narrators ADD COLUMN turn_ended_at text");
			db.run(
				"INSERT INTO narrators (id, created_at, updated_at, traits, next_seq) VALUES (?, ?, ?, ?, ?)",
				["narrator-fixture", "2026-09-21T00:00:00Z", "2026-09-21T00:00:00Z", '["standalone"]', 0],
			);
			db.run(
				"INSERT INTO narrator_messages (id, narrator_id, role, content_json, created_at) VALUES (?, ?, ?, ?, ?)",
				["message-fixture", "narrator-fixture", "user", "[]", "2026-09-21T00:00:00Z"],
			);
			db.run(
				"INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq) VALUES (?, ?, ?, ?)",
				["ref-fixture", "narrator-fixture", "message-fixture", 7],
			);
			db.run(
				`INSERT INTO file_change_scopes
					(id, source_instance_id, device_id, workspace_instance_id, canonical_root,
					 display_root, path_flavor, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				[
					"scope-fixture",
					"source-fixture",
					"device-fixture",
					"workspace-fixture",
					"/fixture",
					"/fixture",
					"posix",
					"2026-09-21T00:00:00Z",
					"2026-09-21T00:00:00Z",
				],
			);
			db.run(
				`INSERT INTO file_change_scope_recoveries
					(id, scope_id, device_id, canonical_root, path_flavor, effect_decisions_json,
					 scope_revision_before, fencing_token_before, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				[
					"recovery-fixture",
					"scope-fixture",
					"device-fixture",
					"/fixture",
					"posix",
					"[]",
					0,
					0,
					"2026-09-21T00:00:00Z",
				],
			);
			expect(
				(
					db.query("PRAGMA table_info(file_change_scope_recoveries)").all() as Array<{
						name: string;
					}>
				).some((column) => column.name === "workspace_lease_id"),
			).toBe(false);
			db.close();

			journal.entries.push(target);
			writeFileSync(journalPath, JSON.stringify(journal));
			cpSync(join(ROOT, "drizzle", `${target.tag}.sql`), join(preMigrations, `${target.tag}.sql`));

			const validation = join(root, "validation.db");
			cpSync(original, validation);
			const online = join(root, "online.db");
			writeFileSync(online, "not opened");
			const isolatedHome = join(root, "home");

			const child = Bun.spawnSync({
				cmd: [
					process.execPath,
					join(ROOT, "scripts/validate-local-upgrade.ts"),
					"--database",
					validation,
					"--migrations",
					preMigrations,
					"--online-database",
					online,
					"--backup-original",
					original,
					"--backup-sha256",
					sha256(original),
					"--source-sha256",
					"1".repeat(64),
					"--commit",
					"a".repeat(40),
					"--version",
					"0.7.7",
				],
				cwd: ROOT,
				env: { ...Bun.env, NARRAFORK_HOME: isolatedHome, NARRAFORK_LOG_LEVEL: "error" },
				stdout: "pipe",
				stderr: "pipe",
				timeout: 300_000,
			});
			const stderr = child.stderr.toString();
			expect(child.exitCode, stderr).toBe(0);
			const report = JSON.parse(child.stdout.toString());
			expect(report.status).toBe("ok");
			expect(report.targetMigrations).toEqual([
				{
					tag: "0013_omniscient_dormammu",
					hash: sha256(join(ROOT, "drizzle/0013_omniscient_dormammu.sql")),
				},
			]);
			expect(report.checks).toEqual({
				migrationApplied: true,
				quickCheck: true,
				foreignKeyCheck: true,
				schemaMatchesFresh: true,
				rowCountsPreserved: true,
				affectedFieldsPreserved: true,
				nextSeqValid: true,
				idempotent: true,
			});
			expect(sha256(original)).toBe(report.backupSha256);
			const validated = new Database(validation, { readonly: true });
			try {
				const narrator = validated
					.query("SELECT next_seq FROM narrators WHERE id = 'narrator-fixture'")
					.get() as { next_seq: number };
				// The first persisted ref claim raises this floor; migration preserves it.
				expect(narrator.next_seq).toBe(0);
				const recovery = validated
					.query(
						"SELECT workspace_lease_id FROM file_change_scope_recoveries WHERE id = 'recovery-fixture'",
					)
					.get() as { workspace_lease_id: string | null };
				expect(recovery.workspace_lease_id).toBeNull();
				const leases = validated
					.query("SELECT count(*) AS count FROM workspace_write_leases")
					.get() as {
					count: number;
				};
				expect(leases.count).toBe(0);
			} finally {
				validated.close();
			}
		} finally {
			rmSync(root, { recursive: true, force: true });
		}
	}, 300_000);

	test("validates migration 0014 on an isolated fixture copy", async () => {
		const root = mkdtempSync(join(tmpdir(), "nf-local-upgrade-0014-"));
		try {
			const migrations = join(root, "drizzle");
			cpSync(join(ROOT, "drizzle"), migrations, { recursive: true });
			const journalPath = join(migrations, "meta/_journal.json");
			const journal = JSON.parse(readFileSync(journalPath, "utf8"));
			const migrationIndex = journal.entries.findIndex(
				(entry: { tag: string }) => entry.tag === "0014_blue_tomorrow_man",
			);
			if (migrationIndex < 0) throw new Error("Missing 0014 migration fixture");
			const target = journal.entries[migrationIndex] as { tag: string };
			for (const entry of journal.entries.splice(migrationIndex) as Array<{ tag: string }>) {
				rmSync(join(migrations, `${entry.tag}.sql`));
			}
			writeFileSync(journalPath, JSON.stringify(journal));

			const original = join(root, "backup.db");
			const db = new Database(original);
			db.exec("PRAGMA foreign_keys=ON");
			applyPendingMigrationsByHash(db, migrations);
			await applySqliteDataBackfills(db);
			// Current ensureColumns would add 0015's nullable field; pre-seed it to isolate 0014.
			db.exec("ALTER TABLE narrators ADD COLUMN turn_ended_at text");
			db.run(
				"INSERT INTO narrators (id, created_at, updated_at, traits, next_seq) VALUES (?, ?, ?, ?, ?)",
				["narrator-0014", "2026-09-21T00:00:00Z", "2026-09-21T00:00:00Z", '["standalone"]', 0],
			);
			db.run(
				"INSERT INTO narrator_messages (id, narrator_id, role, content_json, created_at) VALUES (?, ?, ?, ?, ?)",
				["message-0014", "narrator-0014", "user", "[]", "2026-09-21T00:00:00Z"],
			);
			db.close();

			journal.entries.push(target);
			writeFileSync(journalPath, JSON.stringify(journal));
			cpSync(join(ROOT, "drizzle", `${target.tag}.sql`), join(migrations, `${target.tag}.sql`));
			const validation = join(root, "validation.db");
			cpSync(original, validation);
			const online = join(root, "online.db");
			writeFileSync(online, "not opened");
			const originalHash = sha256(original);
			const child = Bun.spawnSync({
				cmd: [
					process.execPath,
					join(ROOT, "scripts/validate-local-upgrade.ts"),
					"--database",
					validation,
					"--migrations",
					migrations,
					"--online-database",
					online,
					"--backup-original",
					original,
					"--backup-sha256",
					originalHash,
					"--source-sha256",
					"1".repeat(64),
					"--commit",
					"a".repeat(40),
					"--version",
					"0.7.8",
				],
				cwd: ROOT,
				env: { ...Bun.env, NARRAFORK_HOME: join(root, "home"), NARRAFORK_LOG_LEVEL: "error" },
				stdout: "pipe",
				stderr: "pipe",
				timeout: 300_000,
			});
			expect(child.exitCode, child.stderr.toString()).toBe(0);
			const report = JSON.parse(child.stdout.toString());
			expect(report.status).toBe("ok");
			expect(report.targetMigrations).toEqual([
				{ tag: target.tag, hash: sha256(join(ROOT, "drizzle", `${target.tag}.sql`)) },
			]);
			expect(sha256(original)).toBe(originalHash);
			const upgraded = new Database(validation, { readonly: true });
			try {
				expect(upgraded.query("SELECT id FROM narrator_messages").all()).toContainEqual({
					id: "message-0014",
				});
				for (const table of ["notifications", "user_usage_totals", "workspace_execution_owners"]) {
					expect(
						upgraded.query("SELECT 1 FROM sqlite_master WHERE name=?").get(table),
					).not.toBeNull();
				}
			} finally {
				upgraded.close();
			}
		} finally {
			rmSync(root, { recursive: true, force: true });
		}
	}, 300_000);

	async function validateTailMigration(
		targetTag: "0015_nifty_magma" | "0016_high_shiva" | "0017_merge_opensource_083",
	) {
		const root = mkdtempSync(join(tmpdir(), `nf-local-upgrade-${targetTag.slice(0, 4)}-`));
		try {
			const migrations = join(root, "drizzle");
			cpSync(join(ROOT, "drizzle"), migrations, { recursive: true });
			const journalPath = join(migrations, "meta/_journal.json");
			const journal = JSON.parse(readFileSync(journalPath, "utf8"));
			const migrationIndex = journal.entries.findIndex(
				(entry: { tag: string }) => entry.tag === targetTag,
			);
			if (migrationIndex < 0) throw new Error(`Missing ${targetTag} migration fixture`);
			const target = journal.entries[migrationIndex] as { tag: string };
			for (const entry of journal.entries.splice(migrationIndex) as Array<{ tag: string }>) {
				rmSync(join(migrations, `${entry.tag}.sql`));
			}
			writeFileSync(journalPath, JSON.stringify(journal));

			const original = join(root, "backup.db");
			const db = new Database(original);
			db.exec("PRAGMA foreign_keys=ON");
			applyPendingMigrationsByHash(db, migrations);
			await applySqliteDataBackfills(db);
			const narratorId = `narrator-${targetTag.slice(0, 4)}`;
			db.run(
				"INSERT INTO narrators (id, created_at, updated_at, traits, next_seq) VALUES (?, ?, ?, ?, ?)",
				[narratorId, "2026-09-21T00:00:00Z", "2026-09-22T00:00:00Z", '["standalone"]', 17],
			);
			const before = db
				.query("SELECT id, created_at, updated_at, traits, next_seq FROM narrators")
				.get();
			if (targetTag === "0016_high_shiva") {
				expect(
					db
						.query("SELECT name FROM sqlite_master WHERE name='idx_narrator_refs_narrator_id'")
						.get(),
				).toBeNull();
				db.run(
					"INSERT INTO narrator_messages (id, narrator_id, role, content_json, created_at) VALUES (?, ?, ?, ?, ?)",
					["message-0016", narratorId, "user", "[]", "2026-09-21T00:00:00Z"],
				);
				db.run(
					"INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq) VALUES (?, ?, ?, ?)",
					["ref-0016", narratorId, "message-0016", 7],
				);
			}
			const beforeRef = db.query("SELECT * FROM narrator_message_refs WHERE id='ref-0016'").get();
			if (targetTag === "0017_merge_opensource_083") {
				db.run(
					"INSERT INTO terminals (id, narrator_id, name, cwd, created_at) VALUES (?, ?, ?, ?, ?)",
					["terminal-0017", narratorId, "keep terminal", root, "2026-10-08T00:00:00Z"],
				);
			}
			const beforeTerminal =
				targetTag === "0017_merge_opensource_083"
					? (db.query("SELECT * FROM terminals WHERE id='terminal-0017'").get() as Record<
							string,
							unknown
						>)
					: undefined;
			db.close();

			journal.entries.push(target);
			writeFileSync(journalPath, JSON.stringify(journal));
			cpSync(join(ROOT, "drizzle", `${target.tag}.sql`), join(migrations, `${target.tag}.sql`));
			const validation = join(root, "validation.db");
			cpSync(original, validation);
			const online = join(root, "online.db");
			writeFileSync(online, "not opened");
			const originalHash = sha256(original);
			const sourceHash = "1".repeat(64);
			const commit = "a".repeat(40);
			const version = targetTag === "0017_merge_opensource_083" ? "0.8.3" : "0.7.9";
			const child = Bun.spawnSync({
				cmd: [
					process.execPath,
					join(ROOT, "scripts/validate-local-upgrade.ts"),
					"--database",
					validation,
					"--migrations",
					migrations,
					"--online-database",
					online,
					"--backup-original",
					original,
					"--backup-sha256",
					originalHash,
					"--source-sha256",
					sourceHash,
					"--commit",
					commit,
					"--version",
					version,
				],
				cwd: ROOT,
				env: { ...Bun.env, NARRAFORK_HOME: join(root, "home"), NARRAFORK_LOG_LEVEL: "error" },
				stdout: "pipe",
				stderr: "pipe",
				timeout: 300_000,
			});
			expect(child.exitCode, child.stderr.toString()).toBe(0);
			const report = JSON.parse(child.stdout.toString());
			expect(report.status).toBe("ok");
			expect(report.targetMigrations).toEqual([
				{ tag: target.tag, hash: sha256(join(ROOT, "drizzle", `${target.tag}.sql`)) },
			]);
			expect(report.backupSha256).toBe(originalHash);
			expect(report.sourceSha256).toBe(sourceHash);
			expect(report.commit).toBe(commit);
			expect(report.version).toBe(version);
			expect(report.checks).toMatchObject({
				schemaMatchesFresh: true,
				rowCountsPreserved: true,
				affectedFieldsPreserved: true,
				idempotent: true,
			});
			expect(sha256(original)).toBe(originalHash);
			const upgraded = new Database(validation, { readonly: true });
			try {
				if (targetTag === "0016_high_shiva") {
					expect(beforeRef).not.toBeNull();
					const index = (
						upgraded.query("PRAGMA index_list('narrator_message_refs')").all() as Array<{
							name: string;
							unique: number;
							partial: number;
						}>
					).find((row) => row.name === "idx_narrator_refs_narrator_id");
					expect(index).toMatchObject({
						name: "idx_narrator_refs_narrator_id",
						unique: 0,
						partial: 0,
					});
					const indexColumns = (
						upgraded.query("PRAGMA index_info('idx_narrator_refs_narrator_id')").all() as Array<{
							seqno: number;
							name: string | null;
						}>
					)
						.sort((left, right) => left.seqno - right.seqno)
						.map((row) => row.name);
					expect(indexColumns).toEqual(["narrator_id", "id"]);
					expect(
						upgraded.query("SELECT * FROM narrator_message_refs WHERE id='ref-0016'").get(),
					).toEqual(beforeRef);
				} else {
					if (targetTag === "0017_merge_opensource_083") {
						expect(
							upgraded.query("SELECT * FROM terminals WHERE id='terminal-0017'").get(),
						).toEqual({
							...beforeTerminal,
							worktree_resource_id: null,
						});
						expect(
							upgraded.query("SELECT count(*) AS count FROM narrator_worktree_resources").get(),
						).toEqual({ count: 0 });
					}
					expect(
						upgraded
							.query(
								"SELECT id, created_at, updated_at, traits, next_seq, turn_ended_at FROM narrators",
							)
							.get(),
					).toEqual({ ...before, turn_ended_at: null });
				}
			} finally {
				upgraded.close();
			}
		} finally {
			rmSync(root, { recursive: true, force: true });
		}
	}
	test(
		"validates migration 0015 without changing existing narrator data",
		() => validateTailMigration("0015_nifty_magma"),
		300_000,
	);
	test(
		"validates migration 0016 without changing existing refs",
		() => validateTailMigration("0016_high_shiva"),
		300_000,
	);
	test(
		"validates migration 0017 while preserving rebuilt tables and existing data",
		() => validateTailMigration("0017_merge_opensource_083"),
		300_000,
	);
});
