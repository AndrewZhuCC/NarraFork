import { QueryClient } from "@tanstack/react-query";

/**
 * Editor fixtures mount FilePanelContent, whose large-file prompt reads platform and user
 * preferences through React Query. Seed both so the fixtures issue no extra requests.
 */
export function createSmokeEditorQueryClient(): QueryClient {
	const client = new QueryClient({
		defaultOptions: { queries: { retry: false, staleTime: Number.POSITIVE_INFINITY } },
	});
	client.setQueryData(["health"], { platform: "linux" });
	client.setQueryData(["user-preferences"], {});
	return client;
}
