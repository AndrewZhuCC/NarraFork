import { describe, expect, test } from "bun:test";
import { resolveLegacyNarratorCwd, resolveNarratorSessionCwd } from "../narrator-cwd";

describe("resolveNarratorSessionCwd", () => {
	test("prefers an explicit narrator cwd over the chapter worktree", () => {
		expect(
			resolveNarratorSessionCwd("/chosen/workspace", "/chapter/worktree", "/project", "/home"),
		).toBe("/chosen/workspace");
	});

	test("uses the chapter worktree when no override exists", () => {
		expect(resolveNarratorSessionCwd(null, "/chapter/worktree", "/project", "/home")).toBe(
			"/chapter/worktree",
		);
	});

	test("uses the project path for a project-context session", () => {
		expect(resolveNarratorSessionCwd(null, null, "/project", "/home")).toBe("/project");
	});

	test("uses the supplied fallback when no persisted path exists", () => {
		expect(resolveNarratorSessionCwd(null, null, null, "/home")).toBe("/home");
	});
});

describe("legacy directory compatibility", () => {
	test("keeps explicit cwd and worktree paths, including paths unavailable on this host", () => {
		expect(resolveLegacyNarratorCwd("/chosen", "/old-worktree", "/main", false)).toBe("/chosen");
		expect(resolveLegacyNarratorCwd(null, "/old-worktree", "/main", false)).toBe("/old-worktree");
	});
	test("missing branch directory never becomes main or HOME", () => {
		expect(resolveLegacyNarratorCwd(null, null, "/main", false)).toBeNull();
		expect(resolveLegacyNarratorCwd(null, null, null, false)).toBeNull();
	});
	test("a known root chapter may still use its original project directory", () => {
		expect(resolveLegacyNarratorCwd(null, null, "/main", true)).toBe("/main");
		expect(resolveLegacyNarratorCwd(null, null, null, true)).toBeNull();
	});
});
