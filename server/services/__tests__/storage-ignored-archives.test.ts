/**
 * Ignored-file archives left behind by dormant chapters.
 *
 * `~/.narrafork/dormant-ignored/<chapterId>` held the files git refuses to track —
 * `.env`, local credential files — copied aside so a chapter going dormant did not
 * destroy them. Chapters are gone, so nothing will restore these archives any more, but
 * they are the user's plaintext secrets and are never deleted automatically. The storage
 * scan must still report them, or their bytes are in no total at all.
 */
import { afterEach, describe, expect, test } from "bun:test";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { generateId } from "../../lib/id";
import { getNarraforkPath } from "../../lib/narrafork-home";
import { scanStorage } from "../storage-service";

const SECRET = "API_TOKEN=super-secret\n";

const archives: string[] = [];

function archiveDir(chapterId: string): string {
	return getNarraforkPath("dormant-ignored", chapterId);
}

function seedArchive(): string {
	const chapterId = generateId();
	archives.push(chapterId);
	const dir = archiveDir(chapterId);
	mkdirSync(dir, { recursive: true });
	const file = join(dir, ".env");
	writeFileSync(file, SECRET);
	return file;
}

afterEach(() => {
	for (const id of archives.splice(0)) rmSync(archiveDir(id), { recursive: true, force: true });
});

describe("storage scan", () => {
	test("reports the archives as their own category, every one of them unowned", async () => {
		seedArchive();
		seedArchive();

		// `scanStorage` streams progress and yields the result as the generator's return
		// value, so it has to be driven to completion rather than awaited.
		const scan = scanStorage();
		let next = await scan.next();
		while (!next.done) next = await scan.next();
		const category = next.value.categories.find((c) => c.key === "dormantIgnored");

		// Without a category of its own this directory's bytes were in no total at all,
		// which is how it grew unnoticed.
		expect(category).toBeDefined();
		expect(category?.sizeBytes).toBeGreaterThan(0);
		const details = category?.details as { archiveCount?: number; orphanCount?: number };
		// Other suites share this data directory, so only a lower bound is ours.
		expect(details.archiveCount).toBeGreaterThanOrEqual(2);
		expect(details.orphanCount).toBe(details.archiveCount);
	});
});
