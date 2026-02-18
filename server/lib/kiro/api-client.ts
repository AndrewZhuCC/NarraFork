// Kiro API client — ported from kiro.rs/src/kiro/provider.rs
// Multi-credential failover mode using MultiTokenManager

import { randomUUID } from "node:crypto";
import { effectiveApiRegion } from "./config";
import type { MultiTokenManager } from "./multi-token-manager";
import {
	KiroApiError,
	KiroQuotaError,
	type CallContext,
	type KiroChatRequest,
	type KiroModel,
} from "./types";

const BASE_DELAY_MS = 200;
const MAX_DELAY_MS = 2000;

export class KiroApiClient {
	constructor(private tokenManager: MultiTokenManager) {}

	/** Send a chat request, return raw Response for streaming. */
	async sendChatRequest(
		request: KiroChatRequest,
	): Promise<Response> {
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

			const headers = this.buildHeaders(ctx);
			// Inject profileArn from credentials if not already set
			const body = { ...request };
			if (!body.profileArn && ctx.credentials.profileArn) {
				body.profileArn = ctx.credentials.profileArn;
			}
			const region = effectiveApiRegion(ctx.credentials, this.tokenManager.currentConfig);
			const url = `https://q.${region}.amazonaws.com`;

			let res: Response;
			try {
				res = await fetch(url, {
					method: "POST",
					headers,
					body: JSON.stringify(body),
				});
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

	private buildHeaders(ctx: CallContext): Record<string, string> {
		const config = this.tokenManager.currentConfig;
		const region = effectiveApiRegion(ctx.credentials, config);
		const { kiroVersion } = config;

		return {
			"Content-Type": "application/x-amz-json-1.0",
			"x-amz-target": "AmazonCodeWhispererStreamingService.GenerateAssistantResponse",
			"x-amzn-codewhisperer-optout": "true",
			"x-amzn-kiro-agent-mode": "vibe",
			"User-Agent": `AmazonQ-For-CLI/Version/${kiroVersion}`,
			Host: `q.${region}.amazonaws.com`,
			"amz-sdk-invocation-id": randomUUID(),
			"amz-sdk-request": "attempt=1; max=3",
			Authorization: `Bearer ${ctx.token}`,
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
