import type { ModelCardMutation } from "@shared/model-catalog/card-local";
import type { ModelCardSnapshot } from "@shared/model-catalog/schema/api";
import type { ModelQuery } from "@shared/model-catalog/schema/catalog";
import { type QueryClient, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { modelCatalogApi } from "../lib/api/model-catalog";

export const modelCatalogKeys = {
	all: ["model-catalog"] as const,
	snapshot: ["model-catalog", "snapshot"] as const,
	card: (query?: ModelQuery) => ["model-catalog", "card", query] as const,
	cardModel: (model?: string) => ["model-catalog", "card-actual", model] as const,
};

/** One invalidation path for editors, version updates and binding changes. */
export async function invalidateModelCatalog(qc: QueryClient, snapshot?: ModelCardSnapshot) {
	if (snapshot) qc.setQueryData(modelCatalogKeys.snapshot, snapshot);
	await Promise.all([
		qc.invalidateQueries({ queryKey: modelCatalogKeys.all }),
		// Provider pages show the windows the catalog's bindings hold.
		qc.invalidateQueries({ queryKey: ["settings"] }),
		qc.invalidateQueries({ queryKey: ["model-pricing"] }),
	]);
}
export function useModelCatalog(enabled = true) {
	return useQuery({
		queryKey: modelCatalogKeys.snapshot,
		queryFn: modelCatalogApi.snapshot,
		staleTime: 60_000,
		enabled,
	});
}
export function useResolvedModelCard(query?: ModelQuery) {
	return useQuery({
		queryKey: modelCatalogKeys.card(query),
		queryFn: () => modelCatalogApi.resolve(query as ModelQuery),
		enabled: !!query,
		staleTime: 60_000,
	});
}
/** The card a request for this full model id (`provider:model`) actually resolves to. */
export function useActualResolvedModelCard(model?: string) {
	return useQuery({
		queryKey: modelCatalogKeys.cardModel(model),
		queryFn: () => modelCatalogApi.resolveModel(model as string),
		enabled: !!model,
		staleTime: 60_000,
	});
}
export function useCatalogMutation() {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: (mutation: ModelCardMutation) => modelCatalogApi.mutate(mutation),
		onSuccess: (data) => invalidateModelCatalog(qc, data),
	});
}
export function useCatalogUpdate() {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: (
			input:
				| { action: "check" | "apply" | "rollback"; version?: string }
				| { settings: { autoApply?: boolean; pinnedVersion?: string | null } },
		) =>
			"settings" in input
				? modelCatalogApi.settings(input.settings)
				: modelCatalogApi.update(input.action, input.version),
		onSuccess: (data) => invalidateModelCatalog(qc, data),
	});
}
