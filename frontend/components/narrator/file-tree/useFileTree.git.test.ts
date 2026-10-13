import { describe, expect, test } from "bun:test";
import { omitGitDirectory } from "./useFileTree";

describe("omitGitDirectory", () => {
	test("drops the git directory and keeps other dotfiles", () => {
		const entries = omitGitDirectory([
			{ name: ".git" },
			{ name: ".gitignore" },
			{ name: ".env" },
			{ name: "src" },
		]);
		expect(entries.map((entry) => entry.name)).toEqual([".gitignore", ".env", "src"]);
	});
});
