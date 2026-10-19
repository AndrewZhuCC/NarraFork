/**
 * legacy-delegation-materializer.test.ts — delegations written before runtime
 * registrations get their relations stored once, and a second pass changes nothing.
 */

import { afterAll, beforeEach, describe, expect, mock, test } from "bun:test";
import { eq } from "drizzle-orm";
import { cleanDb, getTestDb } from "../../../tests/setup";
import { narratorMessages, narrators } from "../../db/schema";
import { hasRuntimePolicyTrait } from "../../lib/runtime-policy-trait";
import { readRuntimeRegistrationColumn } from "../../lib/validators/runtime-registration";

const { db, sqlite } = getTestDb();
const realDbModule = { ...(await import("../../db")) };
mock.module("../../db", () => ({ ...realDbModule, db, sqlite }));

const { materializeLegacyDelegations } = await import("../legacy-delegation-materializer");

afterAll(() => {
	mock.module("../../db", () => realDbModule);
	sqlite.close();
});

const now = "2026-10-19T00:00:00.000Z";

async function seed(id: string, values: Partial<typeof narrators.$inferInsert> = {}) {
	await db.insert(narrators).values({ id, status: "idle", createdAt: now, updatedAt: now, ...values });
}

async function linkedTurn(narratorId: string, toolUseId: string, createdAt: string) {
	await db.insert(narratorMessages).values({
		id: `${narratorId}-${toolUseId}`,
		narratorId,
		role: "user",
		contentJson: [],
		parentToolUseId: toolUseId,
		createdAt,
	});
}

function row(id: string) {
	return db.query.narrators.findFirst({ where: eq(narrators.id, id) });
}

describe("materializeLegacyDelegations", () => {
	beforeEach(async () => {
		cleanDb(sqlite);
		await seed("parent", { variant: "primary" });
	});

	test("stores controller, source, pool, latest mount, ceiling and base prompt", async () => {
		await seed("child", {
			type: "subagent",
			variant: "subagent:explore",
			parentNarratorId: "parent",
			systemPrompt: "Base.\n\n## Conversation Context\n\nold context",
		});
		await linkedTurn("child", "toolu_first", "2026-10-18T00:00:00.000Z");
		await linkedTurn("child", "toolu_latest", "2026-10-18T01:00:00.000Z");

		expect(await materializeLegacyDelegations()).toBe(1);
		const child = await row("child");
		expect(readRuntimeRegistrationColumn(child?.runtimeRegistrationJson)).toEqual({
			version: 1,
			modelPool: "explore",
			controller: { narratorId: "parent", actions: ["steer", "stop", "archive"] },
			historyPlacement: { toolUseId: "toolu_latest" },
		});
		expect(child?.policySourceNarratorId).toBe("parent");
		expect(hasRuntimePolicyTrait(child?.traits)).toBe(true);
		expect(child?.systemPrompt).toBe("Base.");

		expect(await materializeLegacyDelegations()).toBe(0);
	});

	test("a registered delegation missing its ceiling only gains the ceiling", async () => {
		const registration = { version: 1, controller: { narratorId: "parent", actions: ["stop"] } };
		await seed("registered", {
			type: "subagent",
			variant: "subagent:general",
			parentNarratorId: "parent",
			runtimeRegistrationJson: registration as never,
		});
		expect(await materializeLegacyDelegations()).toBe(1);
		const registered = await row("registered");
		expect(registered?.runtimeRegistrationJson).toEqual(registration as never);
		expect(registered?.policySourceNarratorId).toBeNull();
		expect(hasRuntimePolicyTrait(registered?.traits)).toBe(true);
	});

	test("only the given ids are touched; primaries never match", async () => {
		await seed("a", { type: "subagent", variant: "subagent:general", parentNarratorId: "parent" });
		await seed("b", { type: "subagent", variant: "subagent:general", parentNarratorId: "parent" });
		expect(await materializeLegacyDelegations(["a", "parent"])).toBe(1);
		expect((await row("a"))?.runtimeRegistrationJson).not.toBeNull();
		expect((await row("b"))?.runtimeRegistrationJson).toBeNull();
		expect((await row("parent"))?.runtimeRegistrationJson).toBeNull();
	});
});
