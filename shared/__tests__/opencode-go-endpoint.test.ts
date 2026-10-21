import { describe, expect, test } from "bun:test";
import { isOpencodeGoBaseUrl, parseOpencodeGoBaseUrl } from "../opencode-go-endpoint";

describe("parseOpencodeGoBaseUrl", () => {
	test("the standard zen route and its usage endpoint", () => {
		for (const base of ["https://opencode.ai/zen/go/v1", "https://opencode.ai/zen/go/v1/"]) {
			expect(parseOpencodeGoBaseUrl(base)).toEqual({
				route: "zen",
				family: "unified",
				usageUrl: "https://opencode.ai/zen/go/v1/usage",
			});
		}
	});

	test("the Go inference roots all report usage at /inference/go/v1/usage", () => {
		const cases: Array<[string, string]> = [
			["https://opencode.ai/inference/go/v1", "unified"],
			["https://opencode.ai/inference/go/openai/v1", "openai"],
			["https://opencode.ai/inference/go/anthropic/v1/", "anthropic"],
			["https://opencode.ai/inference/go/google/v1beta", "google"],
		];
		for (const [base, family] of cases) {
			expect(parseOpencodeGoBaseUrl(base)).toEqual({
				route: "inference",
				family: family as never,
				usageUrl: "https://opencode.ai/inference/go/v1/usage",
			});
		}
	});

	test("rejects non-Go paths, other hosts and unsafe URL shapes", () => {
		for (const base of [
			"https://opencode.ai/zen/v1",
			"https://opencode.ai/inference/v1",
			"https://opencode.ai/inference/go",
			"https://opencode.ai/inference/go/openai/v2",
			"http://opencode.ai/zen/go/v1",
			"https://opencode.ai.evil.test/zen/go/v1",
			"https://opencode.ai:444/zen/go/v1",
			"https://key@opencode.ai/zen/go/v1",
			"https://opencode.ai/zen/go/v1?api_key=x",
			"https://example.com/inference/go/v1",
			"not a url",
			undefined,
		]) {
			expect(isOpencodeGoBaseUrl(base)).toBe(false);
		}
	});
});
