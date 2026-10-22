import type {
	LocalProviderResolveResponse,
	LocalProviderScanResponse,
} from "@shared/local-provider-import";
import { request } from "./client";

const LOCAL_IMPORT_TIMEOUT_MS = 30_000;
const LOCAL_IMPORT_MAX_RESPONSE_BYTES = 1024 * 1024;

function post<T>(path: string, data: unknown) {
	return request<T>(`/provider-import/${path}`, {
		method: "POST",
		body: JSON.stringify(data),
		signal: AbortSignal.timeout(LOCAL_IMPORT_TIMEOUT_MS),
		maxResponseBytes: LOCAL_IMPORT_MAX_RESPONSE_BYTES,
	});
}

export const providerImportApi = {
	/** Read other AI tools' provider settings on the server machine (masked keys only). */
	scanLocalProviders: () => post<LocalProviderScanResponse>("local/scan", {}),
	/** Fetch the real keys for the candidates the admin chose to import. */
	resolveLocalProviders: (ids: string[]) =>
		post<LocalProviderResolveResponse>("local/resolve", { ids }),
};
