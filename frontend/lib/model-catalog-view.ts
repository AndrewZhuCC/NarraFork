import type { ModelCard, RawModel, RawVariant } from "@shared/model-catalog/card";
import { createModelCardResolver } from "@shared/model-catalog/card-resolver";
import type { ModelCardSnapshot } from "@shared/model-catalog/schema/api";

type Resolver = ReturnType<typeof createModelCardResolver>;
const inspectors = new WeakMap<ModelCardSnapshot, Resolver>();

/** Inspect a specific layer, not an unrelated concrete connection's effective card.
 * Hidden records remain inspectable; this does not alter actual availability. */
export function resolveCatalogEntry(
	snapshot: ModelCardSnapshot,
	entry: RawModel | RawVariant,
): ModelCard {
	let resolve = inspectors.get(snapshot);
	if (!resolve) {
		resolve = createModelCardResolver(snapshot.catalog, {
			...snapshot.local,
			hiddenModelIds: [],
			hiddenVariantIds: [],
			bindings: [],
			overrides: (snapshot.local.overrides ?? []).filter((o) => o.target !== "binding"),
		});
		inspectors.set(snapshot, resolve);
	}
	const variant = "modelId" in entry;
	return resolve({
		upstreamModelId: entry.id,
		...(variant
			? { modelId: entry.modelId, variantId: entry.id, providerKey: entry.providerKey }
			: { modelId: entry.id }),
	});
}
