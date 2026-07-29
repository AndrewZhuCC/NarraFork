import { describe, expect, it } from "bun:test";
import {
	containsKiroApiKey,
	kiroCredentialsFromImportPayload,
	kiroCredentialsFromImportText,
} from "../../../server/lib/kiro-import-text";

describe("kiroCredentialsFromImportText", () => {
	it("parses one ksk_ key per line", () => {
		const creds = kiroCredentialsFromImportText("ksk_aaa111\nksk_bbb222\n");
		expect(creds).toEqual([
			{ apiKey: "ksk_aaa111", authMethod: "api_key" },
			{ apiKey: "ksk_bbb222", authMethod: "api_key" },
		]);
	});

	it("parses comma-separated keys", () => {
		const creds = kiroCredentialsFromImportText("ksk_aaa111, ksk_bbb222");
		expect(creds.map((c) => c.apiKey)).toEqual(["ksk_aaa111", "ksk_bbb222"]);
	});

	it("accepts the legacy ksk- prefix", () => {
		const creds = kiroCredentialsFromImportText("ksk-legacy123");
		expect(creds).toEqual([{ apiKey: "ksk-legacy123", authMethod: "api_key" }]);
	});

	it("labels a credential with an email found on the same line", () => {
		const creds = kiroCredentialsFromImportText("user@example.com----pass----ksk_withemail");
		expect(creds).toEqual([
			{
				apiKey: "ksk_withemail",
				authMethod: "api_key",
				email: "user@example.com",
				displayName: "user@example.com",
			},
		]);
	});

	it("ignores blank lines and lines without a key", () => {
		const creds = kiroCredentialsFromImportText("\n# notes\nksk_only\n   \n");
		expect(creds.map((c) => c.apiKey)).toEqual(["ksk_only"]);
	});

	it("still parses JSON payloads, leaving key aliases for the plugin normalizer", () => {
		const creds = kiroCredentialsFromImportText('{"kiroApiKey":"ksk_json","authMethod":"api_key"}');
		expect(creds).toEqual([
			{ kiroApiKey: "ksk_json", authMethod: "api_key" } as (typeof creds)[number],
		]);
	});

	it("falls back to key extraction for malformed JSON", () => {
		const creds = kiroCredentialsFromImportText('{"kiroApiKey": "ksk_broken"');
		expect(creds).toEqual([{ apiKey: "ksk_broken", authMethod: "api_key" }]);
	});

	it("returns nothing for text without a key", () => {
		expect(kiroCredentialsFromImportText("no keys here")).toEqual([]);
		expect(kiroCredentialsFromImportText("   ")).toEqual([]);
	});
});

describe("kiroCredentialsFromImportPayload", () => {
	it("passes credential objects through untouched", () => {
		const record = { refreshToken: "rt-1", authMethod: "social" };
		expect(kiroCredentialsFromImportPayload([record])).toEqual([record]);
	});

	it("mixes objects and pasted key strings", () => {
		const creds = kiroCredentialsFromImportPayload([{ refreshToken: "rt-1" }, "ksk_mixed"]);
		expect(creds).toEqual([
			{ refreshToken: "rt-1" },
			{ apiKey: "ksk_mixed", authMethod: "api_key" },
		]);
	});

	it("ignores non-object, non-string entries", () => {
		expect(kiroCredentialsFromImportPayload([null, 42, undefined])).toEqual([]);
	});
});

describe("containsKiroApiKey", () => {
	it("detects keys regardless of position and repeated calls", () => {
		expect(containsKiroApiKey("prefix ksk_abc")).toBe(true);
		expect(containsKiroApiKey("prefix ksk_abc")).toBe(true);
		expect(containsKiroApiKey("ksk_only")).toBe(true);
		expect(containsKiroApiKey("kskabc")).toBe(false);
	});
});
