/**
 * standalone-narrator-cwd.test.ts
 *
 * Where a standalone narrator starts when the client sent no directory. The rule the
 * tests pin: a configured default is used only when it EXISTS, nothing is ever
 * created, and with no usable default the answer is "the user must choose" — never a
 * guessed directory (home included). Chapter-bound narrators stay unset so session
 * start binds their worktree.
 */

import { describe, expect, test } from "bun:test";
import {
	resolveStandaloneNarratorCwd,
	type StandaloneNarratorCwdDeps,
} from "../standalone-narrator-cwd";

const HOME = "/home/tester";

function deps(
	overrides: Partial<StandaloneNarratorCwdDeps> & { existing?: string[] } = {},
): StandaloneNarratorCwdDeps {
	const existing = new Set(overrides.existing ?? []);
	return {
		home: HOME,
		configuredDir: undefined,
		isDirectory: (path) => existing.has(path),
		// Enough of node:path resolve for these inputs; the real one is wired in the route.
		toAbsolute: (path) => (path.startsWith("/") ? path.replace(/\/+$/, "") : `/cwd/${path}`),
		...overrides,
	};
}

describe("an explicit cwd from the client", () => {
	test("wins over everything and is trimmed", () => {
		expect(
			resolveStandaloneNarratorCwd({ cwd: "  /srv/work  " }, deps({ configuredDir: "/data" })),
		).toEqual({ kind: "cwd", cwd: "/srv/work" });
	});

	test("a whitespace-only cwd is treated as absent", () => {
		expect(resolveStandaloneNarratorCwd({ cwd: "   " }, deps())).toEqual({ kind: "required" });
	});
});

describe("chapter-bound narrators", () => {
	test("are left unset so session start binds the worktree", () => {
		expect(
			resolveStandaloneNarratorCwd(
				{ chapterId: "ch1" },
				deps({ configuredDir: "/data/projects", existing: ["/data/projects"] }),
			),
		).toEqual({ kind: "session" });
	});

	test("still honour an explicit cwd", () => {
		expect(resolveStandaloneNarratorCwd({ cwd: "/srv/work", chapterId: "ch1" }, deps())).toEqual({
			kind: "cwd",
			cwd: "/srv/work",
		});
	});
});

describe("the configured default project directory", () => {
	test("is used when it exists", () => {
		expect(
			resolveStandaloneNarratorCwd(
				{},
				deps({ configuredDir: "/data/projects", existing: ["/data/projects"] }),
			),
		).toEqual({ kind: "cwd", cwd: "/data/projects" });
	});

	test("expands a bare ~ and a ~/ prefix", () => {
		expect(
			resolveStandaloneNarratorCwd({}, deps({ configuredDir: "~", existing: [HOME] })),
		).toEqual({ kind: "cwd", cwd: HOME });
		expect(
			resolveStandaloneNarratorCwd(
				{},
				deps({ configuredDir: "~/Code", existing: [`${HOME}/Code`] }),
			),
		).toEqual({ kind: "cwd", cwd: `${HOME}/Code` });
	});

	test("requires a choice when it does not exist, instead of creating or guessing one", () => {
		expect(resolveStandaloneNarratorCwd({}, deps({ configuredDir: "~/projects" }))).toEqual({
			kind: "required",
		});
	});

	test("requires a choice when unset, blank, or an unresolvable ~user form", () => {
		for (const configuredDir of [undefined, "   ", "~other/projects"]) {
			expect(resolveStandaloneNarratorCwd({}, deps({ configuredDir, existing: [HOME] }))).toEqual({
				kind: "required",
			});
		}
	});

	test("makes a relative configured path absolute", () => {
		expect(
			resolveStandaloneNarratorCwd(
				{},
				deps({ configuredDir: "projects", existing: ["/cwd/projects"] }),
			),
		).toEqual({ kind: "cwd", cwd: "/cwd/projects" });
	});
});
