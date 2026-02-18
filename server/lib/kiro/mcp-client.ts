// MCP Client — ported from kiro.rs/src/kiro/provider.rs call_mcp_with_retry()

import { randomUUID } from "node:crypto";
import { effectiveApiRegion } from "./config";

import type { MultiTokenManager } from "./multi-token-manager";
import {
	KiroApiError,
	KiroQuotaError,
	type CallContext,
	type McpRequest,
	type McpResponse,
} from "./types";

const BASE_DELAY_MS = 200;
const MAX_DELAY_MS = 2000;

export class KiroMcpClient {
	constructor(private tokenManager: MultiTokenManager) {}

	async callTool(
		name: string,
		args: Record<string, unknown>,
	): Promise<McpResponse> {
		const body: McpRequest = {
			id: randomUUID(),
			jsonrpc: "2.0",
			method: "tools/call",
			params: { name, arguments: args },
		};
		return this.callMcpWithRetry(JSON.stringify(body));
	}

	private async callMcpWithRetry(body: string): Promise<McpResponse> {
		const maxRetries = this.tokenManager.maxRetries;
		let lastError: Error | null = null;

		for (let attempt = 0; attempt < maxRetries; attempt++) {
			let ctx: CallContext;
			try {
				ctx = await this.tokenManager.acquireContext(null);
			} catch (err) {
				lastError = err instanceof Error ? err : new Error(String(err));
				continue;
			}

			const headers = this.buildHeaders(ctx);
			const url = this.buildUrl(ctx);

			let res: Response;
			try {
				res = await fetch(url, {
					method: "POST",
					headers,
					body,
				});
			} catch (err) {
				lastError = err instanceof Error ? err : new Error(String(err));
				if (attempt + 1 < maxRetries) await this.delay(attempt);
				continue;
			}

			if (res.ok) {
				this.tokenManager.reportSuccess(ctx.id);
				return (await res.json()) as McpResponse;
			}

			const resBody = await res.text().catch(() => "");

			if (res.status === 400) {
				throw new KiroApiError("BAD_REQUEST", resBody, 400);
			}

			if (res.status === 402 && this.isMonthlyRequestLimit(resBody)) {
				const hasAvailable = this.tokenManager.reportQuotaExhausted(ctx.id);
				if (!hasAvailable) throw new KiroQuotaError(resBody);
				lastError = new KiroApiError("QUOTA_EXHAUSTED", resBody, 402);
				continue;
			}

			if (res.status === 401 || res.status === 403) {
				const hasAvailable = this.tokenManager.reportFailure(ctx.id);
				if (!hasAvailable) {
					throw new KiroApiError("AUTH_FAILED", resBody, res.status);
				}
				lastError = new KiroApiError("AUTH_FAILED", resBody, res.status);
				continue;
			}

			if (res.status === 408 || res.status === 429 || res.status >= 500) {
				lastError = new KiroApiError("TRANSIENT", resBody, res.status);
				if (attempt + 1 < maxRetries) await this.delay(attempt);
				continue;
			}

			throw new KiroApiError("API_ERROR", resBody, res.status);
		}

		throw lastError ?? new Error("MCP: max retries exceeded");
	}

	private buildUrl(ctx: CallContext): string {
		const config = this.tokenManager.currentConfig;
		const region = effectiveApiRegion(ctx.credentials, config);
		return `https://q.${region}.amazonaws.com`;
	}

	private buildHeaders(ctx: CallContext): Record<string, string> {
		const config = this.tokenManager.currentConfig;
		const region = effectiveApiRegion(ctx.credentials, config);
		const { kiroVersion } = config;

		return {
			"Content-Type": "application/x-amz-json-1.0",
			"x-amz-target": "AmazonCodeWhispererStreamingService.InvokeMCP",
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
