/**
 * Runtime registration and subscriptions through the real database: policy sources
 * are followed live and fail closed, registrations are relationship-only, and two
 * narrators with no parent/child link can be related exactly like a delegation.
 */
import { afterEach, describe, expect, test } from "bun:test";
import { eq, inArray } from "drizzle-orm";
import { db } from "../../db";
import { narratorEventSubscriptions, narrators, users } from "../../db/schema";
import { generateId } from "../../lib/id";
import { DISABLED_TOOLS_TRAIT_PREFIX, upsertEncodedTrait } from "../../lib/narrator-custom-traits";
import { withRuntimePolicyTrait } from "../../lib/runtime-policy-trait";
import { MAX_POLICY_SOURCE_DEPTH } from "../../lib/runtime-source";
import { MAX_SUBSCRIPTIONS_PER_SOURCE } from "../../lib/validators/runtime-registration";
import { isRuntimeToolAllowed } from "../agent-runtime/policy";
import { customSubagentService } from "../custom-subagent-service";
import {
	deleteNarratorEventSubscription,
	getRuntimeRegistration,
	listSubscriptionsForRecipient,
	listSubscriptionsForSourceRun,
	setPolicySource,
	upsertNarratorEventSubscription,
} from "../narrator-runtime-registration";
import { narratorService } from "../narrator-service";
import {
	resetTraitLayerCaches,
	resolveNarratorCapabilities,
	resolveNarratorRuntimePolicy,
} from "../trait-layer-service";

const created: string[] = [];

afterEach(async () => {
	resetTraitLayerCaches();
	const ids = created.splice(0);
	if (ids.length === 0) return;
	await db
		.delete(narratorEventSubscriptions)
		.where(inArray(narratorEventSubscriptions.sourceNarratorId, ids));
	// Children reference parents; clear the links before deleting.
	await db
		.update(narrators)
		.set({
			parentNarratorId: null,
			aclRootNarratorId: null,
			runtimeRegistrationJson: null,
			policySourceNarratorId: null,
		})
		.where(inArray(narrators.id, ids));
	await db.delete(narrators).where(inArray(narrators.id, ids));
});

async function narrator(traits: string[] = []): Promise<string> {
	const row = await narratorService.create({ locale: "en" });
	created.push(row.id);
	if (traits.length) await db.update(narrators).set({ traits }).where(eq(narrators.id, row.id));
	return row.id;
}

async function setTraits(id: string, traits: string[]): Promise<void> {
	await db.update(narrators).set({ traits }).where(eq(narrators.id, id));
}

const denySpawn = withRuntimePolicyTrait([], { version: 1, deny: ["spawnAgent"] });

describe("policy source layer", () => {
	test("an unrelated narrator can be a policy source, and changes follow live", async () => {
		const source = await narrator();
		const target = await narrator();
		await setPolicySource(target, source);
		expect((await resolveNarratorRuntimePolicy(target)).capabilities.spawnAgent).toBe(true);

		await setTraits(source, denySpawn);
		expect((await resolveNarratorRuntimePolicy(target)).capabilities.spawnAgent).toBe(false);

		// Restrictions also follow, and stay boundaries the target cannot relax.
		await setTraits(
			source,
			upsertEncodedTrait([], DISABLED_TOOLS_TRAIT_PREFIX, {
				version: 1,
				tools: ["Bash"],
			}),
		);
		await setTraits(
			target,
			upsertEncodedTrait([], DISABLED_TOOLS_TRAIT_PREFIX, { version: 1, tools: [] }),
		);
		const effective = (await resolveNarratorCapabilities(target)).traits;
		expect(effective.enforced.disabledTools.has("Bash")).toBe(true);
	});

	test("the target's own ceiling cannot widen the source's", async () => {
		const source = await narrator(withRuntimePolicyTrait([], { version: 1, tools: ["Read"] }));
		const target = await narrator(
			withRuntimePolicyTrait([], { version: 1, tools: ["Read", "Write"] }),
		);
		await setPolicySource(target, source);
		const policy = await resolveNarratorRuntimePolicy(target);
		expect(isRuntimeToolAllowed(policy, "Read")).toBe(true);
		expect(isRuntimeToolAllowed(policy, "Write")).toBe(false);
	});

	test("cycles and missing sources are refused at registration and fail at resolution", async () => {
		const a = await narrator();
		const b = await narrator();
		await setPolicySource(b, a);
		await expect(setPolicySource(a, b)).rejects.toThrow("cycle");
		await expect(setPolicySource(a, a)).rejects.toThrow("cycle");
		// A cycle written behind the service's back still fails closed.
		await db.update(narrators).set({ policySourceNarratorId: b }).where(eq(narrators.id, a));
		await expect(resolveNarratorRuntimePolicy(a)).rejects.toThrow("cycle");

		// The column's FK refuses a missing source outright.
		expect(() =>
			db
				.update(narrators)
				.set({ policySourceNarratorId: "missing-source" })
				.where(eq(narrators.id, a))
				.run(),
		).toThrow();
	});

	test("chains deeper than the bound are refused", async () => {
		const ids: string[] = [];
		for (let i = 0; i <= MAX_POLICY_SOURCE_DEPTH; i++) ids.push(await narrator());
		for (let i = 1; i < ids.length; i++) {
			await db
				.update(narrators)
				.set({ policySourceNarratorId: ids[i - 1] })
				.where(eq(narrators.id, ids[i]));
		}
		await expect(resolveNarratorRuntimePolicy(ids.at(-1) as string)).rejects.toThrow("too deep");
	});

	test("an unreadable registration or ceiling never resolves as unrestricted", async () => {
		const id = await narrator(["custom-runtime-policy:not-json"]);
		await expect(resolveNarratorRuntimePolicy(id)).rejects.toThrow();
		await setTraits(id, []);
		await db
			.update(narrators)
			.set({ runtimeRegistrationJson: { version: 2 } as never })
			.where(eq(narrators.id, id));
		await expect(resolveNarratorRuntimePolicy(id)).rejects.toThrow();
	});
});

describe("delegation creation", () => {
	test("a created child stores its compiled ceiling and follows the parent live", async () => {
		const parent = await narrator();
		const child = await narratorService.createSubagent({
			subagentOriginKind: "standalone",
			parentNarratorId: parent,
			subagentType: "general",
			cwd: ".",
		});
		created.push(child.id);
		expect(await getRuntimeRegistration(child.id)).toMatchObject({
			modelPool: "general",
			controller: { narratorId: parent },
		});
		expect((await narratorService.getById(child.id)).policySourceNarratorId).toBe(parent);
		const before = await resolveNarratorRuntimePolicy(child.id);
		expect(before.capabilities.spawnAgent).toBe(false);
		expect(isRuntimeToolAllowed(before, "Write")).toBe(true);

		// The parent becomes read-only after creation: the child follows on its next resolve.
		await setTraits(parent, withRuntimePolicyTrait([], { version: 1, readOnly: true }));
		expect(isRuntimeToolAllowed(await resolveNarratorRuntimePolicy(child.id), "Write")).toBe(false);
	});

	test("every legacy delegated shape without a stored ceiling resolves restricted", async () => {
		const typeOnly = await narrator();
		await db
			.update(narrators)
			.set({ type: "subagent", variant: "primary", subagentType: null })
			.where(eq(narrators.id, typeOnly));
		const bareVariant = await narrator();
		await db.update(narrators).set({ variant: "subagent" }).where(eq(narrators.id, bareVariant));
		const typedExplore = await narrator();
		await db
			.update(narrators)
			.set({ type: "subagent", variant: "primary", subagentType: "explore" })
			.where(eq(narrators.id, typedExplore));
		for (const id of [typeOnly, bareVariant, typedExplore]) {
			const policy = await resolveNarratorRuntimePolicy(id);
			expect(policy.capabilities.spawnAgent).toBe(false);
			expect(policy.capabilities.askUserQuestion).toBe("disabled");
		}
		// Fallback template is "general"; an explicit legacy subagentType is honoured.
		expect(isRuntimeToolAllowed(await resolveNarratorRuntimePolicy(typeOnly), "Write")).toBe(true);
		expect(isRuntimeToolAllowed(await resolveNarratorRuntimePolicy(typedExplore), "Write")).toBe(
			false,
		);
	});

	test("custom templates compile once at creation; later edits or deletion do not move the ceiling", async () => {
		const parent = await narrator();
		const name = `ceiling-${Date.now()}`;
		const definition = {
			name,
			description: "fixture",
			toolAccess: "custom" as const,
			customTools: ["Read", "Write"],
			defaultModel: "",
			prompt: "fixture",
		};
		await customSubagentService.create(definition);
		try {
			const child = await narratorService.createSubagent({
				subagentOriginKind: "standalone",
				parentNarratorId: parent,
				subagentType: name,
				cwd: ".",
			});
			created.push(child.id);
			const expectCreationCeiling = async () => {
				const policy = await resolveNarratorRuntimePolicy(child.id);
				expect(policy.tools.builtin).toEqual(["Read", "Write"]);
				expect(isRuntimeToolAllowed(policy, "Grep")).toBe(false);
			};
			await expectCreationCeiling();
			await customSubagentService.update(name, { ...definition, customTools: ["Grep"] });
			await expectCreationCeiling();
			await customSubagentService.remove(name);
			await expectCreationCeiling();
		} finally {
			await customSubagentService.remove(name).catch(() => {});
		}
	});

	test("a custom template missing at creation compiles to read-only", async () => {
		const parent = await narrator();
		const child = await narratorService.createSubagent({
			subagentOriginKind: "standalone",
			parentNarratorId: parent,
			subagentType: "definition-that-does-not-exist",
			cwd: ".",
		});
		created.push(child.id);
		const policy = await resolveNarratorRuntimePolicy(child.id);
		expect(policy.tools.readOnly).toBe(true);
		expect(isRuntimeToolAllowed(policy, "Write")).toBe(false);
		expect(isRuntimeToolAllowed(policy, "Read")).toBe(true);
	});

	test("legacy delegated rows without a stored ceiling resolve their template", async () => {
		const id = await narrator();
		await db.update(narrators).set({ variant: "subagent:review" }).where(eq(narrators.id, id));
		const policy = await resolveNarratorRuntimePolicy(id);
		expect(policy.readOnlyGitShell).toBe(true);
		expect(policy.capabilities.askUserQuestion).toBe("disabled");
	});
});

describe("event subscriptions", () => {
	test("non-parent narrators can subscribe; one row per scope, updated in place", async () => {
		const source = await narrator();
		const recipient = await narrator();
		const first = await upsertNarratorEventSubscription({
			sourceNarratorId: source,
			recipientNarratorId: recipient,
			events: ["completed"],
			origin: "explicit",
		});
		const again = await upsertNarratorEventSubscription({
			sourceNarratorId: source,
			recipientNarratorId: recipient,
			events: ["failed", "cancelled"],
			wake: true,
			origin: "explicit",
		});
		expect(again.id).toBe(first.id);
		expect(again.events).toEqual(["failed", "cancelled"]);
		expect(again.wake).toBe(true);

		const runScoped = await upsertNarratorEventSubscription({
			sourceNarratorId: source,
			recipientNarratorId: recipient,
			runId: "run-1",
			events: ["completed"],
			origin: "delegation",
		});
		expect(runScoped.id).not.toBe(first.id);
		expect((await listSubscriptionsForSourceRun(source, "run-1")).map((s) => s.id).sort()).toEqual(
			[first.id, runScoped.id].sort(),
		);
		expect((await listSubscriptionsForSourceRun(source, "run-2")).map((s) => s.id)).toEqual([
			first.id,
		]);
		expect((await listSubscriptionsForRecipient(recipient)).items).toHaveLength(2);

		// Subscribing grants no relationship: no control or policy source appears.
		expect(await getRuntimeRegistration(recipient)).toBeNull();

		expect(await deleteNarratorEventSubscription(first.id)).toBe(true);
		expect(await deleteNarratorEventSubscription(first.id)).toBe(false);
	});

	test("a human registration is attributed to that human, not a caller-chosen id", async () => {
		const owner = generateId();
		await db.insert(users).values({
			id: owner,
			username: `sub-${owner}`,
			passwordHash: "fixture",
			createdAt: new Date().toISOString(),
		});
		try {
			const source = await narrator();
			const recipient = await narrator();
			const sub = await upsertNarratorEventSubscription(
				{
					sourceNarratorId: source,
					recipientNarratorId: recipient,
					events: ["completed"],
					origin: "explicit",
					authorizedByUserId: "someone-else",
				},
				{ userId: owner, isAdmin: true },
			);
			expect(sub.authorizedByUserId).toBe(owner);
		} finally {
			await db
				.delete(narratorEventSubscriptions)
				.where(eq(narratorEventSubscriptions.authorizedByUserId, owner));
			await db.delete(users).where(eq(users.id, owner));
		}
	});

	test("self-subscription is refused and the per-source cap is enforced", async () => {
		const source = await narrator();
		await expect(
			upsertNarratorEventSubscription({
				sourceNarratorId: source,
				recipientNarratorId: source,
				events: ["completed"],
				origin: "explicit",
			}),
		).rejects.toThrow("itself");
		const recipient = await narrator();
		const now = new Date().toISOString();
		await db.insert(narratorEventSubscriptions).values(
			Array.from({ length: MAX_SUBSCRIPTIONS_PER_SOURCE }, (_, i) => ({
				id: `cap-${source}-${i}`,
				sourceNarratorId: source,
				recipientNarratorId: recipient,
				runId: `run-${i}`,
				events: ["completed" as const],
				origin: "explicit" as const,
				createdAt: now,
				updatedAt: now,
			})),
		);
		await expect(
			upsertNarratorEventSubscription({
				sourceNarratorId: source,
				recipientNarratorId: recipient,
				events: ["completed"],
				origin: "explicit",
			}),
		).rejects.toThrow(`at most ${MAX_SUBSCRIPTIONS_PER_SOURCE}`);
	});
});
