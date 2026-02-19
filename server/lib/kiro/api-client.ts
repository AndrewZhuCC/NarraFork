// Kiro API client — reverse-engineered from kiro CLI chat
// Multi-credential failover mode using MultiTokenManager

import { randomUUID } from "node:crypto";
import { mkdirSync, writeFileSync, appendFileSync } from "node:fs";
import { effectiveApiRegion } from "./config";
import type { MultiTokenManager } from "./multi-token-manager";
import {
	type CallContext,
	KiroApiError,
	type KiroChatRequest,
	type KiroModel,
	KiroQuotaError,
} from "./types";

const BASE_DELAY_MS = 200;
const MAX_DELAY_MS = 2000;

const DUMP_DIR = "/tmp/narrafork-kiro-dumps";
let dumpSeq = 0;
try {
	mkdirSync(DUMP_DIR, { recursive: true });
} catch {}

export class KiroApiClient {
	constructor(private tokenManager: MultiTokenManager) {}

	/** Send a chat request, return raw Response for streaming. */
	async sendChatRequest(request: KiroChatRequest, signal?: AbortSignal): Promise<Response> {
		const model = request.conversationState.currentMessage.userInputMessage.modelId;
		const maxRetries = this.tokenManager.maxRetries;
		let lastError: Error | null = null;

		for (let attempt = 0; attempt < maxRetries; attempt++) {
			let ctx: CallContext;
			try {
				ctx = await this.tokenManager.acquireContext(model);
			} catch (err) {
				lastError = err instanceof Error ? err : new Error(String(err));
				continue;
			}

			const agentMode =
				request.conversationState.currentMessage.userInputMessage.agentMode
				?? request.conversationState.agentTaskType
				?? "vibe";
			const headers = this.buildHeaders(ctx, attempt, maxRetries, agentMode);
			// Inject profileArn from credentials if not already set
			const body = { ...request };
			if (!body.profileArn && ctx.credentials.profileArn) {
				body.profileArn = ctx.credentials.profileArn;
			}
			const region = effectiveApiRegion(ctx.credentials, this.tokenManager.currentConfig);
			const url = `https://q.${region}.amazonaws.com`;

			let res: Response;
			const seq = String(++dumpSeq).padStart(4, "0");
			const ts = new Date().toISOString().replace(/[:.]/g, "-");
			const convId = request.conversationState.conversationId?.slice(0, 8) ?? "unknown";
			const dumpPath = `${DUMP_DIR}/${seq}_${ts}_${convId}`;
			try {
				// Dump request
				try {
					writeFileSync(`${dumpPath}_req.json`, JSON.stringify(body, null, 2));
				} catch {}

				res = await fetch(url, {
					method: "POST",
					headers,
					body: JSON.stringify(body),
					signal,
				});

				// Dump response metadata
				try {
					const resHeaders: Record<string, string> = {};
					res.headers.forEach((v, k) => { resHeaders[k] = v; });
					writeFileSync(
						`${dumpPath}_res_meta.json`,
						JSON.stringify({ status: res.status, headers: resHeaders }, null, 2),
					);
				} catch {}
			} catch (err) {
				lastError = err instanceof Error ? err : new Error(String(err));
				if (attempt + 1 < maxRetries) await this.delay(attempt);
				continue;
			}

			if (res.ok) {
				this.tokenManager.reportSuccess(ctx.id);
				return res;
			}

			const resBody = await res.text().catch(() => "");

			// Dump error response body
			try {
				writeFileSync(`${dumpPath}_res_body.txt`, resBody);
			} catch {}

			// 400: bad request, don't retry
			if (res.status === 400) {
				throw new KiroApiError("BAD_REQUEST", resBody, 400);
			}

			// 402 + quota exhausted
			if (res.status === 402 && this.isMonthlyRequestLimit(resBody)) {
				const hasAvailable = this.tokenManager.reportQuotaExhausted(ctx.id);
				if (!hasAvailable) throw new KiroQuotaError(resBody);
				lastError = new KiroApiError("QUOTA_EXHAUSTED", resBody, 402);
				continue;
			}

			// 401/403: credential failure, failover to next
			if (res.status === 401 || res.status === 403) {
				const hasAvailable = this.tokenManager.reportFailure(ctx.id);
				if (!hasAvailable) {
					throw new KiroApiError("AUTH_FAILED", resBody, res.status);
				}
				lastError = new KiroApiError("AUTH_FAILED", resBody, res.status);
				continue;
			}

			// 429/5xx: transient, retry with backoff (don't switch credential)
			if (res.status === 408 || res.status === 429 || res.status >= 500) {
				lastError = new KiroApiError("TRANSIENT", resBody, res.status);
				if (attempt + 1 < maxRetries) await this.delay(attempt);
				continue;
			}

			// Other client errors
			throw new KiroApiError("API_ERROR", resBody, res.status);
		}

		throw lastError ?? new Error("Max retries exceeded");
	}

	private buildHeaders(
		ctx: CallContext,
		attempt: number,
		maxRetries: number,
		agentMode: string,
	): Record<string, string> {
		const config = this.tokenManager.currentConfig;
		const region = effectiveApiRegion(ctx.credentials, config);
		const { kiroVersion } = config;
		const [os, osVersion] = config.systemVersion.split("#");

		return {
			"Content-Type": "application/x-amz-json-1.0",
			"x-amz-target": "AmazonCodeWhispererStreamingService.GenerateAssistantResponse",
			"x-amzn-codewhisperer-optout": "true",
			"x-amzn-kiro-agent-mode": agentMode,
			"User-Agent": `AmazonQ-For-CLI/Version/${kiroVersion}`,
			Host: `q.${region}.amazonaws.com`,
			"amz-sdk-invocation-id": randomUUID(),
			"amz-sdk-request": `attempt=${attempt + 1}; max=${maxRetries}`,
			Authorization: `Bearer ${ctx.token}`,
			"x-amzn-codewhisperer-os": os ?? "linux",
			"x-amzn-codewhisperer-os-version": osVersion ?? "unknown",
			"x-amzn-codewhisperer-ide": "kiro",
			"x-amzn-codewhisperer-ide-version": kiroVersion,
		};
	}

	private isMonthlyRequestLimit(body: string): boolean {
		if (body.includes("MONTHLY_REQUEST_COUNT")) return true;
		try {
			const json = JSON.parse(body);
			if (json?.reason === "MONTHLY_REQUEST_COUNT") return true;
			if (json?.error?.reason === "MONTHLY_REQUEST_COUNT") return true;
		} catch {}
		return false;
	}

	private delay(attempt: number): Promise<void> {
		const exp = Math.min(BASE_DELAY_MS * 2 ** attempt, MAX_DELAY_MS);
		const jitter = Math.random() * (exp / 4);
		return new Promise((r) => setTimeout(r, exp + jitter));
	}
}
