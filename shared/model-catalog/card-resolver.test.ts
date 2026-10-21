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

describe("provider-entry (connection) overrides", () => {
	const catalog = () =>
		rawCatalogFromModelFiles(files({ max_input_tokens: 128000 }), sourceVersion, date);
	const query = { upstreamModelId: "m", providerId: "a" };

	test("apply by model value above every other local layer and never assert identity", () => {
		let local: RawLocalState = {
			revision: 0,
			bindings: [
				{
					id: "editor",
					providerId: "a",
					upstreamModelId: "m",
					overrides: { working_context_tokens: 90000, max_output_tokens: 4000 },
				},
			],
		};
		local = patchLocalCardMetadata(catalog(), local, "model", "m", {
			set: { working_context_tokens: 100000 },
		});
		local = patchLocalCardMetadata(catalog(), local, "connection", "a:m", {
			set: { working_context_tokens: 64000 },
		});
		const resolve = createModelCardResolver(catalog(), local);
		const own = resolve(query, undefined, undefined, "a:m");
		expect(own.metadata.working_context_tokens).toBe(64000);
		expect(own.metadata.max_output_tokens).toBe(4000);
		expect(own.provenance.working_context_tokens).toEqual({
			layer: "local-connection",
			id: "a:m",
			explicit: true,
		});
		expect(own.bindingId).toBe("editor");
		// Another provider entry of the same connection, or none, does not see it.
		expect(resolve(query, undefined, undefined, "b:m").metadata.working_context_tokens).toBe(90000);
		expect(resolve(query).metadata.working_context_tokens).toBe(90000);
	});

	test("normalization validates keys and drops emptied entries; only delete removes one", () => {
		expect(() =>
			normalizeRawLocalState({
				revision: 0,
				connections: { " a:m": { working_context_tokens: 1 } },
			}),
		).toThrow("Invalid provider model key");
		expect(() =>
			normalizeRawLocalState({ revision: 0, connections: { "a:m": { api_key: "x" } } }),
		).toThrow();
		expect(
			normalizeRawLocalState({ revision: 0, connections: { "a:m": {} } }).connections,
		).toBeUndefined();
		const set = patchLocalCardMetadata(catalog(), { revision: 0 }, "connection", "a:m", {
			set: { working_context_tokens: 64000 },
		});
		const reset = patchLocalCardMetadata(catalog(), set, "connection", "a:m", {
			reset: ["working_context_tokens"],
		});
		expect(reset.connections).toBeUndefined();
	});
});
