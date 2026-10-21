import type { ModelCardMutation } from "@shared/model-catalog/card-local";
import type { ModelCardSnapshot, ModelWindowInfo } from "@shared/model-catalog/schema/api";
import type { ModelQuery } from "@shared/model-catalog/schema/catalog";
import {
	type QueryClient,
	useMutation,
	useQueries,
	useQuery,
	useQueryClient,
} from "@tanstack/react-query";
import { modelCatalogApi } from "../lib/api/model-catalog";

export const modelCatalogKeys = {
	all: ["model-catalog"] as const,
	snapshot: ["model-catalog", "snapshot"] as const,
	card: (query?: ModelQuery) => ["model-catalog", "card", query] as const,
	cardModel: (model?: string) => ["model-catalog", "card-actual", model] as const,
	window: (model: string) => ["model-catalog", "window", model] as const,
};

/** One invalidation path for editors, version updates and binding changes. */
export async function invalidateModelCatalog(qc: QueryClient, snapshot?: ModelCardSnapshot) {
	if (snapshot) qc.setQueryData(modelCatalogKeys.snapshot, snapshot);
	await Promise.all([
		qc.invalidateQueries({ queryKey: modelCatalogKeys.all }),
		// The settings payload carries catalog-derived values (e.g. builtin windows).
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

/**
 * Window rows on a provider page each ask for one model; requests made in the same tick
 * go to the server as one batch.
 */
type WindowWaiter = {
	resolve: (value: { info: ModelWindowInfo; revision: number }) => void;
	reject: (error: unknown) => void;
};
const windowWaiters = new Map<string, WindowWaiter[]>();
let windowFlushScheduled = false;
const WINDOW_BATCH = 500;
function flushWindows() {
	windowFlushScheduled = false;
	const batch = [...windowWaiters];
	windowWaiters.clear();
	for (let i = 0; i < batch.length; i += WINDOW_BATCH) {
		const chunk = batch.slice(i, i + WINDOW_BATCH);
		modelCatalogApi.windows(chunk.map(([model]) => model)).then(
			({ revision, windows }) => {
				const byModel = new Map(windows.map((info) => [info.model, info]));
				for (const [model, waiters] of chunk) {
					const info = byModel.get(model);
					for (const waiter of waiters)
						if (info) waiter.resolve({ info, revision });
						else waiter.reject(new Error(`No window for ${model}`));
				}
			},
			(error) => {
				for (const [, waiters] of chunk) for (const waiter of waiters) waiter.reject(error);
			},
		);
	}
}
function loadModelWindow(model: string) {
	return new Promise<{ info: ModelWindowInfo; revision: number }>((resolve, reject) => {
		const waiters = windowWaiters.get(model) ?? [];
		waiters.push({ resolve, reject });
		windowWaiters.set(model, waiters);
		if (windowFlushScheduled) return;
		windowFlushScheduled = true;
		setTimeout(flushWindows, 0);
	});
}
/** One provider entry's context window, its source and the scopes that can set it. */
export function useModelWindow(model: string, enabled = true) {
	return useQuery({
		queryKey: modelCatalogKeys.window(model),
		queryFn: () => loadModelWindow(model),
		enabled: enabled && !!model,
		staleTime: 30_000,
	});
}
/** Several provider entries' windows, fetched in one batch. */
export function useModelWindows(models: string[]) {
	return useQueries({
		queries: models.map((model) => ({
			queryKey: modelCatalogKeys.window(model),
			queryFn: () => loadModelWindow(model),
			staleTime: 30_000,
		})),
	});
}
export function useSetModelWindow() {
	const qc = useQueryClient();
	return useMutation({
		mutationFn: modelCatalogApi.setWindow,
		onSuccess: (data) => invalidateModelCatalog(qc, data),
	});
}
