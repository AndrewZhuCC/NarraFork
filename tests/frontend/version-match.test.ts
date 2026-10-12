import { describe, expect, test } from "bun:test";
import { appVersionsMatch } from "../../frontend/lib/version-match";

describe("appVersionsMatch", () => {
	test("identical versions match, with or without leading v", () => {
		expect(appVersionsMatch("1.0.0", "1.0.0")).toBe(true);
		expect(appVersionsMatch("v1.0.0", "1.0.0")).toBe(true);
		expect(appVersionsMatch("1.0.0", "v1.0.0")).toBe(true);
		expect(appVersionsMatch("1.0.0+abcdef0", "1.0.0+abcdef0")).toBe(true);
	});

	test("short commit hash matches the full commit hash of the same build", () => {
		// Release-CI server binaries embed the full 40-char commit while the
		// frontend bundle carries `git rev-parse --short HEAD` (7 chars).
		const full = "0.9.0+fe6657f5a48086b931639c1a03bebdc61c87e9b4";
		const short = "0.9.0+fe6657f";
		expect(appVersionsMatch(short, full)).toBe(true);
		expect(appVersionsMatch(full, short)).toBe(true);
		expect(appVersionsMatch(`v${short}`, full)).toBe(true);
	});

	test("different base versions never match", () => {
		expect(appVersionsMatch("0.9.0+fe6657f", "0.9.1+fe6657f")).toBe(false);
		expect(appVersionsMatch("1.0.0", "2.0.0")).toBe(false);
	});

	test("different commits of the same base version never match", () => {
		expect(appVersionsMatch("0.9.0+fe6657f", "0.9.0+8ba194f")).toBe(false);
		expect(
			appVersionsMatch(
				"0.9.0+fe6657f5a48086b931639c1a03bebdc61c87e9b4",
				"0.9.0+8ba194f0a48086b931639c1a03bebdc61c87e9b4",
			),
		).toBe(false);
	});

	test("a missing commit part only matches a missing one", () => {
		expect(appVersionsMatch("1.0.0", "1.0.0+abcdef0")).toBe(false);
		expect(appVersionsMatch("1.0.0+abcdef0", "1.0.0")).toBe(false);
		expect(appVersionsMatch("1.0.0", "v1.0.0")).toBe(true);
	});
});
