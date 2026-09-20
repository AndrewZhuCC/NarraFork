import { Database } from "bun:sqlite";
import { afterEach, expect, test } from "bun:test";
import { normalizeInvalidTraitsJson } from "./data-backfills";

let database: Database | undefined;

afterEach(() => {
	database?.close();
	database = undefined;
});

test("normalizes invalid layered traits JSON without changing valid arrays", () => {
	database = new Database(":memory:");
	for (const table of ["narrators", "projects", "user_preferences"]) {
		database.run(`CREATE TABLE ${table} (id text PRIMARY KEY, traits text)`);
	}
	database.run("INSERT INTO narrators VALUES ('n-invalid', NULL), ('n-valid', '[\"named\"]')");
	database.run("INSERT INTO projects VALUES ('p-invalid', 'not-json'), ('p-valid', '[]')");
	database.run("INSERT INTO user_preferences VALUES ('u-invalid', ''), ('u-valid', '[\"quiet\"]')");

	normalizeInvalidTraitsJson(database);
	normalizeInvalidTraitsJson(database);

	for (const table of ["narrators", "projects", "user_preferences"]) {
		expect(database.query(`SELECT traits FROM ${table} WHERE id LIKE '%-invalid'`).get()).toEqual({
			traits: "[]",
		});
	}
	expect(database.query("SELECT traits FROM narrators WHERE id = 'n-valid'").get()).toEqual({
		traits: '["named"]',
	});
	expect(database.query("SELECT traits FROM projects WHERE id = 'p-valid'").get()).toEqual({
		traits: "[]",
	});
	expect(database.query("SELECT traits FROM user_preferences WHERE id = 'u-valid'").get()).toEqual({
		traits: '["quiet"]',
	});
});
