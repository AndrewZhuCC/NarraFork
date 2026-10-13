import { Database } from "bun:sqlite";
import { describe, expect, spyOn, test } from "bun:test";
import { modelHistoryFixtureSql } from "./model-history-fixture";
import { measureModelJsonShape } from "./model-history-json";
import { queryModelHistory, sqliteHistoryQuery } from "./model-history-query";
import { freezeModelHistorySource } from "./model-history-source";

const deadline = () => Date.now() + 10_000;

describe("pre-parse graph allocation budget", () => {
	test("counts scalar slots and property slots, not punctuation within escaped strings", () => {
		const shape = measureModelJsonShape('[0,0,{"a":0,"b":"[,{}:\\"", "c":[1,2]}]', {
			deadline: deadline(),
		});
		expect(shape).toEqual({
			containers: 3,
			slots: 8,
			maxContainerSlots: 3,
			bytes: 3 * 256 + 8 * 32,
		});
		expect(measureModelJsonShape("[]", { deadline: deadline() }).slots).toBe(0);
		expect(measureModelJsonShape("{}", { deadline: deadline() }).slots).toBe(0);
	});
	test("dense primitive arrays and wide objects fail BEFORE the production loader invokes JSON.parse", async () => {
		for (const raw of [
			`[${Array(2000).fill("0").join(",")}]`,
			JSON.stringify(
				Object.fromEntries(Array.from({ length: 1000 }, (_, index) => [`k${index}`, 0])),
			),
		]) {
			const db = new Database(":memory:");
			db.run(modelHistoryFixtureSql);
			db.run("INSERT INTO narrators (id) VALUES ('owner')");
			db.query(
				"INSERT INTO narrator_messages (id,narrator_id,role,content_json) VALUES ('m','owner','assistant',?)",
			).run(raw);
			db.run(
				"INSERT INTO narrator_message_refs (id,narrator_id,message_id,seq) VALUES ('ref','owner','m',0)",
			);
			const parse = spyOn(JSON, "parse");
			try {
				await expect(
					queryModelHistory(sqliteHistoryQuery(db), {
						narratorId: "owner",
						deadline: deadline(),
						maxBytes: 48_000,
					}),
				).rejects.toThrow("128MiB budget");
				expect(parse).not.toHaveBeenCalled();
			} finally {
				parse.mockRestore();
				db.close();
			}
		}
	});
	test("single-container width fails even when wire/graph byte totals are small", () => {
		for (const raw of ["[0,0,0]", '{"a":0,"b":0,"c":0}'])
			expect(() =>
				measureModelJsonShape(raw, { deadline: deadline(), maxContainerSlots: 2 }),
			).toThrow("container width");
		const escaped = JSON.stringify({ text: ",{}[]:\\".repeat(20_000) });
		expect(
			measureModelJsonShape(escaped, { deadline: deadline(), maxContainerSlots: 2 }).slots,
		).toBe(1);
	});
	test("accepted primitive slots yield to heartbeat without Object.values giant copies", async () => {
		const graph = {
			arrays: Array.from({ length: 20 }, () => Array(8192).fill(0)),
			wide: Object.fromEntries(Array.from({ length: 12_000 }, (_, index) => [`k${index}`, 0])),
		};
		expect(
			measureModelJsonShape(JSON.stringify(graph), { deadline: deadline() }).bytes,
		).toBeLessThan(128 * 1024 * 1024);
		const values = spyOn(Object, "values").mockImplementation(() => {
			throw new Error("Object.values is forbidden while freezing");
		});
		let heartbeat = 0;
		const timer = setInterval(() => heartbeat++, 1);
		try {
			await freezeModelHistorySource(graph, deadline());
			expect(heartbeat).toBeGreaterThan(0);
			expect(Object.isFrozen(graph.arrays[0])).toBe(true);
			expect(Object.isFrozen(graph.wide)).toBe(true);
		} finally {
			clearInterval(timer);
			values.mockRestore();
		}
	});
});
