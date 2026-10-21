import { isRawPriceField, type ModelCard, modelCardView, validateRawMetadata } from "./card";
import type {
	FieldSource,
	ModelQuery,
	RawCatalog,
	RawLocalState,
	RawMetadata,
	RawModel,
} from "./schema/catalog";
import { createModelIdentityResolver } from "./src/index";

export type { RawLocalState } from "./schema/catalog";

/** Group names from MATCH_FIELD_GROUPS mapped onto the raw keys each one covers. */
const groupKeys: Readonly<Record<string, string>> = {
	"limits.maxOutputTokens": "max_output_tokens",
	"modalities.input": "supported_modalities",
	"modalities.output": "supported_output_modalities",
	"nativeSearch.supported": "supports_web_search",
	"reasoning.supported": "supports_reasoning",
	"reasoning.mode": "reasoning_mode",
	"reasoning.levels": "reasoning_effort_levels",
	"reasoning.canDisable": "can_disable_reasoning",
	"reasoning.defaultLevel": "default_reasoning_effort",
	"referencePricing.input": "input_cost_per_token",
	"referencePricing.output": "output_cost_per_token",
	"referencePricing.cacheRead": "cache_read_input_token_cost",
	"referencePricing.cacheWrite": "cache_creation_input_token_cost",
};
const contextKeys = [
	"max_input_tokens",
	"context_window",
	"working_context_tokens",
	"legacy_context_window",
];
const limitKeys = [...contextKeys, "max_output_tokens"];
const longContextKey = (path: string) =>
	/_above_\d+(?:k)?_tokens/.test(path) ||
	path.startsWith("long_context_") ||
	path === "reference_pricing_long_context" ||
	path.startsWith("reference_pricing_long_context.");
/** Whether a raw key belongs to a field group a weak match is allowed to borrow. */
export function fieldAllowed(path: string, group: string): boolean {
	if (path === group || path.startsWith(`${group}.`)) return true;
	if (group === "limits") return limitKeys.includes(path);
	if (group === "limits.contextWindow") return contextKeys.includes(path);
	if (group === "referencePricing") return isRawPriceField(path) || path === "currency";
	if (group.startsWith("referencePricing.longContext")) return longContextKey(path);
	if (group === "referencePricing.currency" || group === "referencePricing.unit")
		return path === "currency";
	if ((group === "modalities" || group === "modalities.input") && path === "supports_vision")
		return true;
	if (group === "reasoning.canDisable" && path === "supports_none_reasoning_effort") return true;
	if (group === "reasoning" && path === "supports_none_reasoning_effort") return true;
	return Object.entries(groupKeys).some(
		([leaf, key]) => key === path && (leaf === group || leaf.startsWith(`${group}.`)),
	);
}

/** Identity matching, local layering and the card view over one raw catalog/local pair. */
export function createModelCardResolver(
	catalogInput: RawCatalog,
	localInput: RawLocalState = { revision: 0 },
) {
	const catalog = structuredClone(catalogInput),
		local = structuredClone(localInput);
	for (const entry of [
		...catalog.models,
		...catalog.variants,
		...(local.models ?? []),
		...(local.variants ?? []),
	])
		entry.metadata = validateRawMetadata(entry.metadata);
	for (const entry of local.overrides ?? []) entry.metadata = validateRawMetadata(entry.metadata);
	for (const entry of local.bindings ?? [])
		if (entry.overrides !== undefined) entry.overrides = validateRawMetadata(entry.overrides);
	const connections = new Map(
		Object.entries(local.connections ?? {}).map(([model, metadata]) => [
			model,
			validateRawMetadata(metadata),
		]),
	);
	const identify = createModelIdentityResolver(catalog, local);
	const baseModels = new Map(catalog.models.map((entry) => [entry.id, entry]));
	const baseVariants = new Map(catalog.variants.map((entry) => [entry.id, entry]));
	const localModels = new Map((local.models ?? []).map((entry) => [entry.id, entry]));
	const localVariants = new Map((local.variants ?? []).map((entry) => [entry.id, entry]));
	/**
	 * `connection` is the full model value (`provider:model`) the query was derived from;
	 * that provider entry's own overrides apply by name, last.
	 */
	return (
		query: ModelQuery,
		discovered?: RawMetadata,
		defaults?: RawMetadata,
		connection?: string,
	): ModelCard => {
		const identity = identify(query);
		const binding = identity.binding;
		const matched = identity.matchedEntry;
		const weak = identity.weakVia !== undefined;
		const via = identity.weakVia;
		const metadata: RawMetadata = {},
			provenance: Record<string, FieldSource> = {};
		const clearSources = (path: string) => {
			for (const key of Object.keys(provenance))
				if (key === path || key.startsWith(`${path}.`)) delete provenance[key];
		};
		// Catalog and local layers were validated once above; only caller input is checked here.
		const merge = (
			value: RawMetadata | undefined,
			source: FieldSource,
			restrict = false,
			trusted = true,
		) => {
			if (value === undefined) return;
			const raw = trusted ? value : validateRawMetadata(value);
			const walk = (target: RawMetadata, data: RawMetadata, prefix: string) => {
				for (const [key, item] of Object.entries(data)) {
					const path = prefix ? `${prefix}.${key}` : key;
					if (
						item !== null &&
						typeof item === "object" &&
						!Array.isArray(item) &&
						Object.keys(item).length
					) {
						const before = target[key];
						const child =
							before && typeof before === "object" && !Array.isArray(before) ? before : {};
						walk(child, item, path);
						if (Object.keys(child).length) {
							delete provenance[path];
							target[key] = child;
						}
						continue;
					}
					if (restrict && weak) {
						const filters = matched?.matches?.fields;
						if (filters && !filters.some((filter) => fieldAllowed(path, filter))) continue;
						if (
							via === "prefix" &&
							isRawPriceField(path) &&
							!filters?.some((filter) => fieldAllowed(path, filter))
						)
							continue;
					}
					// Empty objects merge; explicit null and arrays replace, including empty arrays.
					if (
						item &&
						typeof item === "object" &&
						!Array.isArray(item) &&
						Object.hasOwn(target, key)
					)
						continue;
					clearSources(path);
					target[key] = structuredClone(item);
					provenance[path] = { ...source };
					if (local.legacyFields !== undefined && source.layer.startsWith("local-")) {
						delete provenance[path]!.legacy;
						if (local.legacyFields[`${source.layer.slice(6)}:${source.id}`]?.includes(path))
							provenance[path]!.legacy = true;
					}
				}
			};
			walk(metadata, raw, "");
		};
		const entrySource = (entry: RawModel, layer: FieldSource["layer"]): FieldSource => ({
			layer,
			id: entry.id,
			catalogVersion: catalog.catalogVersion,
			...(entry.status === "unverified" || entry.status === "legacy-unverified"
				? { legacy: true }
				: {}),
		});
		merge(defaults, { layer: "default" }, false, false);
		const model = identity.modelId && baseModels.get(identity.modelId),
			variant = identity.variantId && baseVariants.get(identity.variantId);
		if (model) merge(model.metadata, entrySource(model, "preset-model"), true);
		if (variant) merge(variant.metadata, entrySource(variant, "preset-variant"), true);
		merge(discovered, { layer: "discovered" }, false, false);
		const applyOverride = (target: "model" | "variant" | "binding", id: string) => {
			const entry = local.overrides?.find(
				(override) => override.target === target && override.targetId === id,
			);
			if (entry)
				merge(
					entry.metadata,
					{
						layer: `local-${target}`,
						id,
						explicit: true,
						...(entry.source === "legacy-local" ? { legacy: true } : {}),
					},
					target !== "binding",
				);
		};
		if (identity.modelId) {
			const entry = localModels.get(identity.modelId);
			if (entry)
				merge(entry.metadata, { layer: "local-model", id: entry.id, explicit: true }, true);
			applyOverride("model", identity.modelId);
		}
		if (identity.variantId) {
			const entry = localVariants.get(identity.variantId);
			if (entry)
				merge(entry.metadata, { layer: "local-variant", id: entry.id, explicit: true }, true);
			applyOverride("variant", identity.variantId);
		}
		if (binding) {
			merge(binding.overrides, { layer: "local-binding", id: binding.id, explicit: true });
			applyOverride("binding", binding.id);
		}
		const own = connection === undefined ? undefined : connections.get(connection);
		if (own) merge(own, { layer: "local-connection", id: connection, explicit: true });
		const card = {
			schemaVersion: 2,
			catalogVersion: catalog.catalogVersion,
			localRevision: local.revision,
			...(identity.modelId ? { modelId: identity.modelId } : {}),
			...(identity.variantId ? { variantId: identity.variantId } : {}),
			...(binding ? { bindingId: binding.id } : {}),
			matchedVia: identity.matchedVia,
			metadata: validateRawMetadata(metadata),
			provenance,
		} as ModelCard;
		// The display view (price rows, field groups) is derived on first read: validation
		// and runtime reads resolve many cards and never look at it. JSON, spread and
		// structuredClone all read it like a plain property.
		let view: ModelCard["view"] | undefined;
		Object.defineProperty(card, "view", {
			enumerable: true,
			configurable: true,
			get: () => {
				view ??= modelCardView(card.metadata);
				return view;
			},
		});
		return card;
	};
}
