import type {
	TokenRepairApplyResult,
	TokenRepairCandidatePage,
	TokenRepairJob,
	TokenRepairProbe,
	TokenRepairProtocolOverride,
} from "@shared/usage-token-repair";
import { request } from "./api/client";

const base = "/usage-history/token-repair";
const post = <T>(path: string, body?: unknown) =>
	request<T>(`${base}${path}`, { method: "POST", body: JSON.stringify(body ?? {}) });
export const usageTokenRepairApi = {
	probe: (signal?: AbortSignal) => request<TokenRepairProbe>(`${base}/probe`, { signal }),
	jobs: (signal?: AbortSignal) =>
		request<{ job: TokenRepairJob | null }>(`${base}/jobs`, { signal }),
	start: (
		includeReviewed: boolean,
		protocolOverrides: TokenRepairProtocolOverride[] = [],
		sourceJobId?: string,
	) =>
		post<{ job: TokenRepairJob }>("/jobs", {
			includeReviewed,
			protocolOverrides,
			...(sourceJobId !== undefined ? { sourceJobId } : {}),
		}),
	job: (id: string, signal?: AbortSignal) =>
		request<{ job: TokenRepairJob }>(`${base}/jobs/${encodeURIComponent(id)}`, { signal }),
	candidates: (id: string, cursor?: string, signal?: AbortSignal) =>
		request<TokenRepairCandidatePage>(
			`${base}/jobs/${encodeURIComponent(id)}/candidates${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ""}`,
			{ signal },
		),
	cancel: (id: string) => post<{ job: TokenRepairJob }>(`/jobs/${encodeURIComponent(id)}/cancel`),
	apply: (id: string, candidateIds: string[]) =>
		post<TokenRepairApplyResult>(`/jobs/${encodeURIComponent(id)}/apply`, {
			candidateIds,
			confirm: true,
		}),
};
