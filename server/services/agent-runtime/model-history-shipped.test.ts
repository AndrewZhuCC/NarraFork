import { Database } from "bun:sqlite";
import { expect, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { getTestDb } from "../../../tests/setup";
import { readSqliteModelHistorySignature } from "./model-history-revision";
import {
	clearModelHistorySourceCacheForTest,
	loadModelHistorySource,
} from "./model-history-source";

test("shipped SQLite migration installs a cross-connection model/body/tool revision contract", async () => {
	const directory = mkdtempSync(join(tmpdir(), "nf-shipped-history-"));
	const seed = getTestDb().sqlite;
	const path = join(directory, "fixture.db");
	await Bun.write(path, seed.serialize());
	seed.close();
	const first = new Database(path);
	const second = new Database(path);
	try {
		first.run("PRAGMA journal_mode=WAL");
		first.run(
			"INSERT INTO narrators (id,created_at,updated_at) VALUES ('owner','now','now'),('fork','now','now')",
		);
		first.run(
			`INSERT INTO narrator_messages (id,narrator_id,role,content_json,content_text,created_at) VALUES ('shared','owner','assistant','[{"type":"text","text":"original"}]','original','now')`,
		);
		first.run(
			"INSERT INTO narrator_message_refs (id,narrator_id,message_id,seq) VALUES ('owner-ref','owner','shared',0),('fork-ref','fork','shared',0)",
		);
		first.run(
			`INSERT INTO narrator_tool_calls (id,narrator_id,message_id,tool_use_id,tool_name,input_json,output_json,status,execution_attempt,created_at) VALUES ('tool','owner','shared','use','Read','{}','null','pending',1,'now')`,
		);
		expect(readSqliteModelHistorySignature(first, "fork").trusted).toBe(true);
		const backend = { backend: "sqlite" as const, sqlite: first };
		const original = await loadModelHistorySource(backend, "fork");
		expect(await loadModelHistorySource(backend, "fork")).toBe(original);
		second.run(
			`UPDATE narrator_messages SET sdk_message_uuid='final', content_json='[{"type":"text","text":"edited"}]' WHERE id='shared'`,
		);
		second.run(
			`UPDATE narrator_tool_calls SET execution_attempt=2, status='success',output_json='"result"' WHERE id='tool'`,
		);
		const edited = await loadModelHistorySource(backend, "fork");
		expect(edited).not.toBe(original);
		expect(edited.messages[0].messageUuid).toBe("final");
		expect(edited.messages[0].toolCalls[0].outputJson).toBe("result");
		expect(
			first
				.query<{ body_revision: number }, []>(
					"SELECT body_revision FROM narrator_messages WHERE id='shared'",
				)
				.get()?.body_revision,
		).toBe(2);
		expect(
			first
				.query<{ model_revision: number }, []>(
					"SELECT model_revision FROM narrator_tool_calls WHERE id='tool'",
				)
				.get()?.model_revision,
		).toBe(2);
		second.run("UPDATE narrator_messages SET tokens_in=12 WHERE id='shared'");
		expect(await loadModelHistorySource(backend, "fork")).toBe(edited);
	} finally {
		clearModelHistorySourceCacheForTest();
		first.close();
		second.close();
		rmSync(directory, { recursive: true, force: true });
	}
});
