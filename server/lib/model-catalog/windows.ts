/**
 * The provider page's view of context windows: for each provider entry, the value
 * requests use, where it comes from, and the two scopes the page can set it in.
 */
import type {
	ModelWindowInfo,
	ModelWindowSource,
	ModelWindowsResponse,
} from "@shared/model-catalog/schema/api";
import { parseModelId } from "@shared/model-id";
import { resolveModelContextWindow } from "../settings/provider";
import {
	connectionModelIds,
	getEffectiveModelProfile,
	getModelCatalogRevision,
	getModelWindowScopes,
	withModelMetadataSnapshot,
} from "./index";

export const MAX_WINDOW_MODELS = 500;

function describe(
	model: string,
	own: boolean,
): { contextWindow: number; source: ModelWindowSource } {
	const { provider, model: bare } = parseModelId(model);
	const resolved = resolveModelContextWindow(bare, provider ?? "", { own });
	const contextWindow = resolved.contextWindow ?? 0;
	switch (resolved.source) {
		case "provider":
			return { contextWindow, source: "provider" };
		case "fallback":
			return { contextWindow, source: "fallback" };
		case "builtin":
			return { contextWindow, source: "preset" };
	}
	const layer = getEffectiveModelProfile(model, { own }).contextWindowSource?.layer;
	const source: ModelWindowSource =
		layer === "local-connection"
			? "own"
			: layer === "local-binding"
				? "binding"
				: layer === "local-model" || layer === "local-variant"
					? "model"
					: layer === "discovered" || resolved.source === "catalog"
						? "discovered"
						: "preset";
	return { contextWindow, source };
}

export function describeModelWindows(models: string[]): ModelWindowsResponse {
	return withModelMetadataSnapshot(() => describeAll(models));
}
function describeAll(models: string[]): ModelWindowsResponse {
	const revision = getModelCatalogRevision();
	const connections = connectionModelIds();
	const windows = [...new Set(models.map((model) => model.trim()).filter(Boolean))].map(
		(model): ModelWindowInfo => {
			const scopes = getModelWindowScopes(model, connections);
			const current = describe(model, true);
			const inherited = scopes.own === undefined ? current : describe(model, false);
			return {
				model,
				...current,
				...(scopes.own !== undefined ? { own: scopes.own } : {}),
				inherited: inherited.contextWindow,
				inheritedSource: inherited.source,
				...(scopes.modelId ? { modelId: scopes.modelId } : {}),
				...(scopes.modelWide !== undefined ? { modelWide: scopes.modelWide } : {}),
				others: scopes.others,
			};
		},
	);
	return { revision, windows };
}
