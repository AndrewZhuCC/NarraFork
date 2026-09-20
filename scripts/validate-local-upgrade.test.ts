import { Database } from "bun:sqlite";
import { describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { applySqliteDataBackfills } from "../server/db/data-backfills";
import { ensureColumns } from "../server/db/ensure-columns";
import { applyPendingMigrationsByHash } from "../server/db/run-migrations";

const ROOT = resolve(import.meta.dir, "..");

function sha256(path: string): string {
	return createHash("sha256").update(readFileSync(path)).digest("hex");
}

describe("validate-local-upgrade", () => {
	test("validates migration 0012 on an isolated fixture copy", async () => {
		const root = mkdtempSync(join(tmpdir(), "nf-local-upgrade-validator-"));
		try {
			const preMigrations = join(root, "drizzle-pre-0012");
			cpSync(join(ROOT, "drizzle"), preMigrations, { recursive: true });
			const journalPath = join(preMigrations, "meta/_journal.json");
			const journal = JSON.parse(readFileSync(journalPath, "utf8"));
			journal.entries = journal.entries.filter(
				(entry: { tag: string }) => entry.tag !== "0012_curved_bloodstorm",
			);
			writeFileSync(journalPath, JSON.stringify(journal));
			rmSync(join(preMigrations, "0012_curved_bloodstorm.sql"));

			const original = join(root, "backup.db");
			const db = new Database(original);
			applyPendingMigrationsByHash(db, preMigrations);
			ensureColumns(db);
			await applySqliteDataBackfills(db);
			db.run(
				"INSERT INTO narrators (id, created_at, updated_at, traits, next_seq) VALUES (?, ?, ?, ?, ?)",
				["narrator-fixture", "2026-09-20T00:00:00Z", "2026-09-20T00:00:00Z", '["standalone"]', 0],
			);
			db.run(
				"INSERT INTO narrator_messages (id, narrator_id, role, content_json, created_at) VALUES (?, ?, ?, ?, ?)",
				["message-fixture", "narrator-fixture", "user", "[]", "2026-09-20T00:00:00Z"],
			);
			db.run(
				"INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq) VALUES (?, ?, ?, ?)",
				["ref-fixture", "narrator-fixture", "message-fixture", 7],
			);
			db.exec(
				"ALTER TABLE narrators DROP COLUMN next_seq; " +
					"ALTER TABLE narrator_tool_calls DROP COLUMN stream_completed_at",
			);
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
					"0.7.5",
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
					tag: "0012_curved_bloodstorm",
					hash: sha256(join(ROOT, "drizzle/0012_curved_bloodstorm.sql")),
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
				const row = validated
					.query("SELECT next_seq FROM narrators WHERE id = 'narrator-fixture'")
					.get() as { next_seq: number };
				expect(row.next_seq).toBe(8);
			} finally {
				validated.close();
			}
		} finally {
			rmSync(root, { recursive: true, force: true });
		}
	}, 300_000);
});
