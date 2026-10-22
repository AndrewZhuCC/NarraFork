import type {
	LocalProviderDetectResponse,
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
	/** Which tools have config directories on the server machine; reads no file. */
	detectLocalProviders: () =>
		request<LocalProviderDetectResponse>("/provider-import/local/detect", {
			signal: AbortSignal.timeout(LOCAL_IMPORT_TIMEOUT_MS),
			maxResponseBytes: LOCAL_IMPORT_MAX_RESPONSE_BYTES,
		}),
	/** Read other AI tools' provider settings on the server machine (masked keys only). */
	scanLocalProviders: () => post<LocalProviderScanResponse>("local/scan", {}),
	/** Fetch the real keys for the candidates the admin chose to import. */
	resolveLocalProviders: (ids: string[]) =>
		post<LocalProviderResolveResponse>("local/resolve", { ids }),
};
