import { afterEach, describe, expect, it } from "bun:test";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { findProjectDirCandidates, getProjectDirStatus, usableProjectDir } from "../project-dir";

const roots: string[] = [];

function makeHome(): string {
	const home = mkdtempSync(join(tmpdir(), "project-dir-"));
	roots.push(home);
	return home;
}

function makeRepos(dir: string, count: number) {
	for (let i = 0; i < count; i++) mkdirSync(join(dir, `repo-${i}`, ".git"), { recursive: true });
}

afterEach(() => {
	for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true });
});

describe("getProjectDirStatus", () => {
	it("reports unset, missing, not-a-directory and valid without creating anything", () => {
		const home = makeHome();
		writeFileSync(join(home, "file"), "");
		mkdirSync(join(home, "Projects"));
		expect(getProjectDirStatus("", home).state).toBe("unset");
		expect(getProjectDirStatus("~/projects-missing", home)).toEqual({
			configured: "~/projects-missing",
			path: join(home, "projects-missing"),
			state: "missing",
		});
		expect(getProjectDirStatus("~/file", home).state).toBe("not_directory");
		expect(getProjectDirStatus("~/Projects", home)).toMatchObject({
			path: join(home, "Projects"),
			state: "valid",
		});
		expect(usableProjectDir("~/projects-missing", home)).toBeNull();
		expect(usableProjectDir("~/Projects", home)).toBe(join(home, "Projects"));
	});
});

describe("findProjectDirCandidates", () => {
	it("finds conventional names in any case and repo-holding folders, ranked by repos", async () => {
		const home = makeHome();
		mkdirSync(join(home, "Code"));
		makeRepos(join(home, "Code"), 1);
		mkdirSync(join(home, "work-stuff"));
		makeRepos(join(home, "work-stuff"), 3);
		mkdirSync(join(home, "Music"));
		mkdirSync(join(home, ".hidden-repos"));
		makeRepos(join(home, ".hidden-repos"), 5);
		mkdirSync(join(home, "Documents", "GitHub"), { recursive: true });

		const candidates = await findProjectDirCandidates(home);
		expect(candidates.map((c) => [c.path, c.repoCount, c.conventional])).toEqual([
			[join(home, "work-stuff"), 3, false],
			[join(home, "Code"), 1, true],
			[join(home, "Documents", "GitHub"), 0, true],
		]);
	});

	it("returns nothing for a home without code folders", async () => {
		const home = makeHome();
		mkdirSync(join(home, "Pictures"));
		expect(await findProjectDirCandidates(home)).toEqual([]);
	});
});
