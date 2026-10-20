/** Reference prices only: these amounts never configure or replace NUG billing. */

import type { ReferencePricingSnapshot } from "@shared/agent-protocol/types";
import type { ReferencePricing } from "@shared/model-catalog/profile";
import type { FieldSource, MatchedVia } from "@shared/model-catalog/schema/catalog";
import { getEffectiveModelProfile } from "./model-catalog";

export type PriceField = "input" | "output" | "cacheRead" | "cacheWrite";
export type ModelPricing = Record<PriceField, number | null>;
export interface ResolvedModelPricing extends ModelPricing {
	modelKey: string;
	matchedVia: MatchedVia;
	overridden: boolean;
	longContext?: ReferencePricing["longContext"];
	provenance: Record<string, FieldSource>;
}

/** Missing and explicit unknown prices remain null; only an explicit decimal zero is free. */
export function referencePriceNumber(value: unknown): number | null {
	if (typeof value !== "string" || !/^\d+(?:\.\d+)?$/.test(value)) return null;
	const number = Number(value);
	return Number.isFinite(number) && number >= 0 ? number : null;
}

/** Clone and freeze the price-only boundary value; null is an explicit captured unknown. */
export function cloneReferencePricingSnapshot(
	snapshot: ReferencePricingSnapshot,
): ReferencePricingSnapshot {
	const copy = structuredClone(snapshot);
	if (copy.referencePricing?.longContext) Object.freeze(copy.referencePricing.longContext);
	if (copy.referencePricing) Object.freeze(copy.referencePricing);
	return Object.freeze(copy);
}

export function captureReferencePricingSnapshot(model: string): ReferencePricingSnapshot {
	const profile = getEffectiveModelProfile(model);
	return cloneReferencePricingSnapshot({
		catalogVersion: profile.card.catalogVersion,
		localRevision: profile.card.localRevision,
		...(profile.card.modelId ? { modelId: profile.card.modelId } : {}),
		referencePricing: profile.referencePricing ?? null,
	});
}

export function pricingFromReferenceSnapshot(
	snapshot: ReferencePricingSnapshot,
): (ModelPricing & Pick<ResolvedModelPricing, "longContext">) | null {
	const prices = snapshot.referencePricing;
	if (!prices) return null;
	return {
		input: referencePriceNumber(prices.input),
		output: referencePriceNumber(prices.output),
		cacheRead: referencePriceNumber(prices.cacheRead),
		cacheWrite: referencePriceNumber(prices.cacheWrite),
		longContext: prices.longContext,
	};
}

/** Identity, variants, user overrides and tombstones are resolved in exactly one place. */
export function resolveModelPricing(model?: string): ResolvedModelPricing | null {
	if (!model?.trim()) return null;
	const profile = getEffectiveModelProfile(model);
	const prices = profile.referencePricing;
	if (!prices) return null;
	return {
		modelKey: profile.card.modelId ?? model,
		matchedVia: profile.card.matchedVia,
		overridden: profile.pricingOverridden,
		input: referencePriceNumber(prices.input),
		output: referencePriceNumber(prices.output),
		cacheRead: referencePriceNumber(prices.cacheRead),
		cacheWrite: referencePriceNumber(prices.cacheWrite),
		longContext: prices.longContext,
		provenance: structuredClone(profile.card.provenance),
	};
}
