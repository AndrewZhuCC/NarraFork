import { describe, expect, test } from "bun:test";
import { modelProfile } from "@shared/model-catalog/profile";
import type { ModelCardSnapshot } from "@shared/model-catalog/schema/api";
import { resolveCatalogEntry } from "./model-catalog-view";

const snapshot: ModelCardSnapshot = {
	schemaVersion: 2,
	catalog: {
		schemaVersion: 2,
		catalogVersion: "v2:test-v1",
		sourceVersion: "test-v1",
		publishedAt: "2026-01-01T00:00:00Z",
		models: [
			{
				id: "base",
				status: "legacy-unverified",
				metadata: {
					max_input_tokens: 1000,
					input_cost_per_token: "0.000002",
					output_cost_per_token: "0.000004",
				},
			},
		],
		variants: [
			{
				id: "variant",
				modelId: "base",
				providerKey: "channel-type",
				upstreamModelIds: ["opaque:upstream"],
				metadata: { max_input_tokens: 2000 },
			},
		],
	},
	local: {
		revision: 4,
		bindings: [
			{
				id: "actual",
				providerId: "local-provider",
				channelId: "local-channel",
				upstreamModelId: "opaque:upstream",
				variantId: "variant",
				overrides: { max_input_tokens: 3000 },
			},
		],
		overrides: [
			{
				target: "model",
				targetId: "base",
				metadata: { input_cost_per_token: "0", supports_web_search: null },
			},
		],
		hiddenModelIds: ["base"],
		hiddenVariantIds: ["variant"],
	},
	update: {
		activeVersion: "v2:test-v1",
		bundledVersion: "v2:test-v1",
		history: [],
		autoApply: false,
		pinnedVersion: null,
	},
};
describe("catalog management views", () => {
	test("a variant editor shows its layer, not a connection-level effective override", () => {
		const result = resolveCatalogEntry(snapshot, snapshot.catalog.variants[0]);
		expect(result.metadata.max_input_tokens).toBe(2000);
		expect(result.bindingId).toBeUndefined();
		expect(modelProfile(result).referencePricing).toMatchObject({ input: "0", output: "4" });
		expect(result.metadata.supports_web_search).toBeNull();
		expect(result.provenance.input_cost_per_token?.layer).toBe("local-model");
	});
	test("inspection does not restore tombstones or mark unverified presets as local", () => {
		const result = resolveCatalogEntry(snapshot, snapshot.catalog.models[0]);
		expect(snapshot.local.hiddenModelIds).toEqual(["base"]);
		expect(snapshot.local.hiddenVariantIds).toEqual(["variant"]);
		expect(result.provenance.max_input_tokens?.layer).toBe("preset-model");
		expect(result.provenance.max_input_tokens?.legacy).toBe(true);
	});
});
