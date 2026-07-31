import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { z } from "zod";

/**
 * Validation boundary for `PATCH /api/kiro/credentials/:id`.
 *
 * The region fields end up interpolated into request hosts
 * (`q.<region>.amazonaws.com`, `prod.<region>.auth.desktop.kiro.dev`), so the
 * schema must reject anything that is not a plain AWS region identifier.
 */

let updateCredentialSchema: z.ZodType<Record<string, unknown>>;
let testHome = "";
let originalNarraforkHome: string | undefined;

beforeAll(async () => {
	// The route module initialises the DB layer at import time; point it at a
	// throwaway home so it does not contend for the real ~/.narrafork instance.
	originalNarraforkHome = process.env.NARRAFORK_HOME;
	testHome = mkdtempSync(join(tmpdir(), "narrafork-kiro-patch-"));
	process.env.NARRAFORK_HOME = testHome;

	const mod = await import("../kiro");
	updateCredentialSchema = mod.updateCredentialSchema as unknown as typeof updateCredentialSchema;
});

afterAll(() => {
	if (originalNarraforkHome === undefined) delete process.env.NARRAFORK_HOME;
	else process.env.NARRAFORK_HOME = originalNarraforkHome;
	if (testHome) rmSync(testHome, { recursive: true, force: true });
});

describe("kiro credential patch schema", () => {
	test("accepts plain AWS region identifiers", () => {
		for (const region of ["us-east-1", "eu-west-1", "ap-northeast-1", "us-gov-west-1"]) {
			const parsed = updateCredentialSchema.safeParse({
				region,
				authRegion: region,
				apiRegion: region,
			});
			expect(parsed.success).toBe(true);
		}
	});

	test("accepts an empty region as an explicit clear", () => {
		const parsed = updateCredentialSchema.safeParse({ apiRegion: "" });
		expect(parsed.success).toBe(true);
	});

	test("rejects regions that could redirect the request host", () => {
		const hostile = [
			"us-east-1.attacker.example",
			"us-east-1/../evil",
			"US-EAST-1",
			"us_east_1",
			"us-east",
			"attacker.example",
			"us-east-1 ",
			"..",
		];
		for (const apiRegion of hostile) {
			const parsed = updateCredentialSchema.safeParse({ apiRegion });
			expect(parsed.success).toBe(false);
		}
	});

	test("rejects unknown fields", () => {
		// A strict schema keeps a typo'd key from silently doing nothing, and blocks
		// writes to credential fields the editor is not meant to touch.
		expect(updateCredentialSchema.safeParse({ accessToken: "at" }).success).toBe(false);
		expect(updateCredentialSchema.safeParse({ disabled: true }).success).toBe(false);
	});

	test("restricts authMethod to the canonical values", () => {
		for (const authMethod of ["social", "idc", "api_key"]) {
			expect(updateCredentialSchema.safeParse({ authMethod }).success).toBe(true);
		}
		for (const authMethod of ["apikey", "iam", "builder-id", "anything"]) {
			expect(updateCredentialSchema.safeParse({ authMethod }).success).toBe(false);
		}
	});

	test("priority must be a non-negative integer", () => {
		expect(updateCredentialSchema.safeParse({ priority: 0 }).success).toBe(true);
		expect(updateCredentialSchema.safeParse({ priority: 10 }).success).toBe(true);
		expect(updateCredentialSchema.safeParse({ priority: -1 }).success).toBe(false);
		expect(updateCredentialSchema.safeParse({ priority: 1.5 }).success).toBe(false);
	});

	test("bounds secret lengths", () => {
		expect(updateCredentialSchema.safeParse({ refreshToken: "x".repeat(8192) }).success).toBe(true);
		expect(updateCredentialSchema.safeParse({ refreshToken: "x".repeat(8193) }).success).toBe(
			false,
		);
	});
});
