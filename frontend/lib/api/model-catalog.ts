import type { ModelCard } from "@shared/model-catalog/card";
import type { ModelCardMutation } from "@shared/model-catalog/card-local";
import type { ModelCardSnapshot } from "@shared/model-catalog/schema/api";
import type { ModelQuery } from "@shared/model-catalog/schema/catalog";
import { request } from "./client";

export type { ModelCardSnapshot };

const root = "/model-catalog";
export const modelCatalogApi = {
	snapshot: () => request<ModelCardSnapshot>(`${root}/v2`),
	resolve: (query: ModelQuery) =>
		request<ModelCard>(`${root}/v2/resolve`, {
			method: "POST",
			body: JSON.stringify({ query }),
		}),
	resolveModel: (model: string) =>
		request<ModelCard & { resolvedQuery: ModelQuery }>(`${root}/v2/resolve`, {
			method: "POST",
			body: JSON.stringify({ model }),
		}),
	mutate: (mutation: ModelCardMutation) =>
		request<ModelCardSnapshot>(`${root}/v2/mutate`, {
			method: "POST",
			body: JSON.stringify(mutation),
		}),
	update: (action: "check" | "apply" | "rollback", version?: string) =>
		request<ModelCardSnapshot>(`${root}/updates/${action}`, {
			method: "POST",
			body: JSON.stringify(version ? { version } : {}),
		}),
	settings: (settings: { autoApply?: boolean; pinnedVersion?: string | null }) =>
		request<ModelCardSnapshot>(`${root}/updates/settings`, {
			method: "PATCH",
			body: JSON.stringify(settings),
		}),
};
