import { describe, expect, test } from "bun:test";
import { signatureSourcesCompatible } from "../reasoning-source";

describe("signatureSourcesCompatible", () => {
	test("same source is compatible", () => {
		expect(signatureSourcesCompatible("nug:kiro", "nug:kiro")).toBe(true);
		expect(signatureSourcesCompatible("anthropic", "anthropic")).toBe(true);
	});

	test("different sources are incompatible", () => {
		// Same channelType (kiro) but different upstream servers.
		expect(signatureSourcesCompatible("nug:kiro", "nug:antigravity")).toBe(false);
		expect(signatureSourcesCompatible("nug:antigravity", "nug:kiro")).toBe(false);
		// Direct provider vs gateway channel.
		expect(signatureSourcesCompatible("anthropic", "nug:anthropic")).toBe(false);
		expect(signatureSourcesCompatible("kiro", "nug:kiro")).toBe(false);
	});

	test("missing stored source (legacy message) is incompatible", () => {
		expect(signatureSourcesCompatible(undefined, "nug:kiro")).toBe(false);
		expect(signatureSourcesCompatible(null, "nug:kiro")).toBe(false);
		expect(signatureSourcesCompatible("", "nug:kiro")).toBe(false);
	});

	test("missing current source is incompatible", () => {
		expect(signatureSourcesCompatible("nug:kiro", undefined)).toBe(false);
		expect(signatureSourcesCompatible("nug:kiro", null)).toBe(false);
		expect(signatureSourcesCompatible("nug:kiro", "")).toBe(false);
	});

	test("both missing is incompatible", () => {
		expect(signatureSourcesCompatible(undefined, undefined)).toBe(false);
	});
});
