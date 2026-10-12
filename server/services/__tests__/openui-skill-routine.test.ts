import { afterAll, afterEach, describe, expect, spyOn, test } from "bun:test";
import { access, readFile, rm, writeFile } from "node:fs/promises";
import * as os from "node:os";
import { join } from "node:path";
import { OPENUI_SKILL_CONTENT } from "../../generated/openui-skill";
import { getBuiltinRoutine, getBuiltinSkillRoutines } from "../../lib/builtin-routines";
import { getNarraforkHome } from "../../lib/narrafork-home";
import { settings } from "../../lib/settings";
import {
	disableRoutineGlobal,
	enableRoutineGlobal,
	getGlobalRoutineStatuses,
	syncBuiltinSkillRoutines,
} from "../routine-service";
import {
	invalidateGlobalSkillDirectoryCache,
	loadSkillByNameForContext,
	loadSkillSummariesForContext,
	toggleGlobalSkill,
} from "../skill-service";

const ROUTINE_ID = "nf-openui";
const originalRoutines = structuredClone(settings.routines);
const skillPath = join(getNarraforkHome(), "skills", "_routine-nf-openui", "SKILL.md");
const creatorPath = join(getNarraforkHome(), "skills", "_routine-skill-creator", "SKILL.md");
const context = { projectGitPath: null, cwd: null };
// Bun resolves homedir from the account, not HOME; keep discovery out of real user skills.
const homeMock = spyOn(os, "homedir").mockReturnValue(getNarraforkHome());
afterAll(() => homeMock.mockRestore());

afterEach(async () => {
	settings.routines = structuredClone(originalRoutines);
	for (const id of [ROUTINE_ID, "skill-creator"]) {
		await rm(join(getNarraforkHome(), "skills", `_routine-${id}`), {
			recursive: true,
			force: true,
		});
	}
	await invalidateGlobalSkillDirectoryCache();
});

function openuiSkill() {
	const routine = getBuiltinRoutine(ROUTINE_ID);
	if (!routine?.skill) throw new Error("nf-openui skill routine is missing");
	return routine.skill;
}

describe("nf-openui builtin skill routine", () => {
	test("is available by default but exposes only a short, chat-UI-specific description", () => {
		const routine = getBuiltinRoutine(ROUTINE_ID);
		expect(routine?.type).toBe("skill");
		expect(routine?.category).toBe("workflow");
		expect(routine?.defaultEnabled).toBe(true);
		expect(getBuiltinSkillRoutines().some((entry) => entry.id === ROUTINE_ID)).toBe(true);
		expect(getGlobalRoutineStatuses().find((entry) => entry.id === ROUTINE_ID)?.enabled).toBe(true);
		const skill = openuiSkill();
		expect(skill.name).toBe(ROUTINE_ID);
		expect(skill.descriptionEn).toContain("inside NarraFork chat when requested");
		expect(skill.descriptionEn).toContain("not for ordinary answers or code development");
		expect(skill.descriptionZh).toContain("普通回答和代码开发无需加载");
		expect(skill.descriptionEn.length).toBeLessThan(200);
		expect(skill.descriptionZh.length).toBeLessThan(100);
	});

	test("uses the generated component reference with Markdown and execution boundaries", () => {
		const content = openuiSkill().content;
		expect(content).toBe(OPENUI_SKILL_CONTENT);
		expect(content).toContain("## Component Signatures");
		expect(content).toContain("Table(");
		expect(content).toContain("Form(");
		expect(content).toContain("After compaction");
		expect(content).toContain("```openui-lang");
		expect(content).toContain(
			"`inlineMode: true`, `bindings: true`, `tools: []`, and `toolCalls: false`",
		);
		expect(content).toContain("Ordinary answers remain Markdown");
		expect(content).not.toContain("Your ENTIRE response must be valid openui-lang");
		expect(content).not.toContain("## Query");
		expect(content).not.toContain("## Mutation");
		expect(content).toContain("never emit Query(), Mutation(), @Run, MCP calls");
		expect(content).toContain("The only outward actions are explicit user clicks");
		expect(content).toContain("Never emit @OpenUrl, open_url");
		expect(content).toContain("unknown action types");
		expect(content).toContain("do not install anything for it");
	});

	test("materializes on enable, keeps discovery brief, and loads the body only on demand", async () => {
		// bunfig's preload isolates NARRAFORK_HOME before any service imports.
		const path = join(getNarraforkHome(), "skills", "_routine-nf-openui", "SKILL.md");
		const context = { projectGitPath: null, cwd: null };
		await expect(access(path)).rejects.toThrow();
		try {
			await enableRoutineGlobal(ROUTINE_ID, "test-openui-skill-user");
			const raw = await readFile(path, "utf8");
			expect(raw).toContain('name: "nf-openui"');
			expect(raw).toContain(OPENUI_SKILL_CONTENT);

			const summaries = await loadSkillSummariesForContext(context, { forceRefresh: true });
			const summary = summaries.skills.find((skill) => skill.name === ROUTINE_ID);
			expect(summary?.description).toBe(openuiSkill().descriptionEn);
			expect(summary).not.toHaveProperty("content");

			const loaded = await loadSkillByNameForContext(context, ROUTINE_ID);
			expect(loaded?.location).toBe(path);
			expect(loaded?.content).toBe(OPENUI_SKILL_CONTENT.trim());
		} finally {
			await disableRoutineGlobal(ROUTINE_ID, "test-openui-skill-user");
		}
		await expect(access(path)).rejects.toThrow();
		expect(await loadSkillByNameForContext(context, ROUTINE_ID)).toBeNull();
	});

	test("startup installs the default skill and invalidates cached discovery", async () => {
		const before = await loadSkillSummariesForContext(context, { forceRefresh: true });
		expect(before.skills.some((skill) => skill.name === ROUTINE_ID)).toBe(false);
		await syncBuiltinSkillRoutines();
		const after = await loadSkillSummariesForContext(context);
		const summary = after.skills.find((skill) => skill.name === ROUTINE_ID);
		expect(summary?.description).toBe(openuiSkill().descriptionEn);
		expect(summary).not.toHaveProperty("content");
		expect((await loadSkillByNameForContext(context, ROUTINE_ID))?.content).toBe(
			OPENUI_SKILL_CONTENT.trim(),
		);
		await expect(access(creatorPath)).rejects.toThrow();
	});

	test("startup refreshes enabled builtin definitions using the same materialization path", async () => {
		await enableRoutineGlobal("skill-creator", "test-openui-skill-user");
		await writeFile(creatorPath, "old builtin definition", "utf8");
		await syncBuiltinSkillRoutines();
		expect(await readFile(creatorPath, "utf8")).toContain(
			getBuiltinRoutine("skill-creator")?.skill?.content ?? "missing skill-creator",
		);
		expect(await readFile(skillPath, "utf8")).toContain(OPENUI_SKILL_CONTENT);
		await syncBuiltinSkillRoutines();
		const summaries = await loadSkillSummariesForContext(context);
		expect(summaries.skills.filter((skill) => skill.name === ROUTINE_ID)).toHaveLength(1);
		expect(settings.routines.enabledRoutines).not.toContain(ROUTINE_ID);
	});

	test("an explicit routine disable remains disabled after startup", async () => {
		await disableRoutineGlobal(ROUTINE_ID, "test-openui-skill-user");
		await syncBuiltinSkillRoutines();
		await expect(access(skillPath)).rejects.toThrow();
		expect(getGlobalRoutineStatuses().find((routine) => routine.id === ROUTINE_ID)?.enabled).toBe(
			false,
		);
		expect(await loadSkillByNameForContext(context, ROUTINE_ID)).toBeNull();
	});

	test("Skills page disable survives startup while its definition stays up to date", async () => {
		await syncBuiltinSkillRoutines();
		await toggleGlobalSkill(ROUTINE_ID, false);
		await writeFile(`${skillPath}.disabled`, "old disabled builtin definition", "utf8");
		await syncBuiltinSkillRoutines();
		await expect(access(skillPath)).rejects.toThrow();
		expect(await readFile(`${skillPath}.disabled`, "utf8")).toContain(OPENUI_SKILL_CONTENT);
		expect(await loadSkillByNameForContext(context, ROUTINE_ID)).toBeNull();

		await toggleGlobalSkill(ROUTINE_ID, true);
		await syncBuiltinSkillRoutines();
		expect((await loadSkillByNameForContext(context, ROUTINE_ID))?.content).toBe(
			OPENUI_SKILL_CONTENT.trim(),
		);
	});
});
