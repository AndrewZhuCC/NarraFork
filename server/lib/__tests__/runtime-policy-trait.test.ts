import { describe, expect, test } from "bun:test";
import {
	compileDelegationPolicy,
	isRuntimeToolAllowed,
	resolveRuntimePolicy,
} from "../../services/agent-runtime/policy";
import {
	parseRuntimePolicyTraits,
	RUNTIME_POLICY_TRAIT_PREFIX,
	withRuntimePolicyTrait,
} from "../runtime-policy-trait";
import { needsLegacyDeliverTo, registrationBeforeRun, runDeliverToOf } from "../runtime-source";
import { resolveLayeredTraits } from "../trait-resolution";
import {
	MAX_SUBSCRIPTIONS_PER_SOURCE,
	narratorEventSubscriptionInputSchema,
	parseRuntimeRegistration,
	RUNTIME_REGISTRATION_MAX_BYTES,
	registerRun,
} from "../validators/runtime-registration";

const viaTraits = (template: string) =>
	resolveRuntimePolicy(
		parseRuntimePolicyTraits(withRuntimePolicyTrait([], compileDelegationPolicy({ template }))),
	);

describe("delegation templates compile to ordinary ceilings", () => {
	test("built-in templates keep their previous capability shape after a trait round trip", () => {
		const general = viaTraits("general");
		expect(general.tools.readOnly).toBe(false);
		expect(general.tools.mcp).toBe("nonDenied");
		expect(isRuntimeToolAllowed(general, "Write")).toBe(true);
		expect(isRuntimeToolAllowed(general, "Stop")).toBe(true);

		for (const name of ["explore", "plan"]) {
			const policy = viaTraits(name);
			expect(policy.tools.readOnly).toBe(true);
			expect(isRuntimeToolAllowed(policy, "Write")).toBe(false);
			expect(isRuntimeToolAllowed(policy, "Stop")).toBe(true);
		}

		const review = viaTraits("review");
		expect(review.readOnlyGitShell).toBe(true);
		expect(isRuntimeToolAllowed(review, "Stop")).toBe(false);

		const search = viaTraits("search");
		expect(search.searchOnly).toBe(true);
		expect(search.tools.mcp).toBe("none");
		expect(isRuntimeToolAllowed(search, "mcp__x__read", "readOnly")).toBe(false);

		for (const name of ["general", "explore", "plan", "review", "search", "missing"]) {
			const caps = viaTraits(name).capabilities;
			expect([caps.spawnAgent, caps.awaitAgent, caps.sendAwait, caps.planApproval]).toEqual([
				false,
				false,
				false,
				false,
			]);
			expect(caps.askUserQuestion).toBe("disabled");
			expect(caps.stopHooks).toBe(false);
			expect(caps.switchWorkingDirectory).toBe(false);
		}
		// Missing custom definitions fail closed to read-only.
		expect(viaTraits("missing").tools.readOnly).toBe(true);
	});

	test("customTools cannot re-open capabilities the template denies", () => {
		const policy = resolveRuntimePolicy([
			compileDelegationPolicy({
				template: "custom",
				customDefinition: {
					toolAccess: "custom",
					customTools: ["AskUserQuestion", "Agent", "ExitPlanMode", "Read"],
				},
			}),
		]);
		for (const name of ["AskUserQuestion", "Agent", "ExitPlanMode"])
			expect(isRuntimeToolAllowed(policy, name)).toBe(false);
		expect(isRuntimeToolAllowed(policy, "Read")).toBe(true);
	});

	test("an undeclared narrator keeps every capability", () => {
		const policy = resolveRuntimePolicy([]);
		expect(policy.tools.builtin).toBe("all");
		expect(policy.capabilities.askUserQuestion).toBe("sync-or-async");
		expect(isRuntimeToolAllowed(policy, "Agent")).toBe(true);
	});
});

describe("narrow-only merge", () => {
	test("any layer can only remove, never add", () => {
		const policy = resolveRuntimePolicy([
			{ version: 1, tools: ["Read", "Write"], deny: ["spawnAgent"] },
			{ version: 1, tools: ["Read", "Bash"], mcp: "readOnly" },
			{ version: 1, mcp: "nonDenied" },
		]);
		expect(policy.tools.builtin).toEqual(["Read"]);
		expect(policy.tools.mcp).toBe("readOnly");
		expect(policy.capabilities.spawnAgent).toBe(false);
	});

	test("read-only from any layer narrows a broad allowlist to read tools", () => {
		const policy = resolveRuntimePolicy([
			compileDelegationPolicy({ template: "general" }),
			{ version: 1, readOnly: true },
		]);
		expect(isRuntimeToolAllowed(policy, "Write")).toBe(false);
		expect(isRuntimeToolAllowed(policy, "Read")).toBe(true);
	});

	test("an unreadable ceiling is an error, never an empty ceiling", () => {
		expect(() => parseRuntimePolicyTraits([`${RUNTIME_POLICY_TRAIT_PREFIX}!!!`])).toThrow();
		const bad = Buffer.from(JSON.stringify({ version: 1, tools: "all" })).toString("base64url");
		expect(() => parseRuntimePolicyTraits([`${RUNTIME_POLICY_TRAIT_PREFIX}${bad}`])).toThrow();
		const unknown = Buffer.from(JSON.stringify({ version: 1, grant: ["x"] })).toString("base64url");
		expect(() => parseRuntimePolicyTraits([`${RUNTIME_POLICY_TRAIT_PREFIX}${unknown}`])).toThrow();
	});
});

describe("source trait layer", () => {
	const ceiling = withRuntimePolicyTrait([], { version: 1, deny: ["spawnAgent"] });

	test("a source's ceilings and restrictions carry over as boundaries", () => {
		const disabled = `custom-disabled-tools:${Buffer.from(
			JSON.stringify({ version: 1, tools: ["Bash"] }),
		).toString("base64url")}`;
		const resolved = resolveLayeredTraits({
			source: { traits: [...ceiling, disabled] },
			// The narrator states a non-enforced set without Bash: it cannot relax the source.
			narrator: {
				traits: [
					`custom-disabled-tools:${Buffer.from(
						JSON.stringify({ version: 1, tools: ["Write"] }),
					).toString("base64url")}`,
				],
			},
		});
		expect(resolved.enforced.disabledTools.has("Bash")).toBe(true);
		const policy = resolveRuntimePolicy(parseRuntimePolicyTraits(resolved.traits));
		expect(policy.capabilities.spawnAgent).toBe(false);
	});
});

describe("runtime registration contract", () => {
	test("accepts relationships and an explicit anonymous principal", () => {
		const parsed = parseRuntimeRegistration({
			version: 1,
			modelPool: "general",
			controller: { narratorId: "a", actions: ["stop"] },
			run: {
				logicalRunId: "run-1",
				cancelWith: { narratorId: "a", toolCallId: "tc", attempt: 0, runId: "run-0" },
				deliverTo: { narratorId: "a", toolUseId: "tu", toolCallId: "tc", attempt: 0 },
				principal: { userId: null },
				deadlineAt: "2030-01-01T00:00:00.000Z",
				historyStartSeq: 3,
			},
		});
		expect(parsed.run?.principal).toEqual({ userId: null });
	});

	test("rejects unknown fields, capability payloads and oversize values", () => {
		expect(() =>
			parseRuntimeRegistration({ version: 1, tools: ["Write"] } as unknown as never),
		).toThrow();
		expect(() =>
			parseRuntimeRegistration({ version: 1, controller: { narratorId: "a", actions: [] } }),
		).toThrow();
		// The policy source is a column now; the registration carries no source id.
		expect(() =>
			parseRuntimeRegistration({ version: 1, policySourceId: "a" } as unknown as never),
		).toThrow();
		expect(() =>
			parseRuntimeRegistration({
				version: 1,
				modelPool: "x".repeat(RUNTIME_REGISTRATION_MAX_BYTES),
			}),
		).toThrow();
	});

	test("subscription input dedupes events and requires at least one", () => {
		const parsed = narratorEventSubscriptionInputSchema.parse({
			sourceNarratorId: "a",
			recipientNarratorId: "b",
			events: ["completed", "completed", "failed"],
			origin: "explicit",
		});
		expect(parsed.events).toEqual(["completed", "failed"]);
		expect(parsed.wake).toBe(false);
		expect(() => narratorEventSubscriptionInputSchema.parse({ ...parsed, events: [] })).toThrow();
		expect(MAX_SUBSCRIPTIONS_PER_SOURCE).toBe(64);
	});
});

describe("run registration", () => {
	const deliverTo = { narratorId: "caller", toolUseId: "tu", toolCallId: "tc", attempt: 1 };
	const cancelWith = { narratorId: "caller", toolCallId: "tc", attempt: 1 };

	test("a new run replaces the previous run's relations; session grants stay", () => {
		const first = registerRun(
			{ version: 1, controller: { narratorId: "caller", actions: ["stop"] } },
			"run-1",
			{
				deliverTo,
				cancelWith,
				principal: { userId: null },
				deadlineAt: "2030-01-01T00:00:00.000Z",
			},
			4,
		);
		const second = registerRun(first, "run-2", { principal: { userId: "u" } }, 9);
		expect(second.controller).toEqual({ narratorId: "caller", actions: ["stop"] });
		expect(second.run).toEqual({
			logicalRunId: "run-2",
			principal: { userId: "u" },
			historyStartSeq: 9,
		});
	});

	test("re-registering the same run (recovery, detach) never restarts its deadline or boundary", () => {
		const first = registerRun(
			null,
			"run-1",
			{ deliverTo, cancelWith, deadlineAt: "2030-01-01T00:00:00.000Z" },
			4,
		);
		const detached = registerRun(
			first,
			"run-1",
			{ cancelWith: undefined, deadlineAt: "2099-01-01T00:00:00.000Z" },
			12,
		);
		expect(detached.run).toEqual({
			logicalRunId: "run-1",
			deliverTo,
			deadlineAt: "2030-01-01T00:00:00.000Z",
			historyStartSeq: 4,
		});
	});

	test("a legacy delegated row derives delivery once; a registered run is authoritative", () => {
		const legacy = { type: "subagent", variant: "subagent:general", parentNarratorId: "p" };
		expect(needsLegacyDeliverTo(legacy)).toBe(true);
		expect(runDeliverToOf(legacy, "linked")).toEqual({ narratorId: "p", toolUseId: "linked" });
		expect(registrationBeforeRun(legacy)?.controller?.narratorId).toBe("p");
		const registered = {
			...legacy,
			runtimeRegistrationJson: registerRun(null, "r", {}, 0),
		};
		expect(needsLegacyDeliverTo(registered)).toBe(false);
		expect(runDeliverToOf(registered, "linked")).toBeUndefined();
		// An ordinary session with the same registration behaves the same.
		expect(
			runDeliverToOf({ runtimeRegistrationJson: registerRun(null, "r", { deliverTo }, 0) }),
		).toEqual(deliverTo);
	});

	test("a registered row without a run delivers nowhere (plugin standalone worker)", () => {
		// Created through the delegation entry, so it has a delegated shape and a
		// registration — but no run has registered a destination yet.
		const worker = {
			type: "subagent",
			variant: "subagent:general",
			parentNarratorId: "p",
			runtimeRegistrationJson: { version: 1, controller: { narratorId: "p", actions: ["stop"] } },
		};
		expect(needsLegacyDeliverTo(worker)).toBe(false);
		expect(runDeliverToOf(worker, "linked")).toBeUndefined();
	});

	test("a legacy standalone transport link is never a delivery destination", () => {
		const legacyWorker = { type: "subagent", variant: "subagent:general", parentNarratorId: "p" };
		expect(runDeliverToOf(legacyWorker, "standalone-worker-1")).toBeUndefined();
		// A real linked tool call of an unregistered legacy delegation still derives.
		expect(runDeliverToOf(legacyWorker, "toolu_real")).toEqual({
			narratorId: "p",
			toolUseId: "toolu_real",
		});
	});
});
