import { describe, expect, test } from "bun:test";
import { isFormerChapterSessionWithoutDirectory, resolveNarratorSessionCwd } from "../narrator-cwd";

describe("resolveNarratorSessionCwd", () => {
	test("prefers an explicit narrator cwd over the project directory", () => {
		expect(resolveNarratorSessionCwd("/chosen/workspace", "/project", "/home")).toBe(
			"/chosen/workspace",
		);
	});

	test("uses the project path for a project-context session", () => {
		expect(resolveNarratorSessionCwd(null, "/project", "/home")).toBe("/project");
	});

	test("uses the supplied fallback when no persisted path exists", () => {
		expect(resolveNarratorSessionCwd(null, null, "/home")).toBe("/home");
		expect(resolveNarratorSessionCwd("", "", "/home")).toBe("/home");
	});
});

describe("former chapter sessions", () => {
	test("a session without a directory or the standalone trait was created in a chapter", () => {
		expect(isFormerChapterSessionWithoutDirectory({ cwd: null, traits: null })).toBe(true);
		expect(isFormerChapterSessionWithoutDirectory({ cwd: null, traits: ["review"] })).toBe(true);
		expect(isFormerChapterSessionWithoutDirectory({ cwd: "", traits: [] })).toBe(true);
	});

	test("its own directory, even one unavailable on this host, is kept", () => {
		expect(isFormerChapterSessionWithoutDirectory({ cwd: "/old-worktree", traits: null })).toBe(
			false,
		);
	});

	test("a standalone session without a directory is not one", () => {
		expect(isFormerChapterSessionWithoutDirectory({ cwd: null, traits: ["standalone"] })).toBe(
			false,
		);
	});
});
