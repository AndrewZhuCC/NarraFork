import { Database } from "bun:sqlite";
import { afterEach, describe, expect, spyOn, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { modelHistoryFixtureSql } from "./model-history-fixture";
import { queryModelHistory, sqliteHistoryQuery } from "./model-history-query";
import * as revisionModule from "./model-history-revision";
import { readSqliteModelHistorySignature } from "./model-history-revision";
import { renderModelHistoryRevisionSql } from "./model-history-revision-sql";
import {
	clearModelHistorySourceCacheForTest,
	loadModelHistorySource,
	ModelHistorySourceCache,
	markModelHistoryRows,
	modelHistorySourceStats,
	modelHistoryWorkerSpecifiers,
	verifyModelHistoryRowsCurrent,
} from "./model-history-source";

const connections: Database[] = [];
const directories: string[] = [];
function fixture(file = false) {
	const directory = file ? mkdtempSync(join(tmpdir(), "nf-model-history-")) : null;
	if (directory) directories.push(directory);
	const db = new Database(directory ? join(directory, "fixture.db") : ":memory:");
	connections.push(db);
	db.run("PRAGMA journal_mode = WAL");
	db.run(modelHistoryFixtureSql);
	db.run(renderModelHistoryRevisionSql("sqlite"));
	db.run(
		"INSERT INTO narrators (id, title) VALUES ('owner', 'Owner'), ('fork', 'Fork'), ('other', 'Other')",
	);
	db.run("INSERT INTO users (id, username) VALUES ('human', 'Alice')");
	return db;
}
function message(db: Database, id: string, seq: number, extra = "", role = "user") {
	const content = `${id}${extra}`;
	db.query(
		"INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_by) VALUES (?, 'owner', ?, ?, ?, 'human')",
	).run(id, role, JSON.stringify([{ type: "text", text: content }]), content);
	db.query(
		"INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq) VALUES (?, 'owner', ?, ?)",
	).run(`ref:${id}`, id, seq);
}
function revision(db: Database, id = "owner") {
	return readSqliteModelHistorySignature(db, id).revision;
}
afterEach(() => {
	clearModelHistorySourceCacheForTest();
	for (const db of connections.splice(0)) db.close();
	for (const directory of directories.splice(0))
		rmSync(directory, { recursive: true, force: true });
});

describe("complete bounded model source", () => {
	test("keysets >2000 refs without truncation and independently pages >200 tools", async () => {
		const db = fixture();
		db.transaction(() => {
			for (let index = 0; index < 2105; index++) message(db, `m${index}`, index);
			for (let index = 0; index < 405; index++)
				db.query(
					"INSERT INTO narrator_tool_calls (id,narrator_id,message_id,tool_use_id,tool_name,input_json,output_json,status,execution_attempt,created_at) VALUES (?,'owner','m2000',?,'Read','{}','\"ok\"','success',1,'now')",
				).run(`tool:${String(index).padStart(4, "0")}`, `use:${index}`);
		})();
		const source = await queryModelHistory(sqliteHistoryQuery(db), {
			narratorId: "owner",
			deadline: Date.now() + 10_000,
		});
		expect(source.messages).toHaveLength(2105);
		expect(source.messages[2104].id).toBe("m2104");
		expect(source.messages[2000].toolCalls).toHaveLength(405);
		expect(source.counters.maxRefsPerQuery).toBe(200);
		expect(source.counters.refQueries).toBe(12);
		expect(source.counters.toolRows).toBe(405);
	});
	test("compact/segment/fork interval and source ordering are preserved", async () => {
		const db = fixture();
		for (let index = 0; index < 205; index++) message(db, `m${index}`, index);
		db.run("UPDATE narrator_message_refs SET is_compact = 1 WHERE message_id = 'm200'");
		db.run(
			"UPDATE narrator_message_refs SET segment_compact_id = 'm200' WHERE message_id = 'm202'",
		);
		message(db, "display", 206, "", "disp");
		db.run(
			"INSERT INTO narrator_message_refs (id,narrator_id,message_id,seq,is_compact,segment_compact_id) SELECT 'fork:' || id,'fork',message_id,seq,is_compact,segment_compact_id FROM narrator_message_refs WHERE seq <= 203",
		);
		const options = { deadline: Date.now() + 10_000 };
		expect(
			(
				await queryModelHistory(sqliteHistoryQuery(db), { ...options, narratorId: "owner" })
			).messages.map((row) => row.id),
		).toEqual(["m201", "m203", "m204"]);
		expect(
			(
				await queryModelHistory(sqliteHistoryQuery(db), { ...options, narratorId: "fork" })
			).messages.map((row) => row.id),
		).toEqual(["m201", "m203"]);
	});
	test("bytes split body batches, budget fails rather than adopting partial history", async () => {
		const db = fixture();
		message(db, "large-a", 0, "x".repeat(1_300_000));
		message(db, "large-b", 1, "x".repeat(1_300_000));
		const source = await queryModelHistory(sqliteHistoryQuery(db), {
			narratorId: "owner",
			deadline: Date.now() + 10_000,
		});
		expect(source.counters.bodyQueries).toBe(2);
		expect(source.counters.maxBodyBatchBytes).toBeLessThanOrEqual(4 * 1024 * 1024);
		await expect(
			queryModelHistory(sqliteHistoryQuery(db), {
				narratorId: "owner",
				deadline: Date.now() + 10_000,
				maxBytes: 1024,
			}),
		).rejects.toThrow("128MiB budget");
		await expect(
			queryModelHistory(sqliteHistoryQuery(db), { narratorId: "owner", deadline: Date.now() - 1 }),
		).rejects.toThrow("10s budget");
	});
	test("read-only worker yields main timers, source cache stays immutable and cross-connection invalidates", async () => {
		const db = fixture(true);
		message(db, "shared", 0, "字".repeat(100_000));
		db.run(
			"INSERT INTO narrator_message_refs (id,narrator_id,message_id,seq) VALUES ('fork:shared','fork','shared',0)",
		);
		const other = new Database(db.filename);
		connections.push(other);
		let ticks = 0;
		const timer = setInterval(() => ticks++, 1);
		try {
			const backend = { backend: "sqlite" as const, sqlite: db };
			const first = await loadModelHistorySource(backend, "fork");
			expect(ticks).toBeGreaterThan(0);
			expect(Object.isFrozen(first.messages[0].contentJson)).toBe(true);
			const stats = modelHistorySourceStats();
			expect(await loadModelHistorySource(backend, "fork")).toBe(first);
			expect(modelHistorySourceStats().signatureReads).toBe(stats.signatureReads + 1);
			other.run("UPDATE narrator_messages SET sdk_message_uuid = 'final-uuid' WHERE id = 'shared'");
			const second = await loadModelHistorySource(backend, "fork");
			expect(second).not.toBe(first);
			expect(second.messages[0].messageUuid).toBe("final-uuid");
			other.run("UPDATE narrator_messages SET tokens_in = 50 WHERE id = 'shared'");
			expect(await loadModelHistorySource(backend, "fork")).toBe(second);
		} finally {
			clearInterval(timer);
		}
	});
	test("append/body/tool delta reuses immutable rows and reads only changed payloads; reorder rebuilds", async () => {
		const db = fixture(true);
		db.transaction(() => {
			for (let index = 0; index < 250; index++) message(db, `m${index}`, index, "x".repeat(1000));
			for (let index = 0; index < 10; index++)
				db.query(
					"INSERT INTO narrator_tool_calls (id,narrator_id,message_id,tool_use_id,tool_name,input_json,output_json,status,execution_attempt,created_at) VALUES (?,'owner','m200',?,'Read','{}',?,'success',1,'now')",
				).run(`tool${index}`, `use${index}`, JSON.stringify("output".repeat(7000)));
		})();
		const backend = { backend: "sqlite" as const, sqlite: db };
		const cold = await loadModelHistorySource(backend, "owner");
		message(db, "appended", 250);
		const appended = await loadModelHistorySource(backend, "owner");
		expect(appended.messages).toHaveLength(251);
		expect(appended.messages[0].contentJson).toBe(cold.messages[0].contentJson);
		expect(appended.counters.reusedBodies).toBe(250);
		expect(appended.counters.sourceBytes).toBeLessThan(cold.counters.sourceBytes / 1000);
		db.query("UPDATE narrator_tool_calls SET output_json=? WHERE id='tool3'").run(
			JSON.stringify("changed".repeat(6000)),
		);
		const toolChanged = await loadModelHistorySource(backend, "owner");
		expect(toolChanged.counters.bodyQueries).toBe(0);
		expect(toolChanged.counters.reusedBodies).toBe(251);
		expect(toolChanged.counters.reusedTools).toBe(9);
		expect(toolChanged.counters.sourceBytes).toBeLessThan(cold.counters.sourceBytes / 10);
		expect(
			toolChanged.messages[200].toolCalls.find((tool) => tool.id === "tool3")?.outputJson,
		).toBe("changed".repeat(6000));
		db.run(
			`UPDATE narrator_messages SET content_json='[{"type":"text","text":"body edit"}]' WHERE id='m10'`,
		);
		const bodyChanged = await loadModelHistorySource(backend, "owner");
		expect(bodyChanged.counters.bodyQueries).toBe(1);
		expect(bodyChanged.counters.reusedBodies).toBe(250);
		expect(bodyChanged.counters.reusedTools).toBe(10);
		db.run("UPDATE narrator_message_refs SET seq=999 WHERE message_id='m0'");
		const reordered = await loadModelHistorySource(backend, "owner");
		expect(reordered.counters.reusedBodies ?? 0).toBe(0);
		expect(reordered.messages.at(-1)?.id).toBe("m0");
	});
	test("a source revision changed after projection is rejected before request adoption", async () => {
		const db = fixture(true);
		message(db, "small", 0);
		const source = await loadModelHistorySource({ backend: "sqlite", sqlite: db }, "owner");
		const view = source.messages.map((row) => ({ ...row }));
		markModelHistoryRows(view, source);
		await verifyModelHistoryRowsCurrent(view);
		db.run("UPDATE narrator_messages SET sdk_message_uuid='late-final' WHERE id='small'");
		await expect(verifyModelHistoryRowsCurrent(view)).rejects.toThrow("request not sent");
	});
	test("an untrusted snapshot cannot be promoted when triggers return between read and signing", async () => {
		const db = fixture(true);
		message(db, "shared", 0);
		db.run(
			"INSERT INTO narrator_tool_calls (id,narrator_id,message_id,tool_use_id,tool_name,input_json,output_json,status,created_at) VALUES ('tool','owner','shared','use','Read','{}','\"old\"','success','now')",
		);
		db.run("DROP TRIGGER model_history_v1_tool_update");
		const realRead = revisionModule.readSqliteModelHistorySignature;
		let reads = 0;
		const barrier = spyOn(revisionModule, "readSqliteModelHistorySignature").mockImplementation(
			(connection, id) => {
				if (++reads === 2) {
					// The worker already returned old rows. A second connection changes them while
					// coverage is disabled, then restores catalog trust without a body/tool bump.
					const writer = new Database(db.filename);
					try {
						writer.run(`UPDATE narrator_tool_calls SET output_json='"new"' WHERE id='tool'`);
						const trigger = renderModelHistoryRevisionSql("sqlite")
							.split("--> statement-breakpoint")
							.find((statement) =>
								statement.trim().startsWith("CREATE TRIGGER model_history_v1_tool_update"),
							);
						if (!trigger) throw new Error("missing fixture trigger");
						writer.run(trigger);
					} finally {
						writer.close();
					}
				}
				return realRead(connection, id);
			},
		);
		try {
			await expect(
				loadModelHistorySource({ backend: "sqlite", sqlite: db }, "owner"),
			).rejects.toThrow("trust changed");
			expect(modelHistorySourceStats().cacheEntries).toBe(0);
			expect(reads).toBe(2);
		} finally {
			barrier.mockRestore();
		}
	});
	test("JSON object overhead is rejected before parsing an oversized graph", async () => {
		const db = fixture(true);
		message(db, "graph", 0);
		const inner = `[${Array(6000).fill("{}").join(",")}]`;
		db.query("UPDATE narrator_messages SET content_json=? WHERE id='graph'").run(
			`[${Array(100).fill(inner).join(",")}]`,
		);
		await expect(
			loadModelHistorySource({ backend: "sqlite", sqlite: db }, "owner"),
		).rejects.toThrow("128MiB budget");
		expect(modelHistorySourceStats().cacheEntries).toBe(0);
	});
	test("cancelled and unversioned sources never enter trusted cache", async () => {
		const db = fixture(true);
		message(db, "small", 0);
		const backend = { backend: "sqlite" as const, sqlite: db };
		await expect(
			loadModelHistorySource(backend, "owner", { signal: AbortSignal.abort() }),
		).rejects.toThrow();
		const controller = new AbortController();
		const pending = loadModelHistorySource(backend, "owner", { signal: controller.signal });
		const cancel = setTimeout(() => controller.abort(), 0);
		await expect(pending).rejects.toThrow("cancelled");
		clearTimeout(cancel);
		db.run("DROP TRIGGER model_history_v1_tool_update");
		expect(readSqliteModelHistorySignature(db, "owner").trusted).toBe(false);
		const first = await loadModelHistorySource(backend, "owner");
		expect(await loadModelHistorySource(backend, "owner")).not.toBe(first);
		expect(modelHistorySourceStats().cacheEntries).toBe(0);
	});
});

describe("hot source signature lifecycle", () => {
	let reload = 0;
	const reevaluate = (): Promise<typeof import("./model-history-source")> =>
		import(`./model-history-source.ts?reload=${++reload}`);
	test.each([
		`UPDATE narrator_messages SET content_json='[{"type":"text","text":"late-body"}]' WHERE id='shared'`,
		`UPDATE narrator_tool_calls SET output_json='"late-tool"' WHERE id='tool'`,
	])("a real reevaluation preserves warm-cache and projected-row guards: %s", async (mutation) => {
		const db = fixture(true);
		message(db, "shared", 0);
		db.run(
			`INSERT INTO narrator_tool_calls (id,narrator_id,message_id,tool_use_id,tool_name,input_json,output_json,status,created_at) VALUES ('tool','owner','shared','use','Read','{}','"old"','success','now')`,
		);
		const backend = { backend: "sqlite" as const, sqlite: db };
		const source = await loadModelHistorySource(backend, "owner");
		const earlierRows = source.messages.map((row) => ({ ...row }));
		markModelHistoryRows(earlierRows, source);
		const loadedBefore = modelHistorySourceStats().loads;
		const reloaded = await reevaluate();
		const warm = await reloaded.loadModelHistorySource(backend, "owner");
		expect(warm).toBe(source);
		expect(reloaded.modelHistorySourceStats().loads).toBe(loadedBefore);
		const rows = warm.messages.map((row) => ({ ...row }));
		reloaded.markModelHistoryRows(rows, warm);
		await reloaded.verifyModelHistoryRowsCurrent(rows);
		db.run(mutation);
		await expect(reloaded.verifyModelHistoryRowsCurrent(rows)).rejects.toThrow("request not sent");
		await expect(reloaded.verifyModelHistoryRowsCurrent(earlierRows)).rejects.toThrow(
			"request not sent",
		);
	});
	test("legacy cache without metadata cold-reads and never stamps unknown stale source", async () => {
		const db = fixture(true);
		message(db, "shared", 0);
		const backend = { backend: "sqlite" as const, sqlite: db };
		const source = await loadModelHistorySource(backend, "owner");
		const unknown = {
			...source,
			messages: source.messages.map((row) => ({
				...row,
				contentJson: [{ type: "text", text: "stale legacy body" }],
			})),
		};
		const state = (
			globalThis as unknown as Record<
				symbol,
				{
					cache: ModelHistorySourceCache;
					sourceSignatures?: unknown;
					rowSignatures?: unknown;
				}
			>
		)[Symbol.for("narrator.modelHistorySource.v1")];
		delete state.sourceSignatures;
		delete state.rowSignatures;
		const signature = readSqliteModelHistorySignature(db, "owner");
		state.cache.set(
			`${db.filename}\u0000owner\u0000${signature.catalogIdentity}:${signature.revision}`,
			unknown,
			1000,
		);
		const reloaded = await reevaluate();
		const before = reloaded.modelHistorySourceStats();
		const current = await reloaded.loadModelHistorySource(backend, "owner");
		expect(current).not.toBe(unknown);
		expect(current.messages[0].contentJson).toEqual([{ type: "text", text: "shared" }]);
		expect(current.counters.reusedBodies ?? 0).toBe(0);
		expect(reloaded.modelHistorySourceStats().loads).toBe(before.loads + 1);
		expect(reloaded.modelHistorySourceStats().signatureReads).toBe(before.signatureReads + 2);
		expect(() => reloaded.markModelHistoryRows([], unknown)).toThrow(/signature/i);
		const rows = current.messages.map((row) => ({ ...row }));
		reloaded.markModelHistoryRows(rows, current);
		db.run(
			`UPDATE narrator_messages SET content_json='[{"type":"text","text":"changed"}]' WHERE id='shared'`,
		);
		await expect(reloaded.verifyModelHistoryRowsCurrent(rows)).rejects.toThrow("request not sent");
	});
	test("missing row provenance rejects, explicit unversioned rows retain compatibility but obey cancellation", async () => {
		await expect(verifyModelHistoryRowsCurrent([])).rejects.toThrow(/signature/i);
		const db = fixture(true);
		message(db, "shared", 0);
		db.run("DROP TRIGGER model_history_v1_tool_update");
		const source = await loadModelHistorySource({ backend: "sqlite", sqlite: db }, "owner");
		const rows = source.messages.map((row) => ({ ...row }));
		markModelHistoryRows(rows, source);
		await verifyModelHistoryRowsCurrent(rows);
		expect(modelHistorySourceStats().cacheEntries).toBe(0);
		await expect(
			verifyModelHistoryRowsCurrent(rows, { signal: AbortSignal.abort() }),
		).rejects.toThrow();
		await expect(verifyModelHistoryRowsCurrent(rows, { deadline: Date.now() - 1 })).rejects.toThrow(
			"10s budget",
		);
	});
	test("reloaded warm hits preserve caller cancellation and absolute deadline", async () => {
		const db = fixture(true);
		message(db, "shared", 0);
		const backend = { backend: "sqlite" as const, sqlite: db };
		const source = await loadModelHistorySource(backend, "owner");
		const reloaded = await reevaluate();
		await expect(
			reloaded.loadModelHistorySource(backend, "owner", { signal: AbortSignal.abort() }),
		).rejects.toThrow();
		await expect(
			reloaded.loadModelHistorySource(backend, "owner", { deadline: Date.now() - 1 }),
		).rejects.toThrow("10s budget");
		const rows = source.messages.map((row) => ({ ...row }));
		reloaded.markModelHistoryRows(rows, source);
		await expect(
			reloaded.verifyModelHistoryRowsCurrent(rows, { signal: AbortSignal.abort() }),
		).rejects.toThrow();
		await expect(
			reloaded.verifyModelHistoryRowsCurrent(rows, { deadline: Date.now() - 1 }),
		).rejects.toThrow("10s budget");
	});
});

test("source LRU enforces 16MiB/session, 64MiB/global, 8 entries and 60s idle", () => {
	let now = 0;
	const cache = new ModelHistorySourceCache(() => now);
	const source: Awaited<ReturnType<typeof queryModelHistory>> = {
		messages: [],
		counters: {
			refQueries: 0,
			bodyQueries: 0,
			toolQueries: 0,
			refs: 0,
			toolRows: 0,
			sourceBytes: 0,
			maxRefsPerQuery: 0,
			maxBodyBatchBytes: 0,
			maxToolBatchBytes: 0,
		},
	};
	cache.set("session\u00001", source, 16 * 1024 * 1024 + 1);
	expect(cache.size).toBe(0);
	for (let index = 0; index < 10; index++) cache.set(`s${index}\u00001`, source, 8 * 1024 * 1024);
	expect(cache.size).toBe(8);
	expect(cache.retainedBytes).toBe(64 * 1024 * 1024);
	cache.set("s9\u00002", source, 8 * 1024 * 1024);
	expect(cache.get("s9\u00001")).toBeUndefined();
	now = 60_000;
	cache.prune();
	expect(cache.size).toBe(0);
	expect(cache.retainedBytes).toBe(0);
});

test("transactional revisions cover shared bodies, final UUID, reorder/compact/segment, tool attempts and authors; not usage", () => {
	const db = fixture();
	message(db, "shared", 0);
	db.run(
		"INSERT INTO narrator_message_refs (id,narrator_id,message_id,seq) VALUES ('fork:shared','fork','shared',0)",
	);
	db.run(
		"INSERT INTO narrator_tool_calls (id,narrator_id,message_id,tool_use_id,tool_name,input_json,output_json,status,execution_attempt,created_at) VALUES ('tool','owner','shared','use','Read','{}','null','pending',1,'now')",
	);
	for (const mutation of [
		'UPDATE narrator_messages SET content_json = \'[{"type":"text","text":"edited"}]\' WHERE id = \'shared\'',
		"UPDATE narrator_messages SET sdk_message_uuid = 'final' WHERE id = 'shared'",
		"UPDATE narrator_tool_calls SET input_json = '{\"path\":\"new\"}', status = 'running', execution_attempt = 2 WHERE id = 'tool'",
		"UPDATE narrator_tool_calls SET output_json = '\"result\"', status = 'success' WHERE id = 'tool'",
		"UPDATE users SET username = 'Bob' WHERE id = 'human'",
		"UPDATE narrators SET title = 'New owner' WHERE id = 'owner'",
	]) {
		const before = [revision(db), revision(db, "fork")];
		db.run(mutation);
		expect(Number(revision(db))).toBeGreaterThan(Number(before[0]));
		expect(Number(revision(db, "fork"))).toBeGreaterThan(Number(before[1]));
	}
	for (const mutation of ["seq = 2", "is_compact = 1", "segment_compact_id = 'compact'"]) {
		const before = revision(db, "fork");
		db.run(`UPDATE narrator_message_refs SET ${mutation} WHERE id = 'fork:shared'`);
		expect(revision(db, "fork")).not.toBe(before);
	}
	const before = revision(db);
	db.run("UPDATE narrator_messages SET tokens_in = 123 WHERE id = 'shared'");
	db.run("UPDATE narrator_tool_calls SET input_tokens = 234 WHERE id = 'tool'");
	expect(revision(db)).toBe(before);
	const body = db
		.query<{ body_revision: number }, []>(
			"SELECT body_revision FROM narrator_messages WHERE id='shared'",
		)
		.get();
	expect(body?.body_revision).toBe(3);
	const original = revision(db);
	try {
		db.transaction(() => {
			db.run("UPDATE narrator_messages SET sdk_message_uuid='rollback' WHERE id='shared'");
			throw new Error("rollback");
		})();
	} catch {}
	expect(revision(db)).toBe(original);
});

test("compiled runtimes probe every worker path shape, source runtimes resolve the development path", () => {
	expect(modelHistoryWorkerSpecifiers(true, "file:///$bunfs/root/narrafork.exe")).toEqual([
		"file:///$bunfs/root/services/agent-runtime/model-history-worker.js",
		"file:///$bunfs/root/server/services/agent-runtime/model-history-worker.js",
		"file:///$bunfs/root/model-history-worker.js",
	]);
	expect(
		modelHistoryWorkerSpecifiers(
			false,
			"file:///repo/server/services/agent-runtime/model-history-source.ts",
		),
	).toEqual(["file:///repo/server/services/agent-runtime/model-history-worker.ts"]);
});
