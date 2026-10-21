export * from "../schema/api";
export * from "../schema/catalog";
export { MATCH_FIELD_GROUPS, validateRawCatalog } from "./validation";

import type {
	MatchedVia,
	ModelQuery,
	RawBinding,
	RawCatalog,
	RawLocalState,
	RawModel,
	RawVariant,
} from "../schema/catalog";
import { fail, validateComposedEntries, validateShape } from "./validation";

/** Only explicitly declared volatile suffixes are stripped; semantic suffixes and colons are opaque. */
function volatileBases(id: string): string[] {
	let result = id;
	const bases: string[] = [];
	for (;;) {
		const next = result.replace(
			/(?:-(?:\d{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01])|(?:20)?\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])|latest|preview))$/,
			"",
		);
		if (next === result) return bases;
		bases.push(next);
		result = next;
	}
}
type Entry = RawModel | RawVariant;
type Candidate = {
	entry: Entry;
	variant: boolean;
	via: MatchedVia;
	scope: number;
	rank: number;
	length: number;
};
function choose<T>(items: T[], message: string): T | undefined {
	if (items.length > 1) fail(message);
	return items[0];
}

/** Which definitions a query resolves to. Metadata layering is the card resolver's job. */
export interface ModelIdentity {
	modelId?: string;
	variantId?: string;
	/** The binding a caller should edit for this connection: an editor binding if any. */
	binding?: RawBinding;
	/** Every binding for this connection, in merge order (later wins). */
	bindings: RawBinding[];
	matchedVia: MatchedVia;
	/** The weak (prefix/suffix) rule that selected the definition, if any. */
	weakVia?: "prefix" | "suffix";
	/** The definition whose `matches.fields` restricts a weak match. */
	matchedEntry?: Entry;
}

/** Validate and index an immutable catalog/local pair once, then resolve identities. */
export function createModelIdentityResolver(catalog: RawCatalog, local?: RawLocalState) {
	validateComposedEntries(catalog, local ?? { revision: 0 });
	const localModels = new Set((local?.models ?? []).map((m) => m.id));
	const localVariants = new Set((local?.variants ?? []).map((v) => v.id));
	const combinedModels = [
		...catalog.models.filter((m) => !localModels.has(m.id)),
		...(local?.models ?? []),
	];
	const combinedVariants = [
		...catalog.variants.filter((v) => !localVariants.has(v.id)),
		...(local?.variants ?? []),
	];
	const hiddenModels = new Set(local?.hiddenModelIds ?? []);
	const hiddenVariants = new Set(local?.hiddenVariantIds ?? []);
	const models = combinedModels.filter((m) => !hiddenModels.has(m.id));
	const variants = combinedVariants.filter(
		(v) => !hiddenVariants.has(v.id) && !hiddenModels.has(v.modelId),
	);
	const bindings = local?.bindings ?? [];
	const seenBindings = new Set<string>();
	for (const binding of bindings) {
		if (seenBindings.has(binding.id)) fail(`Duplicate binding ${binding.id}`);
		seenBindings.add(binding.id);
	}
	const seenOverrides = new Set<string>();
	for (const o of local?.overrides ?? []) {
		const key = JSON.stringify([o.target, o.targetId]);
		if (seenOverrides.has(key)) fail(`Duplicate override ${o.target}:${o.targetId}`);
		seenOverrides.add(key);
	}
	return (
		query: ModelQuery,
		options: {
			ignoreBindings?: boolean;
			/** Binding ids whose overrides apply to this request by name, not by selector. */
			attach?: string[];
		} = {},
	): ModelIdentity => {
		validateShape(query, "ModelQuery");
		const matchingBindings = options.ignoreBindings
			? []
			: bindings.filter(
					(b) =>
						b.upstreamModelId === query.upstreamModelId &&
						(b.providerId === undefined || b.providerId === query.providerId) &&
						(b.channelId === undefined || b.channelId === query.channelId),
				);
		const specificity = (b: RawBinding) =>
			Number(b.providerId !== undefined) + Number(b.channelId !== undefined);
		const maxSpecificity = Math.max(-1, ...matchingBindings.map(specificity));
		// Bindings that name exactly the same connection compose (e.g. the provider page's
		// working window beside a catalog-editor binding). Different selectors that match
		// at the same specificity are a genuine ambiguity.
		const top = matchingBindings.filter((b) => specificity(b) === maxSpecificity);
		const selector = (b: RawBinding) => JSON.stringify([b.providerId, b.channelId]);
		if (top.some((b) => selector(b) !== selector(top[0]!))) fail("Ambiguous binding");
		const agree = (field: "modelId" | "variantId") => {
			const values = [...new Set(top.map((b) => b[field]).filter((v) => v !== undefined))];
			if (values.length > 1) fail("Ambiguous binding");
			return values[0];
		};
		const boundModel = agree("modelId");
		const boundVariant = agree("variantId");
		// Provider-page windows merge last, so the page shows what is in effect.
		const window = (b: RawBinding) => Number(b.id.startsWith("legacy-window:"));
		const composed = [...top].sort((a, b) => window(a) - window(b));
		// Attached bindings add overrides only; identity stays with the selector match.
		for (const id of options.attach ?? []) {
			const attached = bindings.find((b) => b.id === id);
			if (attached && !composed.includes(attached)) composed.push(attached);
		}
		const binding = top.length ? composed[0] : undefined;
		let model: RawModel | undefined;
		let variant: RawVariant | undefined;
		let matchedVia: MatchedVia = "none";
		let matchedEntry: Entry | undefined;
		let weakVia: ModelIdentity["weakVia"];
		const explicitVariant = boundVariant ?? query.variantId;
		const explicitModel = boundModel ?? query.modelId;
		if (boundVariant && query.variantId && boundVariant !== query.variantId)
			fail("Binding conflicts with explicit variant");
		if (boundModel && query.modelId && boundModel !== query.modelId)
			fail("Binding conflicts with explicit model");
		if (explicitVariant !== undefined) {
			const identified =
				combinedVariants.find((v) => v.id === explicitVariant) ??
				fail(`Unknown variant ${explicitVariant}`);
			if (query.providerKey && query.providerKey !== identified.providerKey)
				fail("Variant provider scope mismatch");
			if (explicitModel && explicitModel !== identified.modelId)
				fail("Variant model identity mismatch");
			variant = variants.find((v) => v.id === explicitVariant);
			if (variant) {
				model = models.find((m) => m.id === variant!.modelId)!;
				matchedVia = "exact";
			}
		} else if (explicitModel !== undefined) {
			if (!combinedModels.some((m) => m.id === explicitModel))
				fail(`Unknown model ${explicitModel}`);
			model = models.find((m) => m.id === explicitModel);
			if (model) matchedVia = "exact";
		} else {
			const candidates: Candidate[] = [];
			// Rank suppressed identities too, so hiding an exact/stronger card cannot resurrect it through a weaker unrelated match.
			for (const entry of [...combinedModels, ...combinedVariants]) {
				const isVariant = "providerKey" in entry;
				if (isVariant && entry.providerKey !== query.providerKey) continue;
				const ids = [
					entry.id,
					...(entry.matches?.ids ?? []),
					...(isVariant ? (entry as RawVariant).upstreamModelIds : []),
				];
				const aliases = entry.matches?.aliases ?? [];
				const id = query.upstreamModelId;
				let via: MatchedVia = "none";
				let rank = 0;
				let length = 0;
				if (ids.includes(id)) {
					via = "exact";
					rank = 4;
				} else if (aliases.includes(id)) {
					via = "alias";
					rank = 3;
				} else if (
					entry.matches?.volatileSuffixes &&
					volatileBases(id).some((base) => [...ids, ...aliases].includes(base))
				) {
					via = "suffix";
					rank = 2;
				} else {
					length = Math.max(
						0,
						...(entry.matches?.prefixes ?? []).filter((p) => id.startsWith(p)).map((p) => p.length),
					);
					if (length) {
						via = "prefix";
						rank = 1;
					}
				}
				if (rank)
					candidates.push({
						entry,
						variant: isVariant,
						scope: isVariant ? 1 : 0,
						via,
						rank,
						length,
					});
			}
			candidates.sort((a, b) => b.scope - a.scope || b.rank - a.rank || b.length - a.length);
			const best = candidates[0];
			if (best) {
				if (
					candidates.some(
						(c) =>
							c !== best &&
							c.scope === best.scope &&
							c.rank === best.rank &&
							c.length === best.length,
					)
				)
					fail(`Ambiguous ${best.via} match for ${query.upstreamModelId}`);
				const suppressed = best.variant
					? hiddenVariants.has(best.entry.id) ||
						hiddenModels.has((best.entry as RawVariant).modelId)
					: hiddenModels.has(best.entry.id);
				if (!suppressed) {
					matchedEntry = best.entry;
					matchedVia = best.via;
					// Preserve weak-match restrictions even when a binding only supplies local overrides.
					if (best.variant) {
						variant = best.entry as RawVariant;
						model = models.find((m) => m.id === variant!.modelId)!;
					} else model = best.entry as RawModel;
					if (best.via === "prefix" || best.via === "suffix") weakVia = best.via;
				}
			}
		}
		return {
			...(model ? { modelId: model.id } : {}),
			...(variant ? { variantId: variant.id } : {}),
			...(binding ? { binding } : {}),
			bindings: composed,
			matchedVia: binding ? "binding" : matchedVia,
			...(weakVia ? { weakVia } : {}),
			...(matchedEntry ? { matchedEntry } : {}),
		};
	};
}
