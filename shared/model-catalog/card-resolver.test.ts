import { describe, expect, test } from "bun:test";
import { legacyLocalToRaw } from "../../server/lib/model-catalog/legacy-v1";
import { decodeCatalog } from "../../server/lib/model-catalog/source";
import { type RawMetadata, rawCatalogFromModelFiles } from "./card";
import { normalizeRawLocalState, patchLocalCardMetadata } from "./card-local";
import { createModelCardResolver, type RawLocalState } from "./card-resolver";

const sourceVersion = "a".repeat(40);
const date = "2026-01-01T00:00:00Z";
const files = (metadata: RawMetadata, fields?: string[]) =>
	new Map([
		[
			"m.json",
			JSON.stringify({
				id: "m",
				metadata,
				matches: {
					prefixes: ["m-"],
					volatileSuffixes: true,
					...(fields === undefined ? {} : { fields }),
				},
				variants: [
					{
						id: "m@p",
						providerKey: "p",
						upstreamModelIds: ["up"],
						metadata: { supports_vision: false },
					},
				],
			}),
		],
	]);

describe("raw card resolution and persistence", () => {
	test("layers the complete metadata without erasing unknown siblings or presence", () => {
		const catalog = rawCatalogFromModelFiles(
			files({
				input_cost_per_token: 1e-6,
				max_input_tokens: 128000,
				max_output_tokens: 272000,
				future: { keep: [0, null, false, []], sibling: "source" },
			}),
			sourceVersion,
			date,
		);
		const resolve = createModelCardResolver(catalog, {
			revision: 7,
			models: [{ id: "m", metadata: { supported_modalities: [], future: { sibling: "local" } } }],
			variants: [
				{
					id: "m@p",
					modelId: "m",
					providerKey: "p",
					upstreamModelIds: ["up"],
					metadata: { working_context_tokens: 64000 },
				},
			],
			bindings: [
				{
					id: "b",
					channelId: "c",
					upstreamModelId: "up",
					variantId: "m@p",
					overrides: { input_cost_per_token: "0", output_cost_per_token: null },
				},
			],
		});
		const card = resolve(
			{ upstreamModelId: "up", providerKey: "p", channelId: "c" },
			{ supports_vision: true, input_cost_per_token: "0.000004" },
		);
		expect(card.schemaVersion).toBe(2);
		expect(card.localRevision).toBe(7);
		expect(card.metadata).toMatchObject({
			input_cost_per_token: "0",
			output_cost_per_token: null,
			supported_modalities: [],
			supports_vision: true,
			future: { keep: [0, null, false, []], sibling: "local" },
		});
		expect(card.provenance.input_cost_per_token?.layer).toBe("local-binding");
		expect(card.provenance["future.keep"]?.layer).toBe("preset-model");
		expect(card.provenance["future.sibling"]?.layer).toBe("local-model");
		expect(card.view.limits).toEqual({
			maxInputTokens: 128000,
			maxOutputTokens: 272000,
			workingContextTokens: 64000,
		});
		catalog.models[0]!.metadata.max_input_tokens = 1;
		expect(resolve({ upstreamModelId: "m" }).metadata.max_input_tokens).toBe(128000);
	});

	test("prefix matches and unbound legacy bindings do not borrow prices", () => {
		const metadata = {
			max_input_tokens: 1000,
			input_cost_per_token: 1e-6,
			provider_specific_entry: { modifier: 2 },
			future: { price: "3", capability: true },
		};
		const catalog = rawCatalogFromModelFiles(files(metadata), sourceVersion, date);
		const card = createModelCardResolver(catalog, {
			revision: 1,
			bindings: [
				{
					id: "window",
					channelId: "c",
					upstreamModelId: "m-experimental",
					overrides: { working_context_tokens: 800 },
				},
			],
		})({ upstreamModelId: "m-experimental", channelId: "c" });
		expect(card.bindingId).toBe("window");
		expect(card.metadata.max_input_tokens).toBe(1000);
		expect(card.metadata.working_context_tokens).toBe(800);
		expect(card.metadata.input_cost_per_token).toBeUndefined();
		expect(card.metadata.provider_specific_entry).toBeUndefined();
		expect(card.metadata.future).toEqual({ capability: true });
		const restricted = rawCatalogFromModelFiles(
			files(metadata, ["referencePricing.input"]),
			sourceVersion,
			date,
		);
		expect(
			createModelCardResolver(restricted)({ upstreamModelId: "m-experimental" }).metadata,
		).toEqual({ input_cost_per_token: 1e-6 });
	});

	test("legacy long-context filters do not leak the ordinary 1h cache duration price", () => {
		const catalog = rawCatalogFromModelFiles(
			files(
				{
					cache_creation_input_token_cost_above_1hr: 1e-5,
					input_cost_per_token_above_200k_tokens_priority: 4e-6,
				},
				["referencePricing.longContext"],
			),
			sourceVersion,
			date,
		);
		expect(
			createModelCardResolver(catalog)({ upstreamModelId: "m-experimental" }).metadata,
		).toEqual({ input_cost_per_token_above_200k_tokens_priority: 4e-6 });
	});

	test("old local units and ambiguous windows import without inventing vendor limits", () => {
		const local = legacyLocalToRaw({
			revision: 3,
			bindings: [
				{
					id: "b",
					upstreamModelId: "m",
					overrides: {
						limits: { contextWindow: 4096 },
						referencePricing: { input: "0", output: null },
						nativeSearch: { supported: false },
						modalities: { input: [] },
					},
				},
			],
		});
		expect(local.bindings?.[0]?.overrides).toEqual({
			working_context_tokens: 4096,
			input_cost_per_token: "0",
			output_cost_per_token: null,
			supports_web_search: false,
			supported_modalities: [],
		});
	});

	test("snapshots round-trip unknown-only metadata and old pinned snapshots import once", () => {
		const metadata = { future: { zero: 0, empty: [], unknown: null, negative: false } };
		const catalog = rawCatalogFromModelFiles(files(metadata), sourceVersion, date);
		expect(catalog.schemaVersion).toBe(2);
		expect(decodeCatalog(JSON.parse(JSON.stringify(catalog))).models[0]?.metadata).toEqual(
			metadata,
		);
		const legacy = decodeCatalog({
			schemaVersion: 1,
			catalogVersion: "old-pinned",
			publishedAt: date,
			models: [{ id: "old", metadata: { limits: { contextWindow: 2048 } } }],
			variants: [],
		});
		expect(legacy.schemaVersion).toBe(2);
		expect(legacy.sourceVersion).toBe("old-pinned");
		expect(legacy.models[0]?.metadata).toEqual({ legacy_context_window: 2048 });
		expect(decodeCatalog(JSON.parse(JSON.stringify(legacy)))).toEqual(legacy);
		expect(() => decodeCatalog({ ...catalog, sourceVersion: null })).toThrow();
		expect(() =>
			rawCatalogFromModelFiles(
				files({ nested: { api_key: "not-a-real-key" } }),
				sourceVersion,
				date,
			),
		).toThrow();
	});
});

test("canonical local scopes preserve effective values and reset the only write location", () => {
	const catalog = rawCatalogFromModelFiles(
		files({ input_cost_per_token: 1e-6 }),
		sourceVersion,
		date,
	);
	const input: RawLocalState = {
		revision: 4,
		hiddenVariantIds: ["m@p"],
		models: [{ id: "m", metadata: { input_cost_per_token: 2e-6, future: { keep: false } } }],
		bindings: [
			{ id: "b", upstreamModelId: "m", channelId: "c", overrides: { max_output_tokens: 100 } },
		],
		overrides: [
			{
				target: "model",
				targetId: "m",
				metadata: { input_cost_per_token: "0", output_cost_per_token: null },
				source: "legacy-local",
			},
			{ target: "binding", targetId: "b", metadata: { max_output_tokens: 200 }, source: "user" },
		],
	};
	const canonical = normalizeRawLocalState(input);
	expect(canonical.overrides).toEqual([]);
	expect(normalizeRawLocalState(canonical)).toEqual(canonical);
	expect(canonical.bindings?.[0]?.overrides?.max_output_tokens).toBe(200);
	const query = { upstreamModelId: "m", channelId: "c" };
	const before = createModelCardResolver(catalog, input)(query);
	const after = createModelCardResolver(catalog, canonical)(query);
	expect(after.metadata).toEqual(before.metadata);
	expect(after.provenance.input_cost_per_token?.legacy).toBe(true);
	const reset = patchLocalCardMetadata(catalog, canonical, "model", "m", {
		reset: ["input_cost_per_token"],
	});
	expect(reset.models?.[0]?.metadata.input_cost_per_token).toBeUndefined();
	expect(reset.overrides).toEqual([]);
	expect(reset.hiddenVariantIds).toEqual(input.hiddenVariantIds);
	expect(reset.revision).toBe(4);
	expect(createModelCardResolver(catalog, reset)(query).metadata.input_cost_per_token).toBe(1e-6);
	const edited = patchLocalCardMetadata(catalog, reset, "model", "m", {
		set: { output_cost_per_token: "0.000001" },
	});
	expect(
		createModelCardResolver(catalog, edited)(query).provenance.output_cost_per_token?.legacy,
	).toBeUndefined();
	expect(edited.models?.[0]?.metadata.future).toEqual({ keep: false });
	expect(
		patchLocalCardMetadata(catalog, edited, "model", "m", {
			set: { output_cost_per_token: "0.000001" },
		}),
	).toEqual(edited);
	expect(() =>
		patchLocalCardMetadata(catalog, edited, "model", "m", { set: { "future.keep": true } }),
	).toThrow();
	expect(input.models?.[0]?.metadata.input_cost_per_token).toBe(2e-6);
});

test("bindings for one connection compose; different selectors at one specificity stay ambiguous", () => {
	const catalog = rawCatalogFromModelFiles(
		files({ max_input_tokens: 1000, max_output_tokens: 500 }),
		sourceVersion,
		date,
	);
	const resolve = (bindings: RawLocalState["bindings"]) =>
		createModelCardResolver(catalog, { revision: 1, bindings })({
			upstreamModelId: "m",
			providerId: "p",
			channelId: "c",
		});
	const composed = resolve([
		{
			id: "legacy-window:p:m",
			providerId: "p",
			upstreamModelId: "m",
			overrides: { working_context_tokens: 800 },
		},
		{
			id: "editor",
			providerId: "p",
			upstreamModelId: "m",
			modelId: "m",
			overrides: { max_output_tokens: 100, working_context_tokens: 900 },
		},
	]);
	expect(composed.bindingId).toBe("editor");
	expect(composed.modelId).toBe("m");
	expect(composed.metadata.max_output_tokens).toBe(100);
	// The provider page's window merges last, so the page shows what is in effect.
	expect(composed.metadata.working_context_tokens).toBe(800);
	expect(composed.provenance.working_context_tokens?.id).toBe("legacy-window:p:m");
	expect(() =>
		resolve([
			{ id: "by-provider", providerId: "p", upstreamModelId: "m" },
			{ id: "by-channel", channelId: "c", upstreamModelId: "m" },
		]),
	).toThrow("Ambiguous binding");
	expect(() =>
		resolve([
			{ id: "a", providerId: "p", upstreamModelId: "m", modelId: "m" },
			{ id: "b", providerId: "p", upstreamModelId: "m", variantId: "m@p", modelId: "other" },
		]),
	).toThrow();
	// An attached window applies by name even when its stored selector no longer matches.
	const attached = createModelCardResolver(catalog, {
		revision: 1,
		bindings: [
			{
				id: "legacy-window:p:c:m",
				upstreamModelId: "c:m",
				overrides: { working_context_tokens: 700 },
			},
		],
	})({ upstreamModelId: "m", providerId: "p", channelId: "c" }, undefined, undefined, {
		attach: ["legacy-window:p:c:m"],
	});
	expect(attached.metadata.working_context_tokens).toBe(700);
	expect(attached.bindingId).toBeUndefined();
});
