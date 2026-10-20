import { Database } from "bun:sqlite";
import { afterEach, expect, test } from "bun:test";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { importLegacyProjectOnWorker } from "../legacy-import-job";
import { exportLegacyProjectOnWorker } from "../legacy-sync-job";
import { ARCHIVE_COLUMNS, ARCHIVE_TABLE_ORDER } from "../manifest";

const dirs: string[] = [];
const handles: Database[] = [];
afterEach(async () => {
	for (const handle of handles.splice(0)) handle.close();
	for (const dir of dirs.splice(0)) await rm(dir, { recursive: true, force: true });
});
function database(path: string) {
	const handle = new Database(path, { create: true });
	handles.push(handle);
	handle.run(
		"CREATE TABLE users(id TEXT PRIMARY KEY,role TEXT); INSERT INTO users VALUES('owner','user')",
	);
	for (const table of ARCHIVE_TABLE_ORDER) {
		const fields = new Set([
			...ARCHIVE_COLUMNS[table],
			...(table === "narrators" ? ["owner_user_id", "acl_root_narrator_id", "next_seq"] : []),
		]);
		handle.run(
			`CREATE TABLE ${table} (${[...fields].map((name) => `${name} ${name === "id" ? "TEXT PRIMARY KEY" : name === "seq" ? "INTEGER" : "TEXT"}`).join(",")})`,
		);
	}
	return handle;
}
async function fixture() {
	const root = await mkdtemp(join(tmpdir(), "nf-portable-registration-"));
	dirs.push(root);
	const sourcePath = join(root, "source.sqlite");
	const archivePath = join(root, "archive.sqlite");
	const targetPath = join(root, "target.sqlite");
	const source = database(sourcePath),
		archive = database(archivePath),
		target = database(targetPath);
	source.run(
		"INSERT INTO projects(id,name,git_path) VALUES('project','Registration test','/source'); INSERT INTO narrators(id,type,owner_user_id,context_project_id) VALUES('session','primary','owner','project'),('controller','primary','owner',NULL),('source','primary','owner',NULL),('recipient','primary','owner',NULL); UPDATE narrators SET policy_source_narrator_id='source' WHERE id='session'",
	);
	const registration = {
		version: 1,
		modelPool: "general",
		controller: { narratorId: "controller", actions: ["steer", "stop"] },
		run: {
			logicalRunId: "old-logical-run",
			principal: { userId: "old-user" },
			cancelWith: { narratorId: "recipient", toolCallId: "old-call", attempt: 7 },
			deliverTo: {
				narratorId: "recipient",
				toolUseId: "old-use",
				toolCallId: "old-call",
				attempt: 7,
			},
			deadlineAt: "2026-10-01T00:00:00.000Z",
			historyStartSeq: 1,
		},
	};
	source.run("UPDATE narrators SET runtime_registration_json=? WHERE id='session'", [
		JSON.stringify(registration),
	]);
	const actor = { userId: "owner", isAdmin: false };
	const sync = () =>
		exportLegacyProjectOnWorker({
			databasePath: sourcePath,
			archivePath,
			backend: "sqlite",
			projectId: "project",
			actor,
			deadline: Date.now() + 10_000,
		});
	const restore = () =>
		importLegacyProjectOnWorker({
			databasePath: targetPath,
			archivePath,
			backend: "sqlite",
			gitPath: "/target",
			actor,
			deadline: Date.now() + 10_000,
		});
	return { source, archive, target, sync, restore };
}
function registration(handle: Database) {
	const row = handle
		.prepare(
			"SELECT runtime_registration_json,policy_source_narrator_id FROM narrators WHERE id='session'",
		)
		.get() as { runtime_registration_json: string; policy_source_narrator_id: string };
	return { ...row, value: JSON.parse(row.runtime_registration_json) };
}
test("real archive round trip keeps distinct controller/source and historical placement without reviving recipient/run authority", async () => {
	const { source, archive, target, sync, restore } = await fixture();
	const before = registration(source).runtime_registration_json;
	await sync();
	const expected = {
		version: 1,
		modelPool: "general",
		controller: { narratorId: "controller", actions: ["steer", "stop"] },
		historyPlacement: { toolUseId: "old-use" },
	};
	expect(registration(archive).value).toEqual(expected);
	expect(archive.prepare("SELECT id FROM narrators ORDER BY id").all()).toEqual([
		{ id: "controller" },
		{ id: "session" },
		{ id: "source" },
	]);
	await restore();
	expect(registration(target).value).toEqual(expected);
	expect(registration(target).policy_source_narrator_id).toBe("source");
	expect(registration(target).value.run).toBeUndefined();
	expect(registration(source).runtime_registration_json).toBe(before);
});
test("dangling portable controller rejects atomically even if the target could supply it", async () => {
	const { archive, target, sync, restore } = await fixture();
	await sync();
	archive.run("UPDATE narrators SET runtime_registration_json=? WHERE id='session'", [
		JSON.stringify({ version: 1, controller: { narratorId: "outside", actions: ["stop"] } }),
	]);
	target.run("INSERT INTO narrators(id,type,owner_user_id) VALUES('outside','primary','owner')");
	await expect(restore()).rejects.toThrow("validation or operation failed");
	expect(target.prepare("SELECT id FROM projects").all()).toEqual([]);
	expect(target.prepare("SELECT id FROM narrators ORDER BY id").all()).toEqual([{ id: "outside" }]);
});
for (const invalid of [
	"{broken",
	JSON.stringify({ version: 1, run: { logicalRunId: "bad", principal: { userId: 42 } } }),
	JSON.stringify({ version: 1, unknown: true }),
	` ${" ".repeat(64 * 1024)}{"version":1}`,
]) {
	test(`invalid registration rejects before any import writes (${invalid.length} bytes)`, async () => {
		const { archive, target, sync, restore } = await fixture();
		await sync();
		archive.run("UPDATE narrators SET runtime_registration_json=? WHERE id='session'", [invalid]);
		await expect(restore()).rejects.toThrow("validation or operation failed");
		expect(target.prepare("SELECT id FROM projects").all()).toEqual([]);
		expect(target.prepare("SELECT id FROM narrators").all()).toEqual([]);
	});
}
test("invalid source registration refuses export without replacing the prior archive", async () => {
	const { source, archive, sync } = await fixture();
	await sync();
	const before = archive.serialize();
	source.run("UPDATE narrators SET runtime_registration_json='{}' WHERE id='session'");
	await expect(sync()).rejects.toThrow("validation or operation failed");
	expect(archive.serialize()).toEqual(before);
});
