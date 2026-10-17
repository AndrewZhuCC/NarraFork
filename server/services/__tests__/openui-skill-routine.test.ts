import { afterAll, afterEach, describe, expect, spyOn, test } from "bun:test";
import { access, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import * as os from "node:os";
import { join } from "node:path";
import { eq } from "drizzle-orm";
import { db } from "../../db";
import { skillDirectoryCaches } from "../../db/schema";
import { getBuiltinRoutine } from "../../lib/builtin-routines";
import { getNarraforkHome } from "../../lib/narrafork-home";
import { settings } from "../../lib/settings";
import { buildEffectiveSystemPrompt } from "../narrator-prompt";
import { getGlobalRoutineStatuses, syncBuiltinSkillRoutines } from "../routine-service";
import {
	invalidateGlobalSkillDirectoryCache,
	loadProjectSkills,
	loadSkillByNameForContext,
	loadSkillSummariesForContext,
} from "../skill-service";

const originalRoutines = structuredClone(settings.routines);
const skillsDir = join(getNarraforkHome(), "skills");
const managedDir = join(skillsDir, "_routine-nf-openui");
const customDir = join(skillsDir, "user-openui");
const projectDir = join(getNarraforkHome(), "retired-skill-project");
const context = { projectGitPath: null, cwd: null };
const skillContent =
	'---\nname: "nf-openui"\ndescription: "User-maintained UI skill"\n---\n\nUser content.\n';
// Bun resolves homedir from the account, independently of HOME.
const homeMock = spyOn(os, "homedir").mockReturnValue(getNarraforkHome());
afterAll(() => homeMock.mockRestore());
afterEach(async () => {
	settings.routines = structuredClone(originalRoutines);
	for (const path of [managedDir, customDir, projectDir]) {
		await rm(path, { recursive: true, force: true });
	}
	await invalidateGlobalSkillDirectoryCache();
});

describe("retired OpenUI guidance", () => {
	test("removes the builtin while GenUI defaults on and still respects explicit disabling", () => {
		expect(getBuiltinRoutine("nf-openui")).toBeUndefined();
		settings.routines = { enabledRoutines: [], disabledRoutines: [], toolModes: {} };
		expect(getGlobalRoutineStatuses().find((routine) => routine.id === "gen_ui")?.enabled).toBe(
			true,
		);
		settings.routines.disabledRoutines = ["gen_ui"];
		expect(getGlobalRoutineStatuses().find((routine) => routine.id === "gen_ui")?.enabled).toBe(
			false,
		);
	});

	test.each([
		"SKILL.md",
		"SKILL.md.disabled",
	])("startup removes managed %s, refreshes discovery, and preserves a same-named user skill", async (filename) => {
		await mkdir(managedDir, { recursive: true });
		await mkdir(customDir, { recursive: true });
		await writeFile(join(managedDir, filename), skillContent);
		await writeFile(join(customDir, "SKILL.md"), skillContent);
		settings.routines = { enabledRoutines: ["nf-openui"], disabledRoutines: [], toolModes: {} };
		await syncBuiltinSkillRoutines();
		await syncBuiltinSkillRoutines();
		await expect(access(managedDir)).rejects.toThrow();
		expect(await readFile(join(customDir, "SKILL.md"), "utf8")).toBe(skillContent);
		const loaded = await loadSkillByNameForContext(context, "nf-openui");
		expect(loaded?.location).toBe(join(customDir, "SKILL.md"));
	});

	test("does not expose retired entries from a still-fresh persisted summary cache", async () => {
		await mkdir(customDir, { recursive: true });
		await writeFile(join(customDir, "SKILL.md"), skillContent);
		const before = await loadSkillSummariesForContext(context, { forceRefresh: true });
		const custom = before.skills.find((skill) => skill.location === join(customDir, "SKILL.md"));
		if (!custom) throw new Error("Expected the user skill in discovery");
		await db
			.update(skillDirectoryCaches)
			.set({
				skillsJson: [
					custom,
					{ ...custom, location: join(managedDir, "SKILL.md"), description: "Retired builtin" },
				],
			})
			.where(eq(skillDirectoryCaches.rootKind, "global"));
		const after = await loadSkillSummariesForContext(context);
		expect(after.skills.filter((skill) => skill.name === "nf-openui")).toEqual([custom]);
		expect(after.roots.some((root) => root.cacheHit)).toBe(true);
	});

	test("lazily removes only the reserved project directory during discovery", async () => {
		const projectSkills = join(projectDir, ".narrafork", "skills");
		const retired = join(projectSkills, "_routine-nf-openui");
		const custom = join(projectSkills, "user-openui");
		await mkdir(retired, { recursive: true });
		await mkdir(custom, { recursive: true });
		await writeFile(join(retired, "SKILL.md.disabled"), skillContent);
		await writeFile(join(custom, "SKILL.md"), skillContent);
		await loadSkillSummariesForContext(
			{ projectGitPath: projectDir, cwd: null },
			{ forceRefresh: true },
		);
		await expect(access(retired)).rejects.toThrow();
		expect((await loadProjectSkills(projectDir)).map((skill) => skill.location)).toContain(
			join(custom, "SKILL.md"),
		);
	});

	test("does not inject the retired capability hint into either language", async () => {
		for (const locale of ["en", "zh-CN"] as const) {
			const { prompt } = await buildEffectiveSystemPrompt({
				basePrompt: "Test narrator",
				cwd: getNarraforkHome(),
				locale,
			});
			expect(prompt).not.toContain("nf-openui");
			expect(prompt).not.toContain("## Chat UI");
		}
	});
});
