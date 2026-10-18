/**
 * Step-2 contract: permissions, models and control come from explicit registrations.
 *
 * Every case runs twice with the same relationships:
 *  - "delegated": a child created through `createSubagent` (parent link + registrations);
 *  - "unrelated": two ordinary narrators, no parent link, given the same registrations.
 * Same configuration, same observed behaviour — nothing may depend on parent/variant.
 */
import { afterEach, describe, expect, test } from "bun:test";
import { FOLLOW_PARENT_MODEL } from "@shared/model-inheritance";
import { eq, inArray } from "drizzle-orm";
import { db } from "../../db";
import { chapters, narratorBlacklistCmds, narrators, projects, users } from "../../db/schema";
import { generateId } from "../../lib/id";
import { parseRuntimePolicyTraits, withRuntimePolicyTrait } from "../../lib/runtime-policy-trait";
import { settings } from "../../lib/settings";
import {
	CONTROLLER_ACTIONS,
	delegationRegistration,
} from "../../lib/validators/runtime-registration";
import { setRunHold } from "../agent-runtime/ownership";
import { compileDelegationPolicy, isRuntimeToolAllowed } from "../agent-runtime/policy";
import { executionPolicyRepository } from "../execution-policy/repository";
import { authorizeAgentControl } from "../narrator-control";
import { setPolicySource, setRuntimeRegistration } from "../narrator-runtime-registration";
import { narratorService } from "../narrator-service";
import { reviewService } from "../review-service";
import { resolveSubagentModelForRun } from "../subagent-model";
import { resetTraitLayerCaches, resolveNarratorRuntimePolicy } from "../trait-layer-service";

const A = "anthropic:claude-sonnet-4-20250514";
const B = "anthropic:claude-opus-4-20250514";
const C = "anthropic:claude-3-5-haiku-20241022";

const created: string[] = [];
const createdUsers: string[] = [];
const savedPools = structuredClone(settings.agent.subagentAllowedModels ?? {});

afterEach(async () => {
	resetTraitLayerCaches();
	settings.agent.subagentAllowedModels = structuredClone(savedPools);
	for (const id of created) setRunHold(id, undefined);
	const ids = created.splice(0);
	if (ids.length) {
		await db.delete(narratorBlacklistCmds).where(inArray(narratorBlacklistCmds.narratorId, ids));
		await db
			.update(narrators)
			.set({
				parentNarratorId: null,
				aclRootNarratorId: null,
				policySourceNarratorId: null,
				runtimeRegistrationJson: null,
			})
			.where(inArray(narrators.id, ids));
		await db.delete(narrators).where(inArray(narrators.id, ids));
	}
	const userIds = createdUsers.splice(0);
	if (userIds.length) await db.delete(users).where(inArray(users.id, userIds));
});

async function primary(model = A): Promise<string> {
	const row = await narratorService.create({ locale: "en", model });
	created.push(row.id);
	return row.id;
}

/**
 * - delegated: created through `createSubagent`;
 * - unrelated: two ordinary narrators given the same registrations, no parent link;
 * - legacy: a delegated row written before registrations existed (parent link and
 *   delegated variant only), resolved through the single compatibility rule.
 */
type Shape = "delegated" | "unrelated" | "legacy";

/** A source and a dependent related by `shape`; both shapes register the same facts. */
async function related(shape: Shape): Promise<{ source: string; dependent: string }> {
	const source = await primary();
	if (shape !== "unrelated") {
		const child = await narratorService.createSubagent({
			subagentOriginKind: "standalone",
			parentNarratorId: source,
			subagentType: "general",
			cwd: ".",
		});
		created.push(child.id);
		await narratorService.updateStatus(child.id, "idle");
		if (shape === "legacy")
			await db
				.update(narrators)
				.set({
					policySourceNarratorId: null,
					runtimeRegistrationJson: null,
					traits: ["standalone"],
				})
				.where(eq(narrators.id, child.id));
		return { source, dependent: child.id };
	}
	const dependent = await primary();
	await setPolicySource(dependent, source);
	await setRuntimeRegistration(dependent, delegationRegistration(source, "general"));
	await narratorService.updateModel(dependent, FOLLOW_PARENT_MODEL);
	// Same compiled ceiling the delegation template gives (stored on the row, never re-read).
	const child = await db.query.narrators.findFirst({
		where: eq(narrators.id, dependent),
		columns: { traits: true },
	});
	await db
		.update(narrators)
		.set({
			traits: withRuntimePolicyTrait(
				child?.traits,
				compileDelegationPolicy({ template: "general" }),
			),
		})
		.where(eq(narrators.id, dependent));
	return { source, dependent };
}

async function setTraits(id: string, traits: string[]) {
	await db.update(narrators).set({ traits }).where(eq(narrators.id, id));
	resetTraitLayerCaches();
}

const shapes: Shape[] = ["delegated", "unrelated", "legacy"];

describe.each(shapes)("%s session: permissions follow the registered source", (shape) => {
	test("the source's execution rules apply, and a tightening applies on the next load", async () => {
		const { source, dependent } = await related(shape);
		const before = executionPolicyRepository.loadNow(dependent);
		expect(before.inheritedNarratorIds).toEqual([dependent, source]);
		expect(before.commandBlacklist.some((rule) => rule.pattern === "rm*")).toBe(false);
		await db.insert(narratorBlacklistCmds).values({
			id: generateId(),
			narratorId: source,
			pattern: "rm*",
			createdAt: new Date().toISOString(),
		});
		const after = executionPolicyRepository.loadNow(dependent);
		expect(after.commandBlacklist.some((rule) => rule.pattern === "rm*")).toBe(true);
	});

	test("a capability tightening on the source reaches the dependent", async () => {
		const { source, dependent } = await related(shape);
		expect(isRuntimeToolAllowed(await resolveNarratorRuntimePolicy(dependent), "Write")).toBe(true);
		await setTraits(source, withRuntimePolicyTrait([], { version: 1, readOnly: true }));
		expect(isRuntimeToolAllowed(await resolveNarratorRuntimePolicy(dependent), "Write")).toBe(
			false,
		);
	});

	test("a permission-mode change propagates to registered dependents only", async () => {
		const { source, dependent } = await related(shape);
		const bystander = await primary();
		await narratorService.updatePermissionMode(source, "readOnly");
		const rows = await db.query.narrators.findMany({
			where: inArray(narrators.id, [dependent, bystander]),
			columns: { id: true, permissionMode: true },
		});
		expect(rows.find((row) => row.id === dependent)?.permissionMode).toBe("readOnly");
		expect(rows.find((row) => row.id === bystander)?.permissionMode).not.toBe("readOnly");
	});

	test("a source still in use cannot be deleted out from under its dependent", async () => {
		const { source, dependent } = await related(shape);
		if (shape !== "unrelated") return; // a source's own children are removed with it
		await expect(narratorService.remove(source)).rejects.toThrow("policy source");
		expect(executionPolicyRepository.loadNow(dependent).inheritedNarratorIds).toContain(source);
	});
});

describe.each(shapes)("%s session: model follows the registered source", (shape) => {
	test("follows the source and picks up a source switch on the next resolution", async () => {
		const { source, dependent } = await related(shape);
		const read = () => narratorService.getById(dependent);
		expect((await resolveSubagentModelForRun(await read())).model).toBe(A);
		await narratorService.updateModel(source, B);
		const next = await resolveSubagentModelForRun(await read());
		expect(next.model).toBe(B);
		expect(next.inheritance).toMatchObject({ source: "parent", parentModel: B });
	});

	test("the registered pool bounds a followed model and falls back to its first entry", async () => {
		const { source, dependent } = await related(shape);
		settings.agent.subagentAllowedModels = { ...savedPools, general: [C] };
		await narratorService.updateModel(source, B);
		const run = await resolveSubagentModelForRun(await narratorService.getById(dependent));
		expect(run.model).toBe(C);
		expect(run.inheritance).toMatchObject({ source: "pool-fallback", poolKey: "general" });
		settings.agent.subagentAllowedModels = { ...savedPools, general: [] };
		// A non-empty settings pool is required to restrict; an empty one means no pool.
		expect((await resolveSubagentModelForRun(await narratorService.getById(dependent))).model).toBe(
			B,
		);
	});

	test("a pinned model is not overridden by the source", async () => {
		const { source, dependent } = await related(shape);
		await narratorService.updateModel(dependent, C);
		await narratorService.updateModel(source, B);
		const run = await resolveSubagentModelForRun(await narratorService.getById(dependent));
		expect(run.model).toBe(C);
		expect(run.inheritance).toBeUndefined();
	});
});

test("a session with no registered source cannot follow one, even with a parent link", async () => {
	const parent = await primary();
	const orphan = await primary();
	// A fork-style parent link alone confers neither a model nor a policy source.
	await db.update(narrators).set({ parentNarratorId: parent }).where(eq(narrators.id, orphan));
	await narratorService.updateModel(orphan, FOLLOW_PARENT_MODEL);
	await expect(resolveSubagentModelForRun(await narratorService.getById(orphan))).rejects.toThrow(
		"registered source",
	);
	expect(executionPolicyRepository.loadNow(orphan).inheritedNarratorIds).toEqual([orphan]);
});

describe.each(shapes)("%s session: control is a separate, explicit grant", (shape) => {
	test("the registered controller may stop, steer and archive", async () => {
		const { source, dependent } = await related(shape);
		for (const action of CONTROLLER_ACTIONS)
			expect(
				(
					await authorizeAgentControl({
						callerNarratorId: source,
						targetNarratorId: dependent,
						action,
					})
				).id,
			).toBe(dependent);
	});

	test("siblings, subscribers and fork sources get no control", async () => {
		const { source, dependent } = await related(shape);
		// A sibling registered against the same source.
		const sibling = await primary();
		await setPolicySource(sibling, source);
		await setRuntimeRegistration(sibling, delegationRegistration(source, "general"));
		// A subscriber of the dependent's events.
		const subscriber = await primary();
		const { upsertNarratorEventSubscription } = await import("../narrator-runtime-registration");
		await upsertNarratorEventSubscription({
			sourceNarratorId: dependent,
			recipientNarratorId: subscriber,
			events: ["completed"],
			origin: "explicit",
		});
		// A fork source: parent link on an ordinary narrator, no registration.
		const forkSource = await primary();
		const fork = await primary();
		await db.update(narrators).set({ parentNarratorId: forkSource }).where(eq(narrators.id, fork));
		const denied = async (caller: string, target: string, action: "stop" | "archive") =>
			expect(
				authorizeAgentControl({ callerNarratorId: caller, targetNarratorId: target, action }),
			).rejects.toThrow();
		for (const action of ["stop", "archive"] as const) {
			await denied(sibling, dependent, action);
			await denied(subscriber, dependent, action);
			await denied(dependent, sibling, action);
			await denied(forkSource, fork, action);
		}
		const { narratorEventSubscriptions } = await import("../../db/schema");
		await db
			.delete(narratorEventSubscriptions)
			.where(eq(narratorEventSubscriptions.sourceNarratorId, dependent));
	});

	test("a grant limited to some actions does not imply the others", async () => {
		const { source, dependent } = await related(shape);
		await setRuntimeRegistration(dependent, {
			version: 1,
			modelPool: "general",
			controller: { narratorId: source, actions: ["steer"] },
		});
		await authorizeAgentControl({
			callerNarratorId: source,
			targetNarratorId: dependent,
			action: "steer",
		});
		await expect(
			authorizeAgentControl({
				callerNarratorId: source,
				targetNarratorId: dependent,
				action: "stop",
			}),
		).rejects.toThrow("not a direct child");
	});

	test("the controller cannot stop or archive while the user has taken the session over", async () => {
		const { source, dependent } = await related(shape);
		setRunHold(dependent, { released: false });
		for (const action of ["stop", "archive"] as const)
			await expect(
				authorizeAgentControl({ callerNarratorId: source, targetNarratorId: dependent, action }),
			).rejects.toThrow("taken over");
		// A steer (Send doInterrupt) stays authorized; its caller delivers without interrupting.
		await authorizeAgentControl({
			callerNarratorId: source,
			targetNarratorId: dependent,
			action: "steer",
		});
		setRunHold(dependent, undefined);
		await authorizeAgentControl({
			callerNarratorId: source,
			targetNarratorId: dependent,
			action: "stop",
		});
	});

	test("the controller's own ceiling and the acting user's ACL still bound control", async () => {
		const { source, dependent } = await related(shape);
		await setTraits(source, withRuntimePolicyTrait([], { version: 1, deny: ["spawnAgent"] }));
		await expect(
			authorizeAgentControl({
				callerNarratorId: source,
				targetNarratorId: dependent,
				action: "stop",
			}),
		).rejects.toThrow("capability ceiling");
		await setTraits(source, []);
		const [owner, stranger] = [generateId(), generateId()];
		createdUsers.push(owner, stranger);
		for (const id of [owner, stranger])
			await db.insert(users).values({
				id,
				username: `ctl-${id}`,
				passwordHash: "fixture",
				role: "user",
				createdAt: new Date().toISOString(),
			});
		// Delegated rows judge access by their ACL root (the source); make both private.
		await db
			.update(narrators)
			.set({ ownerUserId: owner, visibility: "private", writeAudience: "owner" })
			.where(inArray(narrators.id, [source, dependent]));
		await authorizeAgentControl({
			callerNarratorId: source,
			targetNarratorId: dependent,
			action: "stop",
			userId: owner,
		});
		await expect(
			authorizeAgentControl({
				callerNarratorId: source,
				targetNarratorId: dependent,
				action: "stop",
				userId: stranger,
			}),
		).rejects.toThrow("current user");
	});
});

describe("every creation path stores its ceiling on the row", () => {
	test("review conversion writes the compiled review ceiling and its relationships", async () => {
		const now = new Date().toISOString();
		const projectId = generateId();
		const [sourceChapterId, reviewChapterId] = [generateId(), generateId()];
		await db.insert(projects).values({
			id: projectId,
			name: "convert-ceiling",
			gitPath: `/tmp/nf-convert-${projectId.slice(0, 6)}`,
			createdAt: now,
			updatedAt: now,
		});
		await db.insert(chapters).values([
			{
				id: sourceChapterId,
				projectId,
				title: "source",
				status: "active",
				role: "branch",
				branch: "feat",
				baseBranch: "main",
				createdAt: now,
				updatedAt: now,
			},
			{
				id: reviewChapterId,
				projectId,
				title: "review",
				status: "active",
				role: "review",
				branch: "review/feat",
				baseBranch: "feat",
				reviewSourceChapterId: sourceChapterId,
				reviewStatus: "concluded",
				createdAt: now,
				updatedAt: now,
			},
		]);
		try {
			const source = await narratorService.create({ locale: "en", chapterId: sourceChapterId });
			const review = await narratorService.create({ locale: "en", chapterId: reviewChapterId });
			created.push(review.id, source.id);
			await reviewService.convertToSubagent(reviewChapterId);
			const row = await narratorService.getById(review.id);
			// Read from the row, not through the legacy compile-on-read fallback.
			expect(parseRuntimePolicyTraits(row.traits)).toEqual([
				compileDelegationPolicy({ template: "review" }),
			]);
			expect(row.policySourceNarratorId).toBe(source.id);
			expect(row.runtimeRegistrationJson).toMatchObject({
				modelPool: "review",
				controller: { narratorId: source.id },
			});
			expect((await resolveNarratorRuntimePolicy(review.id)).readOnlyGitShell).toBe(true);
		} finally {
			await db
				.update(narrators)
				.set({ chapterId: null })
				.where(inArray(narrators.chapterId, [sourceChapterId, reviewChapterId]));
			await db.delete(chapters).where(inArray(chapters.id, [reviewChapterId, sourceChapterId]));
			await db.delete(projects).where(eq(projects.id, projectId));
		}
	});
});

test("deleting a source with a surviving dependent fails before anything is removed", async () => {
	const { source, dependent: child } = await related("delegated");
	const survivor = await primary();
	await setPolicySource(survivor, source);
	await expect(narratorService.remove(source)).rejects.toThrow("policy source");
	// Refused up front: the delegated child was not deleted on the way to the refusal.
	const remaining = await db.query.narrators.findMany({
		where: inArray(narrators.id, [source, child, survivor]),
		columns: { id: true },
	});
	expect(remaining.map((row) => row.id).sort()).toEqual([source, child, survivor].sort());
	// The source's own child alone is not a blocker.
	await setPolicySource(survivor, null);
	await narratorService.remove(source);
	expect(
		await db.query.narrators.findMany({
			where: inArray(narrators.id, [source, child]),
			columns: { id: true },
		}),
	).toEqual([]);
});

test("a primary fork with a parent link is unaffected by permission-mode propagation", async () => {
	const source = await primary();
	const fork = await primary();
	await db.update(narrators).set({ parentNarratorId: source }).where(eq(narrators.id, fork));
	const before = (await narratorService.getById(fork)).permissionMode;
	await narratorService.updatePermissionMode(
		source,
		before === "readOnly" ? "default" : "readOnly",
	);
	expect((await narratorService.getById(fork)).permissionMode).toBe(before);
});
