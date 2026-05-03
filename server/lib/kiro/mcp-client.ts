// MCP Client — reverse-engineered from kiro CLI chat

import { randomUUID } from "node:crypto";
import { effectiveApiRegion } from "./config";
import type { MultiTokenManager } from "./multi-token-manager";
import { pfetch } from "./proxy";
import {
	type CallContext,
	KiroApiError,
	KiroQuotaError,
	type McpRequest,
	type McpResponse,
} from "./types";

const BASE_DELAY_MS = 200;
const MAX_DELAY_MS = 2000;

function isAbortError(err: unknown): boolean {
	return err instanceof Error && (err.name === "AbortError" || err.message === "Aborted");
}

function throwIfAborted(signal?: AbortSignal): void {
	if (signal?.aborted) throw new Error("Aborted");
}

export class KiroMcpClient {
	constructor(private tokenManager: MultiTokenManager) {}

	async callTool(
		name: string,
		args: Record<string, unknown>,
		signal?: AbortSignal,
	): Promise<McpResponse> {
		const body: McpRequest = {
			id: randomUUID(),
			jsonrpc: "2.0",
			method: "tools/call",
			params: { name, arguments: args },
		};
		return this.callMcpWithRetry(JSON.stringify(body), signal);
	}

	async listTools(signal?: AbortSignal): Promise<McpResponse> {
		const body: McpRequest = {
			id: randomUUID(),
			jsonrpc: "2.0",
			method: "tools/list",
			params: {},
		};
		return this.callMcpWithRetry(JSON.stringify(body), signal);
	}

	async callGeneric(
		method: string,
		params: Record<string, unknown>,
		signal?: AbortSignal,
	): Promise<McpResponse> {
		const body: McpRequest = {
			id: randomUUID(),
			jsonrpc: "2.0",
			method,
			params,
		};
		return this.callMcpWithRetry(JSON.stringify(body), signal);
	}

	private async callMcpWithRetry(body: string, signal?: AbortSignal): Promise<McpResponse> {
		const maxRetries = this.tokenManager.maxRetries;
		let lastError: Error | null = null;

		for (let attempt = 0; attempt < maxRetries; attempt++) {
			throwIfAborted(signal);

			let ctx: CallContext;
			try {
				ctx = await this.tokenManager.acquireContext(null);
			} catch (err) {
				lastError = err instanceof Error ? err : new Error(String(err));
				continue;
			}
			throwIfAborted(signal);

			const headers = this.buildHeaders(ctx, attempt, maxRetries);
			const url = this.buildUrl(ctx);

			// Inject profileArn from credentials if available
			let effectiveBody = body;
			if (ctx.credentials.profileArn) {
				const parsed = JSON.parse(body);
				parsed.profileArn = ctx.credentials.profileArn;
				effectiveBody = JSON.stringify(parsed);
			}

			let res: Response;
			try {
				res = await pfetch(url, {
					method: "POST",
					headers,
					body: effectiveBody,
					signal,
				});
			} catch (err) {
				if (signal?.aborted || isAbortError(err)) throw new Error("Aborted");
				lastError = err instanceof Error ? err : new Error(String(err));
				if (attempt + 1 < maxRetries) await this.delay(attempt, signal);
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
				if (attempt + 1 < maxRetries) await this.delay(attempt, signal);
				continue;
			}

			throw new KiroApiError("API_ERROR", resBody, res.status);
		}

		throw lastError ?? new Error("MCP: max retries exceeded");
	}

	private buildUrl(_ctx: CallContext): string {
		const config = this.tokenManager.currentConfig;
		const region = effectiveApiRegion(config);
		return `https://q.${region}.amazonaws.com`;
	}

	private buildHeaders(
		ctx: CallContext,
		attempt: number,
		maxRetries: number,
	): Record<string, string> {
		const config = this.tokenManager.currentConfig;
		const region = effectiveApiRegion(config);
		const { kiroVersion } = config;
		const [os, osVersion] = config.systemVersion.split("#");

		return {
			"Content-Type": "application/x-amz-json-1.0",
			"x-amz-target": "AmazonCodeWhispererStreamingService.InvokeMCP",
			"x-amzn-codewhisperer-optout": "true",
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

	private delay(attempt: number, signal?: AbortSignal): Promise<void> {
		const exp = Math.min(BASE_DELAY_MS * 2 ** attempt, MAX_DELAY_MS);
		const jitter = Math.random() * (exp / 4);
		const delayMs = exp + jitter;
		if (!signal) return new Promise((resolve) => setTimeout(resolve, delayMs));
		throwIfAborted(signal);
		return new Promise((resolve, reject) => {
			const onAbort = () => {
				clearTimeout(timer);
				reject(new Error("Aborted"));
			};
			const timer = setTimeout(() => {
				signal.removeEventListener("abort", onAbort);
				resolve();
			}, delayMs);
			signal.addEventListener("abort", onAbort, { once: true });
		});
	}
}
