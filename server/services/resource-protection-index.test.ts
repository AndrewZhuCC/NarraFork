import { Database } from "bun:sqlite";
import { afterEach, beforeEach, describe, expect, spyOn, test } from "bun:test";
import * as fs from "node:fs/promises";
import { chmod, mkdir, mkdtemp, rename, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { installResourceProtectionTriggers } from "../db/resource-protection-index";
import type { WorktreeJournalRecord } from "./narrator-worktree-journal";
import { createResourceProtectionIndex } from "./resource-protection-index";
import type { LifecycleTarget, ResourceClaim } from "./worktree-lifecycle-guard";

let database: Database;
let root: string;
let receipts: string;
let service: ReturnType<typeof createResourceProtectionIndex>;
const deadline = () => performance.now() + 15_000;
function changes() {
	return (database.query("SELECT total_changes() AS n").get() as { n: number }).n;
}
async function claims(
	targets: LifecycleTarget[],
	includeLegacyReceipts = true,
): Promise<ResourceClaim[]> {
	const result = await service.read(targets, deadline(), undefined, includeLegacyReceipts);
	if (!result.complete) throw new Error("incomplete");
	const resultClaims: ResourceClaim[] = [];
	for await (const claim of result.claims) resultClaims.push(claim);
	return resultClaims;
}
async function bootstrap() {
	for (let attempt = 0; attempt < 1000; attempt++) {
		await service.maintain(deadline());
		const pending = database
			.query(
				"SELECT 1 FROM resource_protection_sources WHERE (source_id = '' AND complete = 0) OR dirty = 1 LIMIT 1",
			)
			.get();
		if (!pending) return;
	}
	throw new Error("Bootstrap never completed");
}
function narrator(id: string, path: string, context?: object) {
	database
		.query("INSERT INTO narrators (id, cwd, workspace_context) VALUES (?, ?, ?)")
		.run(id, path, context ? JSON.stringify(context) : null);
}
function registry(id: string, path: string, project: string | null = null, device = "local") {
	database
		.query(`INSERT INTO narrator_worktree_resources
		(id, device_id, worktree_path, repository_key, scope_project_id, state) VALUES (?, ?, ?, ?, ?, 'ready')`)
		.run(id, device, path, `repo-${id}`, project);
}
function receipt(destination: string): WorktreeJournalRecord {
	return { destination, repositoryKey: "receipt-repository" } as WorktreeJournalRecord;
}

beforeEach(async () => {
	root = await mkdtemp(join(tmpdir(), "nf-protection-index-"));
	receipts = join(root, "worktree-requests");
	database = new Database(":memory:");
	database.exec(`
		CREATE TABLE narrators(id TEXT PRIMARY KEY, cwd TEXT, chapter_id TEXT, default_device_id TEXT,
			workspace_context TEXT, workspace_revision INTEGER NOT NULL DEFAULT 0, title TEXT);
		CREATE TABLE chapters(id TEXT PRIMARY KEY, worktree_path TEXT, snapshot_shadow_key TEXT, title TEXT);
		CREATE TABLE narrator_worktree_resources(id TEXT PRIMARY KEY, device_id TEXT NOT NULL,
			worktree_path TEXT NOT NULL, repository_key TEXT NOT NULL, scope_project_id TEXT, state TEXT, owner_narrator_id TEXT);
		CREATE TABLE resource_protection_sources(source TEXT NOT NULL, source_id TEXT NOT NULL,
			revision INTEGER NOT NULL DEFAULT 1, dirty INTEGER NOT NULL DEFAULT 1, cursor TEXT,
			complete INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(source, source_id));
		CREATE INDEX idx_protection_dirty ON resource_protection_sources(dirty, source, source_id);
		CREATE TABLE resource_protection_paths(source TEXT NOT NULL, source_id TEXT NOT NULL, slot INTEGER NOT NULL,
			device_id TEXT NOT NULL, canonical_path TEXT, repository_key TEXT, shadow_key TEXT, scope_project_id TEXT,
			alias_sensitive INTEGER NOT NULL DEFAULT 0, claim_json TEXT NOT NULL, PRIMARY KEY(source, source_id, slot));
		CREATE INDEX idx_protection_path ON resource_protection_paths(device_id, canonical_path, source, source_id, slot);
		CREATE INDEX idx_protection_repository ON resource_protection_paths(device_id, repository_key, source, source_id, slot);
		CREATE INDEX idx_protection_shadow ON resource_protection_paths(shadow_key, source, source_id, slot);
		CREATE INDEX idx_protection_scope ON resource_protection_paths(scope_project_id, source, source_id, slot);
		CREATE INDEX idx_protection_alias ON resource_protection_paths(alias_sensitive, source, source_id, slot);
	`);
	installResourceProtectionTriggers(database);
	service = createResourceProtectionIndex(database, receipts);
});
afterEach(async () => {
	database.close();
	await rm(root, { recursive: true, force: true });
});

describe("resource protection reverse index", () => {
	test("small fresh inventory is proved in memory with zero writes", async () => {
		narrator("n", root);
		const before = changes();
		expect((await claims([{ path: root }], false)).some((claim) => claim.id === "n")).toBe(true);
		expect(changes()).toBe(before);
		expect(
			database
				.query(
					"SELECT complete FROM resource_protection_sources WHERE source='narrator' AND source_id=''",
				)
				.get(),
		).toEqual({ complete: 0 });
	});
	test("trigger actual-field changes, raw SQL delete and rollback stay transactionally correct", async () => {
		narrator("n", root);
		await bootstrap();
		const before = database
			.query(
				"SELECT revision, dirty FROM resource_protection_sources WHERE source='narrator' AND source_id='n'",
			)
			.get();
		database.query("UPDATE narrators SET title = 'unrelated', cwd = cwd WHERE id = 'n'").run();
		expect(
			database
				.query(
					"SELECT revision, dirty FROM resource_protection_sources WHERE source='narrator' AND source_id='n'",
				)
				.get(),
		).toEqual(before);
		database.exec("BEGIN");
		database.query("UPDATE narrators SET cwd = ? WHERE id = 'n'").run(join(root, "new"));
		database.exec("ROLLBACK");
		expect(
			database
				.query(
					"SELECT revision, dirty FROM resource_protection_sources WHERE source='narrator' AND source_id='n'",
				)
				.get(),
		).toEqual(before);
		database.query("UPDATE narrators SET cwd = ? WHERE id = 'n'").run(join(root, "new"));
		const total = changes();
		expect((await claims([{ path: join(root, "new") }])).some((claim) => claim.id === "n")).toBe(
			true,
		);
		expect(changes()).toBe(total);
		database.query("DELETE FROM narrators WHERE id = 'n'").run();
		expect(await claims([{ path: root }])).toEqual([]);
	});
	test("reinstallation replaces stale trigger definitions without resetting source state", async () => {
		narrator("n", root);
		await bootstrap();
		const before = database
			.query(`SELECT revision, dirty FROM resource_protection_sources
			WHERE source = 'narrator' AND source_id = 'n'`)
			.get();
		database.exec(`DROP TRIGGER resource_protection_narrator_update;
			CREATE TRIGGER resource_protection_narrator_update AFTER UPDATE ON narrators
			BEGIN SELECT 1; END;`);
		installResourceProtectionTriggers(database);
		expect(
			database
				.query(`SELECT revision, dirty FROM resource_protection_sources
			WHERE source = 'narrator' AND source_id = 'n'`)
				.get(),
		).toEqual(before);
		database.query("UPDATE narrators SET cwd = ? WHERE id = 'n'").run(join(root, "new"));
		expect((await claims([{ path: join(root, "new") }])).some((claim) => claim.id === "n")).toBe(
			true,
		);
		expect(
			database
				.query<{ dirty: number }, []>(`SELECT dirty FROM resource_protection_sources
			WHERE source = 'narrator' AND source_id = 'n'`)
				.get()?.dirty,
		).toBe(1);
	});

	test("scope is independent of device; owner deletion does not delete registry evidence", async () => {
		registry("r", "/remote/path", "project", "remote-device");
		database
			.query("UPDATE narrator_worktree_resources SET owner_narrator_id = NULL WHERE id = 'r'")
			.run();
		await bootstrap();
		expect(
			(await claims([{ scopeProjectId: "project", deviceId: "local" }])).map((claim) => claim.id),
		).toContain("r");
		expect(await claims([{ path: root }])).toEqual([]);
	});
	test("legacy device/chapter/revision and shadow-only claims remain intact", async () => {
		narrator("n", root, { cwd: root, deviceId: "local", git: { repositoryKey: "rk" } });
		database.query("UPDATE narrators SET chapter_id='chapter' WHERE id='n'").run();
		database
			.query("INSERT INTO chapters(id, snapshot_shadow_key) VALUES ('chapter', ?)")
			.run(`local\0${root}`);
		await bootstrap();
		const found = await claims([{ path: root }]);
		expect(
			found.some(
				(claim) => claim.kind === "narrator" && claim.legacyChapterCwd && claim.revision === 0,
			),
		).toBe(true);
		expect(
			found.some((claim) => claim.kind === "chapter" && claim.shadowKey === `local\0${root}`),
		).toBe(true);
		expect((await claims([{ repositoryKey: "rk" }])).some((claim) => claim.id === "n")).toBe(true);
	});
	test("warm known aliases are revalidated without maintenance writes", async () => {
		const one = join(root, "one"),
			two = join(root, "two"),
			alias = join(root, "alias");
		await mkdir(one);
		await mkdir(two);
		await symlink(one, alias);
		narrator("alias", alias);
		await bootstrap();
		await rm(alias);
		await symlink(two, alias);
		const before = changes();
		expect((await claims([{ path: two }])).some((claim) => claim.id === "alias")).toBe(true);
		expect(changes()).toBe(before);
	});
	test("revision changes during canonicalization cannot publish stale clean rows", async () => {
		narrator("n", root);
		await bootstrap();
		database.query("UPDATE narrators SET cwd = ? WHERE id = 'n'").run(join(root, "intermediate"));
		const maintaining = service.maintain(deadline());
		database.query("UPDATE narrators SET cwd = ? WHERE id = 'n'").run(join(root, "new"));
		await maintaining;
		expect(
			database
				.query(
					"SELECT dirty FROM resource_protection_sources WHERE source='narrator' AND source_id='n'",
				)
				.get(),
		).toEqual({ dirty: 1 });
		expect(
			(await claims([{ path: join(root, "new") }])).some(
				(claim) => claim.path === join(root, "new"),
			),
		).toBe(true);
	});
	test("concurrent evidence mutation rejects final clear and expired/cancelled reads reject", async () => {
		await bootstrap();
		const result = await service.read([{ path: root }], deadline());
		narrator("later", root);
		await expect(
			(async () => {
				for await (const _claim of result.claims) {
					/* exhaust proof */
				}
			})(),
		).rejects.toThrow("changed during inspection");
		await expect(service.read([{ path: root }], performance.now() - 1)).rejects.toThrow();
		await expect(service.read([{ path: root }], deadline(), AbortSignal.abort())).rejects.toThrow();
	});
	test("unrelated writes do not invalidate clear; index publication does", async () => {
		narrator("n", root);
		await bootstrap();
		const unrelated = [{ path: join(root, "..", "unrelated") }];
		const result = await service.read(unrelated, deadline());
		database.query("UPDATE narrators SET title='chat only' WHERE id='n'").run();
		const found: ResourceClaim[] = [];
		for await (const claim of result.claims) found.push(claim);
		expect(found).toEqual([]);
		database.query("UPDATE narrators SET cwd=? WHERE id='n'").run(join(root, "new"));
		const publishing = await service.read(unrelated, deadline());
		await service.maintain(deadline());
		await expect(
			(async () => {
				for await (const _claim of publishing.claims) {
					/* exhaust proof */
				}
			})(),
		).rejects.toThrow("changed during inspection");
	});
	test("idle maintenance performs no writes and missing ordinary paths are not volatile aliases", async () => {
		for (let i = 0; i < 1100; i++) narrator(`missing-${i}`, join(root, `absent-${i}`));
		await bootstrap();
		expect(
			database
				.query("SELECT count(*) AS n FROM resource_protection_paths WHERE alias_sensitive=1")
				.get(),
		).toEqual({ n: 0 });
		const before = changes();
		await service.maintain(deadline());
		expect(changes()).toBe(before);
		expect(await claims([{ path: join(root, "unrelated") }])).toEqual([]);
	});
	test("descendant keyset pages follow path index order and equal-key lookups retain null paths", async () => {
		for (let i = 0; i < 150; i++)
			registry(`r${String(149 - i).padStart(3, "0")}`, join(root, String(i).padStart(3, "0")));
		registry("no-path", "", "scope-without-path");
		await bootstrap();
		const descendants = await claims([{ path: root }]);
		expect(new Set(descendants.map((claim) => claim.id)).size).toBe(150);
		expect(
			(await claims([{ scopeProjectId: "scope-without-path" }])).map((claim) => claim.id),
		).toEqual(["no-path"]);
		expect((await claims([{ repositoryKey: "repo-no-path" }])).map((claim) => claim.id)).toEqual([
			"no-path",
		]);
	});
	test("raw empty workspace_context remains malformed JSON instead of an empty inventory", async () => {
		narrator("empty-context", root);
		database.query("UPDATE narrators SET workspace_context='' WHERE id='empty-context'").run();
		await expect(claims([{ path: join(root, "unrelated") }], false)).rejects.toThrow();
	});
	test("oversized raw legacy scalars reject before entering the JavaScript projection", async () => {
		registry("large", "x".repeat(200_000));
		await expect(claims([{ path: root }], false)).rejects.toThrow("row budget");
	});
	test("uninitialised inventory beyond one page is never complete", async () => {
		for (let i = 0; i < 65; i++) registry(`r${i}`, root);
		expect((await service.read([{ path: root }], deadline())).complete).toBe(false);
		await bootstrap();
		expect((await service.read([{ path: root }], deadline())).complete).toBe(true);
	});
	test("more than old row/byte limits stays queryable through targeted indexes", async () => {
		database.transaction(() => {
			for (let i = 0; i < 8500; i++)
				registry(`r${String(i).padStart(5, "0")}`, root, `project-${i}`);
		})();
		await bootstrap();
		const before = changes();
		expect((await claims([{ repositoryKey: "repo-r08499" }])).map((claim) => claim.id)).toEqual([
			"r08499",
		]);
		expect(await claims([{ path: join(root, "unrelated") }])).toHaveLength(8500); // all are ancestors, no artificial cap
		expect(changes()).toBe(before);
		for (const [where, args, expected] of [
			["device_id = ? AND canonical_path = ?", ["local", root], "idx_protection_path"],
			[
				"device_id = ? AND canonical_path >= ? AND canonical_path < ?",
				["local", `${root}/`, `${root}0`],
				"idx_protection_path",
			],
			[
				"device_id = ? AND repository_key = ?",
				["local", "repo-r08499"],
				"idx_protection_repository",
			],
			["scope_project_id = ?", ["project-8499"], "idx_protection_scope"],
			["shadow_key = ?", ["key"], "idx_protection_shadow"],
		] as const) {
			const pathOrder = where.includes("canonical_path >=");
			const order = pathOrder ? "canonical_path,source,source_id,slot" : "source,source_id,slot";
			const parameters = pathOrder ? "?,?,?,?" : "?,?,?";
			const cursor = pathOrder ? ["", "", "", -1] : ["", "", -1];
			const plan = database
				.query(`EXPLAIN QUERY PLAN SELECT source, source_id, slot, claim_json, canonical_path FROM resource_protection_paths WHERE ${where}
					AND (${order}) > (${parameters}) ORDER BY ${order} LIMIT 64`)
				.all(...args, ...cursor);
			expect(JSON.stringify(plan)).toContain(expected);
			expect(JSON.stringify(plan)).not.toContain("USE TEMP B-TREE");
		}
	}, 60_000);
	test("a slow first dirty item cannot starve the rest of a short page", async () => {
		const slow = join(root, "slow");
		narrator("a-slow", slow);
		narrator("z-fast", root);
		database.query("UPDATE resource_protection_sources SET complete=1 WHERE source_id=''").run();
		const entered = Promise.withResolvers<void>();
		const release = Promise.withResolvers<string>();
		const realpath = fs.realpath;
		const spy = spyOn(fs, "realpath").mockImplementation(((
			path: Parameters<typeof fs.realpath>[0],
		) => {
			if (path === slow) {
				entered.resolve();
				return release.promise;
			}
			return realpath(path);
		}) as typeof fs.realpath);
		try {
			const controller = new AbortController();
			const work = service.maintain(deadline(), controller.signal);
			await entered.promise;
			controller.abort();
			await expect(work).rejects.toThrow();
			await service.maintain(deadline());
			expect(
				database
					.query(
						"SELECT source_id, dirty FROM resource_protection_sources WHERE source='narrator' AND source_id!='' ORDER BY source_id",
					)
					.all(),
			).toEqual([
				{ source_id: "a-slow", dirty: 1 },
				{ source_id: "z-fast", dirty: 0 },
			]);
		} finally {
			release.resolve(slow);
			spy.mockRestore();
		}
	});
	test("raw candidate pages stay bounded with many dirty same-path aliases; actual plans never temp-sort", async () => {
		const owned = join(root, "owned"),
			alias = join(root, "alias");
		await mkdir(owned);
		await symlink(owned, alias);
		for (let i = 0; i < 240; i++) narrator(`dirty-${String(i).padStart(3, "0")}`, alias);
		narrator("z-keeper", alias);
		await bootstrap();
		database
			.query("UPDATE narrators SET cwd = ? WHERE id != 'z-keeper'")
			.run(join(root, "elsewhere"));
		const pages: { sql: string; args: unknown[]; size: number }[] = [];
		const query = database.query.bind(database);
		const spy = spyOn(database, "query").mockImplementation(((sql: string) => {
			const statement = query(sql);
			if (!/^SELECT source, source_id, slot, canonical_path/.test(sql)) return statement;
			return new Proxy(statement, {
				get(target, property) {
					if (property === "all")
						return (...args: never[]) => {
							const rows = target.all(...args);
							pages.push({ sql, args, size: rows.length });
							return rows;
						};
					const value = Reflect.get(target, property, target);
					return typeof value === "function" ? value.bind(target) : value;
				},
			});
		}) as typeof database.query);
		const before = changes();
		try {
			expect(new Set((await claims([{ path: owned }])).map((claim) => claim.id))).toEqual(
				new Set(["z-keeper"]),
			);
		} finally {
			spy.mockRestore();
		}
		expect(changes()).toBe(before);
		expect(pages.filter((page) => page.size === 64).length).toBeGreaterThanOrEqual(6);
		for (const page of pages) {
			expect(page.size).toBeLessThanOrEqual(64);
			expect(page.args.at(-1)).toBe(64);
			expect(page.sql).not.toMatch(/NOT EXISTS|JOIN|dirty/i);
			const plan = JSON.stringify(
				query(`EXPLAIN QUERY PLAN ${page.sql}`).all(...(page.args as never[])),
			);
			expect(plan).toContain(
				page.sql.includes("alias_sensitive") ? "idx_protection_alias" : "idx_protection_path",
			);
			expect(plan).not.toContain("USE TEMP B-TREE");
		}
	});
});

describe("one-time legacy receipt pins", () => {
	test("a failed retry-row write retains the consumed entry, then imports later records and restarts idempotently", async () => {
		await mkdir(receipts);
		const files = ["a", "b"].map((letter) => join(receipts, `${letter.repeat(64)}.json`));
		for (const file of files) await writeFile(file, JSON.stringify(receipt(root)));
		database.exec(`CREATE TRIGGER fail_receipt_registration BEFORE INSERT ON resource_protection_sources
			WHEN NEW.source = 'receipt' AND NEW.source_id != '' BEGIN
			SELECT RAISE(ABORT, 'injected receipt write failure'); END`);
		await expect(service.maintain(deadline())).rejects.toThrow("injected receipt write failure");
		expect(database.query("SELECT count(*) AS n FROM resource_protection_paths").get()).toEqual({
			n: 0,
		});
		expect((await service.read([{ path: root }], deadline())).complete).toBe(false);
		database.exec("DROP TRIGGER fail_receipt_registration");
		await service.maintain(deadline());
		expect(
			database
				.query("SELECT count(*) AS n FROM resource_protection_paths WHERE source='receipt'")
				.get(),
		).toEqual({ n: 2 });
		for (const file of files) await writeFile(file, "{");
		service = createResourceProtectionIndex(database, receipts);
		await bootstrap();
		expect((await claims([{ path: root }])).map((claim) => claim.id).sort()).toEqual(files);
		const before = changes();
		await service.maintain(deadline());
		expect(changes()).toBe(before);
	});
	for (const stage of ["open", "read", "close"] as const) {
		test(`pending receipt ${stage} is bounded, pauses only legacy import and advances short retry pages`, async () => {
			await mkdir(receipts);
			const files = ["a", "b"].map((letter) => join(receipts, `${letter.repeat(64)}.json`));
			for (const file of files) await writeFile(file, "{");
			await service.maintain(deadline()); // Reach EOF with two durable retry rows.
			for (const file of files) await writeFile(file, JSON.stringify(receipt(root)));
			const entered = Promise.withResolvers<void>();
			const release = Promise.withResolvers<void>();
			const closed = Promise.withResolvers<void>();
			const open = fs.open;
			let opens = 0;
			const spy = spyOn(fs, "open").mockImplementation(async (...args) => {
				const file = await open(...args);
				if (++opens !== 1) return file;
				const close = file.close.bind(file);
				spyOn(file, "close").mockImplementation(async () => {
					if (stage === "close") {
						entered.resolve();
						await release.promise;
					}
					await close();
					closed.resolve();
				});
				if (stage === "read") {
					const read = file.read.bind(file);
					spyOn(file, "read").mockImplementationOnce((async (
						...readArgs: Parameters<typeof file.read>
					) => {
						entered.resolve();
						await release.promise;
						return read(...readArgs);
					}) as typeof file.read);
				}
				if (stage === "open") {
					entered.resolve();
					await release.promise;
				}
				return file;
			});
			try {
				const controller = new AbortController();
				const work = service.maintain(deadline(), controller.signal);
				await entered.promise;
				controller.abort();
				await expect(work).rejects.toThrow();
				narrator("business-narrator", root);
				registry("business-registry", root);
				database
					.query("INSERT INTO chapters(id, worktree_path) VALUES ('business-chapter', ?)")
					.run(root);
				await service.maintain(deadline());
				expect(opens).toBe(1); // No second open/read while the first IO or its close is unresolved.
				expect(await claims([{ path: root }], false)).toHaveLength(3);
				expect((await service.read([{ path: root }], deadline())).complete).toBe(false);
				release.resolve();
				await closed.promise;
				await new Promise<void>((done) => setTimeout(done, 0));
				await service.maintain(deadline());
				expect(
					database
						.query(
							"SELECT source_id, dirty FROM resource_protection_sources WHERE source='receipt' AND source_id!='' ORDER BY source_id",
						)
						.all(),
				).toEqual([
					{ source_id: files[0], dirty: 1 },
					{ source_id: files[1], dirty: 0 },
				]);
				await bootstrap();
				expect(
					(await claims([{ path: root }])).filter((claim) => claim.kind === "receipt"),
				).toHaveLength(2);
			} finally {
				release.resolve();
				await closed.promise;
				spy.mockRestore();
			}
		});
	}
	test("legacy compatibility waits for background import and ignores ordinary atomic-save temporary files", async () => {
		await mkdir(receipts);
		const file = join(receipts, `${"a".repeat(64)}.json`);
		await writeFile(file, JSON.stringify(receipt(join(root, "legacy"))));
		await writeFile(`${file}.00000000-0000-4000-8000-000000000000.pending`, "{");
		const before = changes();
		expect((await service.read([{ path: join(root, "legacy") }], deadline())).complete).toBe(false);
		expect(await claims([{ path: join(root, "legacy") }], false)).toEqual([]);
		expect(changes()).toBe(before);
		await bootstrap();
		expect((await claims([{ path: join(root, "legacy") }])).map((claim) => claim.id)).toEqual([
			file,
		]);
	});
	test("timed-out directory reads and pending cleanup never block business maintenance or reopen the directory", async () => {
		await mkdir(receipts);
		await writeFile(join(receipts, `${"a".repeat(64)}.json`), JSON.stringify(receipt(root)));
		database
			.query("UPDATE resource_protection_sources SET complete=1 WHERE source!='receipt'")
			.run();
		const reading = Promise.withResolvers<void>();
		const releaseRead = Promise.withResolvers<void>();
		const closing = Promise.withResolvers<void>();
		const releaseClose = Promise.withResolvers<void>();
		const closed = Promise.withResolvers<void>();
		const opendir = fs.opendir;
		let opens = 0;
		let reads = 0;
		let closes = 0;
		const spy = spyOn(fs, "opendir").mockImplementation(async (...args) => {
			const dir = await opendir(...args);
			if (++opens !== 1) return dir;
			const read = dir.read.bind(dir);
			const close = dir.close.bind(dir);
			spyOn(dir, "read").mockImplementationOnce(async () => {
				reads++;
				reading.resolve();
				await releaseRead.promise;
				return read();
			});
			spyOn(dir, "close").mockImplementation(async () => {
				closes++;
				closing.resolve();
				await releaseClose.promise;
				await close();
				closed.resolve();
			});
			return dir;
		});
		try {
			const controller = new AbortController();
			const work = service.maintain(deadline(), controller.signal);
			await reading.promise;
			controller.abort();
			await expect(work).rejects.toThrow();
			narrator("before-close", root);
			await service.maintain(deadline());
			expect(opens).toBe(1);
			expect(reads).toBe(1);
			releaseRead.resolve();
			await closing.promise;
			narrator("during-close", root);
			await service.maintain(deadline());
			expect(await claims([{ path: root }], false)).toHaveLength(2);
			expect(opens).toBe(1);
			expect(closes).toBe(1);
			releaseClose.resolve();
			await closed.promise;
			await new Promise<void>((done) => setTimeout(done, 0));
			await bootstrap();
			expect(
				(await claims([{ path: root }])).filter((claim) => claim.kind === "receipt"),
			).toHaveLength(1);
		} finally {
			releaseRead.resolve();
			releaseClose.resolve();
			await closed.promise;
			spy.mockRestore();
		}
	});
	test("legacy-only pins survive restart, rewritten and deleted original journals", async () => {
		await mkdir(receipts);
		const file = join(receipts, `${"b".repeat(64)}.json`);
		const destination = join(root, "old");
		await writeFile(file, JSON.stringify(receipt(destination)));
		await bootstrap();
		service = createResourceProtectionIndex(database, receipts);
		await writeFile(file, JSON.stringify(receipt(join(root, "new"))));
		await service.maintain(deadline());
		expect((await claims([{ path: destination }])).map((claim) => claim.id)).toEqual([file]);
		expect(await claims([{ path: join(root, "new") }])).toEqual([]);
		await rm(file);
		service = createResourceProtectionIndex(database, receipts);
		const before = changes();
		await service.maintain(deadline());
		expect((await claims([{ path: destination }])).map((claim) => claim.id)).toEqual([file]);
		expect(await claims([{ path: destination }], false)).toEqual([]);
		expect(changes()).toBe(before);
	});
	test("ordinary capture never decodes even malformed receipt index entries", async () => {
		await mkdir(receipts);
		await writeFile(join(receipts, `${"9".repeat(64)}.json`), JSON.stringify(receipt(root)));
		narrator("cwd", root);
		registry("registry", root);
		await bootstrap();
		database
			.query(
				"UPDATE resource_protection_paths SET claim_json = '{', alias_sensitive = 1 WHERE source = 'receipt'",
			)
			.run();
		const before = changes();
		expect((await claims([{ path: root }], false)).map((claim) => claim.id)).toEqual(
			expect.arrayContaining(["cwd", "registry"]),
		);
		expect(changes()).toBe(before);
		await expect(claims([{ path: root }])).rejects.toThrow();
	});
	test("completed import never reads renamed or unreadable receipt directories again", async () => {
		await mkdir(receipts);
		const file = join(receipts, `${"c".repeat(64)}.json`);
		await writeFile(file, JSON.stringify(receipt(root)));
		await bootstrap();
		const retired = `${receipts}-retired`;
		await rename(receipts, retired);
		await writeFile(receipts, "not a directory");
		await chmod(retired, 0);
		try {
			service = createResourceProtectionIndex(database, receipts);
			const before = changes();
			expect((await claims([{ path: root }])).map((claim) => claim.id)).toEqual([file]);
			await service.maintain(deadline());
			expect(changes()).toBe(before);
		} finally {
			await chmod(retired, 0o700);
		}
	});
	test("damaged legacy finals block compatibility proof, not ordinary cold capture or business claims", async () => {
		await mkdir(receipts);
		const file = join(receipts, `${"d".repeat(64)}.json`);
		narrator("cwd", root);
		registry("registered", root);
		for (const content of [
			"{",
			" ".repeat(32 * 1024 + 1),
			JSON.stringify({ ...receipt(root), deviceId: null }),
		]) {
			await writeFile(file, content);
			expect((await service.read([{ path: root }], deadline())).complete).toBe(false);
			const before = changes();
			expect((await claims([{ path: root }], false)).map((claim) => claim.id)).toEqual(
				expect.arrayContaining(["cwd", "registered"]),
			);
			expect(changes()).toBe(before);
		}
		await service.maintain(deadline());
		await service.maintain(deadline());
		expect(
			database
				.query(
					"SELECT dirty FROM resource_protection_sources WHERE source = 'receipt' AND source_id = ?",
				)
				.get(file),
		).toEqual({ dirty: 1 });
		expect((await service.read([{ path: root }], deadline())).complete).toBe(false);
		expect((await claims([{ path: root }], false)).map((claim) => claim.id)).toEqual(
			expect.arrayContaining(["cwd", "registered"]),
		);
	});
	test("legacy import does not invalidate ordinary capture versions; deletion waits for import", async () => {
		await mkdir(receipts);
		const file = join(receipts, `${"e".repeat(64)}.json`);
		await writeFile(file, JSON.stringify(receipt(root)));
		// Complete only the three business sources before starting either read.
		database
			.query("UPDATE resource_protection_sources SET complete = 1 WHERE source != 'receipt'")
			.run();
		const ordinary = await service.read([{ path: root }], deadline(), undefined, false);
		const destructive = await service.read([{ path: root }], deadline());
		await service.maintain(deadline());
		const found: ResourceClaim[] = [];
		for await (const claim of ordinary.claims) found.push(claim);
		expect(found).toEqual([]);
		expect(destructive.complete).toBe(false);
		await bootstrap();
		expect((await claims([{ path: root }])).map((claim) => claim.id)).toEqual([file]);
	});
	test("more than a batch imports progressively, resumes after restart and retains already imported records", async () => {
		await mkdir(receipts);
		for (let i = 0; i < 520; i++)
			await writeFile(
				join(receipts, `${i.toString(16).padStart(64, "0")}.json`),
				JSON.stringify({ ...receipt(root), padding: "x".repeat(600) }),
			);
		expect((await service.read([{ path: root }], deadline())).complete).toBe(false);
		await service.maintain(deadline());
		const first = database
			.query("SELECT source_id FROM resource_protection_paths WHERE source = 'receipt'")
			.all() as { source_id: string }[];
		expect(first).toHaveLength(64);
		// Finish this process's directory pass, then exercise a restart before completion publication.
		for (let i = 0; i < 8; i++) await service.maintain(deadline());
		database
			.query(
				"UPDATE resource_protection_sources SET cursor = NULL WHERE source = 'receipt' AND source_id = ''",
			)
			.run();
		const imported = first[0];
		if (!imported) throw new Error("No receipt imported");
		await writeFile(imported.source_id, "{");
		service = createResourceProtectionIndex(database, receipts);
		await bootstrap();
		expect(
			(await claims([{ repositoryKey: "receipt-repository" }])).filter(
				(claim) => claim.kind === "receipt",
			),
		).toHaveLength(520);
	}, 30_000);
	test("bad first records cannot starve later pages or bounded round-robin retries", async () => {
		await mkdir(receipts);
		for (let i = 0; i < 130; i++)
			await writeFile(
				join(receipts, `${i.toString(16).padStart(64, "0")}.json`),
				i < 65 ? "{" : JSON.stringify(receipt(join(root, "legacy"))),
			);
		for (let i = 0; i < 4; i++) await service.maintain(deadline());
		expect(
			database
				.query("SELECT count(*) AS n FROM resource_protection_paths WHERE source = 'receipt'")
				.get(),
		).toEqual({ n: 65 });
		expect((await service.read([{ path: root }], deadline())).complete).toBe(false);
		// Leave the earliest corrupt record broken; a later retry page must still be imported.
		const later = join(receipts, `${(64).toString(16).padStart(64, "0")}.json`);
		await writeFile(later, JSON.stringify(receipt(join(root, "recovered"))));
		for (let i = 0; i < 3; i++) await service.maintain(deadline());
		expect(
			database
				.query(
					"SELECT 1 AS present FROM resource_protection_paths WHERE source = 'receipt' AND source_id = ?",
				)
				.get(later),
		).toEqual({ present: 1 });
		expect(await claims([{ path: root }], false)).toEqual([]);
	});
});
