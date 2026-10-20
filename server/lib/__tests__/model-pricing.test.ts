import { afterEach, describe, expect, test } from "bun:test";
import { db } from "@server/db";
import { apiRequests } from "@server/db/schema";
import { decimalValue, type RawMetadata } from "@shared/model-catalog/card";
import type { RawLocalState } from "@shared/model-catalog/schema/catalog";
import { eq } from "drizzle-orm";
import { finishApiRequest, startApiRequest, trackApiRequest } from "../api-request-tracker";
import {
	bindModelCatalogSettings,
	getEffectiveModelProfile,
	getModelCardSnapshot,
	mutateModelCard,
	withModelMetadataSnapshotIterator,
} from "../model-catalog";
import { captureReferencePricingSnapshot, resolveModelPricing } from "../model-pricing";
import { saveSettings, settings } from "../settings";
import type { NarraForkSettings } from "../settings/types";
import {
	buildUsageDataFromSnapshot,
	calculateCost,
	calculateCostDetailed,
	type UsageData,
} from "../usage-tracking";

afterEach(() => bindModelCatalogSettings(settings, () => saveSettings(settings)));

describe("normalized usage snapshot conversion", () => {
	test.each([
		undefined,
		0,
		17,
	])("input=%s preserves net counters or derives them from full prompt", (inputTokens) => {
		expect(
			buildUsageDataFromSnapshot({
				promptTokens: 100,
				inputTokens,
				cachedInputTokens: 40,
				cacheCreation5mTokens: 15,
				cacheCreation1hTokens: 5,
			}),
		).toMatchObject({
			inputTokens: inputTokens ?? 40,
			cachedInputTokens: 40,
			cacheCreationInputTokens: 20,
		});
	});
	test("cache-only usage does not turn cache into ordinary input", () => {
		expect(
			buildUsageDataFromSnapshot({
				promptTokens: 60,
				cachedInputTokens: 40,
				cacheCreation5mTokens: 20,
			}),
		).toMatchObject({ inputTokens: 0, cacheCreationInputTokens: 20 });
		expect(buildUsageDataFromSnapshot({ cacheCreation1hTokens: 20 })).toMatchObject({
			inputTokens: 0,
			cacheCreationInputTokens: 20,
		});
	});
});
type Prices = Partial<Record<"input" | "output" | "cacheRead" | "cacheWrite", string | null>>;
const PRICE_KEYS = {
	input: "input_cost_per_token",
	output: "output_cost_per_token",
	cacheRead: "cache_read_input_token_cost",
	cacheWrite: "cache_creation_input_token_cost",
} as const;
/** Fixture prices are written per million tokens, as people read them; the catalog stores per token. */
function perToken(prices: Prices, suffix = ""): RawMetadata {
	const raw: RawMetadata = {};
	for (const [field, value] of Object.entries(prices) as Array<[keyof Prices, string | null]>)
		raw[PRICE_KEYS[field] + suffix] = value === null ? null : decimalValue(value, -6);
	return raw;
}
function install(metadata?: RawMetadata, extra: Partial<RawLocalState> = {}) {
	const local: RawLocalState = {
		revision: 1,
		models: [
			{
				id: "cost-fixture",
				matches: { aliases: ["cost-alias"] },
				metadata: metadata ?? {},
			},
		],
		...extra,
	};
	bindModelCatalogSettings(
		{
			agent: {
				modelCatalog: {
					schemaVersion: 2,
					migrationVersion: 2,
					local,
					autoApply: false,
					pinnedVersion: null,
				},
			},
		} as NarraForkSettings,
		() => {},
	);
}
function patchFixture(set: Record<string, string | number | boolean | null>, reset?: string[]) {
	return mutateModelCard({
		baseRevision: getModelCardSnapshot().local.revision,
		action: "patch",
		target: "model",
		targetId: "cost-fixture",
		patch: { set, reset },
	});
}
function usage(partial: Partial<UsageData> = {}): UsageData {
	return { inputTokens: 1_000_000, outputTokens: 1_000_000, ...partial };
}
const allPrices = perToken({ input: "2", output: "8", cacheRead: "0.5", cacheWrite: "2.5" });
const estimate = (data = usage(), provider = "openai", model = "cost-fixture") =>
	calculateCostDetailed(data, provider, model);

describe("reference costs use effective catalog metadata", () => {
	test("stream start price snapshots survive external consumers and preserve captured unknown", async () => {
		const cases: Array<{
			pricing?: RawMetadata;
			status: string;
			cost: number | null;
		}> = [
			{ pricing: allPrices, status: "complete", cost: 10 },
			{ pricing: perToken({ input: "2" }), status: "partial", cost: 2 },
			{ status: "unknown", cost: null },
			{
				pricing: perToken({ input: "0", output: "0", cacheRead: "0", cacheWrite: "0" }),
				status: "complete",
				cost: 0,
			},
		];
		for (const fixture of cases) {
			install(fixture.pricing);
			const producer = withModelMetadataSnapshotIterator(async function* () {
				yield {
					type: "api_request_start" as const,
					provider: "openai",
					model: "cost-fixture",
					referencePricingSnapshot: captureReferencePricingSnapshot("cost-fixture"),
				};
			});
			const event = (await producer.next()).value;
			if (!event) throw new Error("Missing start event");
			// The consumer runs outside producer ALS, after a concurrent settings edit.
			patchFixture(
				perToken({ input: "20", output: "8", cacheRead: "0.5", cacheWrite: "2.5" }) as Record<
					string,
					string
				>,
			);
			expect(estimate().knownCost).toBe(28);
			const handle = startApiRequest(event);
			const next = startApiRequest({ provider: "openai", model: "cost-fixture" });
			try {
				expect(Object.isFrozen(handle.referencePricingSnapshot)).toBe(true);
				expect(handle.referencePricingSnapshot.localRevision).toBe(1);
				await finishApiRequest(handle, { usage: usage() });
				await finishApiRequest(next, { usage: usage() });
				expect(
					db.select().from(apiRequests).where(eq(apiRequests.id, handle.id)).get(),
				).toMatchObject({ costStatus: fixture.status, costUsd: fixture.cost });
				expect(
					db.select().from(apiRequests).where(eq(apiRequests.id, next.id)).get(),
				).toMatchObject({ costStatus: "complete", costUsd: 28 });
			} finally {
				db.delete(apiRequests).where(eq(apiRequests.id, handle.id)).run();
				db.delete(apiRequests).where(eq(apiRequests.id, next.id)).run();
				await producer.return();
			}
		}
	});
	test("tracked request and completion pricing share one snapshot across deferred edits", async () => {
		install(allPrices);
		patchFixture({ working_context_tokens: 100_000 });
		let release = () => {};
		let entered = () => {};
		const deferred = new Promise<void>((resolve) => {
			release = resolve;
		});
		const started = new Promise<void>((resolve) => {
			entered = resolve;
		});
		const options = { provider: "openai", model: "cost-fixture" };
		const pending = trackApiRequest(options, async () => {
			expect(getEffectiveModelProfile(options.model).contextWindow).toBe(100_000);
			entered();
			await deferred;
			expect(getEffectiveModelProfile(options.model).contextWindow).toBe(100_000);
			expect(estimate().knownCost).toBe(10);
			return { usage: usage() };
		});
		try {
			await started;
			patchFixture({ input_cost_per_token: "0.00002", working_context_tokens: 200_000 });
			expect(getEffectiveModelProfile(options.model).contextWindow).toBe(200_000);
			release();
			await pending;
			const first = db.select().from(apiRequests).where(eq(apiRequests.model, options.model)).all();
			expect(first.map((row) => row.costUsd)).toEqual([10]);
			await trackApiRequest(options, async () => {
				expect(getEffectiveModelProfile(options.model).contextWindow).toBe(200_000);
				return { usage: usage() };
			});
			const both = db.select().from(apiRequests).where(eq(apiRequests.model, options.model)).all();
			expect(both.map((row) => row.costUsd).sort((a, b) => (a ?? 0) - (b ?? 0))).toEqual([10, 28]);
		} finally {
			release();
			await pending;
			db.delete(apiRequests).where(eq(apiRequests.model, options.model)).run();
		}
	});
	test("new request records persist partial coverage without altering NUG metering", async () => {
		install(perToken({ input: "2" }));
		const request = startApiRequest({ provider: "nug", model: "cost-fixture" });
		try {
			await finishApiRequest(request, { usage: usage(), meterUsage: 17, meterUnit: "credits" });
			const row = db.select().from(apiRequests).where(eq(apiRequests.id, request.id)).get();
			expect(row).toMatchObject({
				costStatus: "partial",
				costUsd: 2,
				costMissingFields: ["output"],
				meterUsage: 17,
				meterUnit: "credits",
			});
		} finally {
			db.delete(apiRequests).where(eq(apiRequests.id, request.id)).run();
		}
	});
	test("input-only prices retain known amount but cannot claim full cost", () => {
		install(perToken({ input: "2" }));
		expect(estimate()).toMatchObject({
			status: "partial",
			knownCost: 2,
			missingFields: ["output"],
		});
		expect(calculateCost(usage(), "openai", "cost-fixture")).toBeNull();
	});
	test("actual cache usage requires its own price; unused missing fields do not", () => {
		install(perToken({ input: "2", output: "8" }));
		expect(estimate()).toMatchObject({ status: "complete", knownCost: 10 });
		expect(
			estimate(usage({ cachedInputTokens: 500_000, cacheCreationInputTokens: 100_000 })),
		).toMatchObject({
			status: "partial",
			knownCost: 10,
			missingFields: ["cacheRead", "cacheWrite"],
		});
	});
	test("explicit null clears inherited price, not a free rate", () => {
		install(allPrices, {
			overrides: [
				{
					target: "model",
					targetId: "cost-fixture",
					metadata: perToken({ input: null }),
				},
			],
		});
		expect(resolveModelPricing("cost-alias")?.input).toBeNull();
		expect(estimate()).toMatchObject({ status: "partial", knownCost: 8, missingFields: ["input"] });
		install(perToken({ input: null, output: null }));
		expect(estimate()).toMatchObject({ status: "unknown", knownCost: 0 });
	});
	test("explicit all-zero prices are complete and free including caches", () => {
		install(perToken({ input: "0", output: "0", cacheRead: "0", cacheWrite: "0" }));
		expect(
			estimate(usage({ cachedInputTokens: 100, cacheCreationInputTokens: 100 })),
		).toMatchObject({ status: "complete", knownCost: 0, missingFields: [] });
		expect(calculateCost(usage(), "openai", "cost-fixture")?.totalCost).toBe(0);
	});
	test("alias inherits custom override; reset clears it and republishing restores base", () => {
		install(allPrices, {
			overrides: [
				{
					target: "model",
					targetId: "cost-fixture",
					metadata: perToken({ input: "9" }),
				},
			],
		});
		expect(estimate(usage(), "openai", "cost-alias")).toMatchObject({
			status: "complete",
			knownCost: 17,
		});
		const reset = patchFixture({}, ["input_cost_per_token"]);
		expect(reset.local.overrides).toEqual([]);
		// Reset means inheritance even for creation-time fields; a local-only model has no input base.
		expect(estimate(usage(), "openai", "cost-alias")).toMatchObject({
			status: "partial",
			knownCost: 8,
			missingFields: ["input"],
		});
		const model = reset.local.models?.find((entry) => entry.id === "cost-fixture");
		if (!model) throw new Error("Missing fixture model");
		mutateModelCard({
			baseRevision: reset.local.revision,
			action: "upsert-model",
			model: { ...model, metadata: allPrices },
		});
		expect(estimate(usage(), "openai", "cost-alias").knownCost).toBe(10);
	});
	test("tombstone suppresses pricing immediately and restoration recovers it", () => {
		install(allPrices);
		mutateModelCard({
			baseRevision: getModelCardSnapshot().local.revision,
			action: "hide",
			target: "model",
			targetId: "cost-fixture",
		});
		expect(estimate(usage(), "openai", "cost-alias").status).toBe("unknown");
		mutateModelCard({
			baseRevision: getModelCardSnapshot().local.revision,
			action: "restore",
			target: "model",
			targetId: "cost-fixture",
		});
		expect(estimate(usage(), "openai", "cost-alias").knownCost).toBe(10);
	});
	test("variant price and explicit zero override are consumed", () => {
		install(allPrices, {
			variants: [
				{
					id: "cost-fixture-variant",
					modelId: "cost-fixture",
					providerKey: "openai",
					upstreamModelIds: ["variant-cost"],
					metadata: perToken({ input: "5" }),
				},
			],
			overrides: [
				{
					target: "variant",
					targetId: "cost-fixture-variant",
					metadata: perToken({ output: "0" }),
				},
			],
		});
		expect(estimate(usage(), "openai", "openai:variant-cost")).toMatchObject({
			status: "complete",
			knownCost: 5,
		});
	});
	test.each([
		"openai",
		"codex",
		"gemini",
		"nug",
		"anthropic",
	])("%s prices normalized disjoint input and cache components without another subtraction", (provider) => {
		install(allPrices);
		const data = usage({
			inputTokens: 1_000_000,
			outputTokens: 0,
			cachedInputTokens: 500_000,
			cacheCreationInputTokens: 100_000,
		});
		expect(estimate(data, provider).knownCost).toBe(2.5);
		const fullyCached = estimate(
			usage({ inputTokens: 0, outputTokens: 0, cachedInputTokens: 1_000_000 }),
			provider,
		);
		expect(fullyCached.inputCost).toBe(0);
		expect(fullyCached.cacheReadCost).toBe(0.5);
	});
	test("unknown models and empty usage do not look free", () => {
		install();
		expect(estimate().status).toBe("unknown");
		expect(estimate(usage({ inputTokens: 0, outputTokens: 0 })).status).toBe("unknown");
	});
	test("long context full mode uses prompt threshold and respects sparse/unknown/zero tiers", () => {
		install({
			...allPrices,
			long_context_input_token_threshold: 1_000_000,
			...perToken({ input: "4", output: "16" }, "_above_1000k_tokens"),
		});
		expect(estimate().knownCost).toBe(10); // exactly threshold stays base
		expect(estimate(usage({ inputTokens: 1_000_001 })).knownCost).toBeCloseTo(20.000004);
		patchFixture({
			input_cost_per_token_above_1000k_tokens: null,
			output_cost_per_token_above_1000k_tokens: "0",
		});
		expect(estimate(usage({ inputTokens: 1_000_001 }))).toMatchObject({
			status: "partial",
			knownCost: 0,
			missingFields: ["longContext.input"],
		});
	});
	test("cache TTL details still require cache-write pricing when aggregate is omitted", () => {
		install(perToken({ input: "2", output: "8" }));
		expect(estimate(usage({ cacheCreation1hInputTokens: 100 }))).toMatchObject({
			status: "partial",
			missingFields: ["cacheWrite"],
		});
	});
	test("unknown long-context threshold cannot silently use base price", () => {
		install({
			...allPrices,
			long_context_input_token_threshold: null,
			long_context_input_cost_multiplier: 2,
		});
		expect(estimate().status).toBe("unknown");
		expect(estimate().missingFields).toContain("longContext.thresholdTokens");
	});
	test.each([
		"anthropic",
		"openai",
		"codex",
		"gemini",
		"nug",
	])("%s threshold includes disjoint cache read and write tokens", (provider) => {
		install({
			...allPrices,
			long_context_input_token_threshold: 100,
			...perToken({ input: "4" }, "_above_100_tokens"),
		});
		const result = estimate(
			usage({
				inputTokens: 50,
				cachedInputTokens: 30,
				cacheCreationInputTokens: 30,
				outputTokens: 0,
			}),
			provider,
		);
		expect(result.inputCost).toBeCloseTo(0.0002);
		expect(result.cacheReadCost).toBeCloseTo(0.000015);
	});
	test("marginal rules without verified reference allocation stay unknown", () => {
		install({
			...allPrices,
			long_context_input_token_threshold: 100,
			long_context_pricing_basis: "marginal",
			...perToken({ input: "4", output: "16" }, "_above_100_tokens"),
		});
		expect(estimate().status).toBe("unknown");
		expect(estimate().missingFields).toContain("longContext.mode");
	});
});
