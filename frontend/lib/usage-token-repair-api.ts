import type {
	TokenRepairJob,
	TokenRepairModelPage,
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
	start: (includeReviewed = true) => post<{ job: TokenRepairJob }>("/jobs", { includeReviewed }),
	job: (id: string, signal?: AbortSignal) =>
		request<{ job: TokenRepairJob }>(`${base}/jobs/${encodeURIComponent(id)}`, { signal }),
	models: (id: string, cursor?: string, signal?: AbortSignal) =>
		request<TokenRepairModelPage>(
			`${base}/jobs/${encodeURIComponent(id)}/models${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ""}`,
			{ signal },
		),
	cancel: (id: string) => post<{ job: TokenRepairJob }>(`/jobs/${encodeURIComponent(id)}/cancel`),
	dryRun: (id: string, protocolOverrides: TokenRepairProtocolOverride[]) =>
		post<{ job: TokenRepairJob }>(`/jobs/${encodeURIComponent(id)}/dry-run`, { protocolOverrides }),
	apply: (id: string, previewVersion: string) =>
		post<{ job: TokenRepairJob }>(`/jobs/${encodeURIComponent(id)}/apply`, {
			previewVersion,
			confirm: true,
		}),
};
