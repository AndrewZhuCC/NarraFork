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
			const preMigrations = join(root, "drizzle-pre-0013");
			cpSync(join(ROOT, "drizzle"), preMigrations, { recursive: true });
			const journalPath = join(preMigrations, "meta/_journal.json");
			const journal = JSON.parse(readFileSync(journalPath, "utf8"));
			journal.entries = journal.entries.filter(
				(entry: { tag: string }) => entry.tag !== "0013_omniscient_dormammu",
			);
			writeFileSync(journalPath, JSON.stringify(journal));
			rmSync(join(preMigrations, "0013_omniscient_dormammu.sql"));

			const original = join(root, "backup.db");
			const db = new Database(original);
			db.exec("PRAGMA foreign_keys=ON");
			applyPendingMigrationsByHash(db, preMigrations);
			await applySqliteDataBackfills(db);
			db.run(
				"INSERT INTO narrators (id, created_at, updated_at, traits, next_seq) VALUES (?, ?, ?, ?, ?)",
				[
					"narrator-fixture",
					"2026-09-21T00:00:00Z",
					"2026-09-21T00:00:00Z",
					'["standalone"]',
					0,
				],
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
				(db.query("PRAGMA table_info(file_change_scope_recoveries)").all() as Array<{
					name: string;
				}>).some((column) => column.name === "workspace_lease_id"),
			).toBe(false);
			db.close();

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
					join(ROOT, "drizzle"),
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
				expect(narrator.next_seq).toBe(8);
				const recovery = validated
					.query(
						"SELECT workspace_lease_id FROM file_change_scope_recoveries WHERE id = 'recovery-fixture'",
					)
					.get() as { workspace_lease_id: string | null };
				expect(recovery.workspace_lease_id).toBeNull();
				const leases = validated.query("SELECT count(*) AS count FROM workspace_write_leases").get() as {
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
});
