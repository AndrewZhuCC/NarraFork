// MCP Client — reverse-engineered from kiro CLI chat

import { randomUUID } from "node:crypto";
import {
	acquireSlot,
	acquireSlotPriority,
	onSuccess as concurrencySuccess,
	onThrottle as concurrencyThrottle,
	getBackoffMs,
	recordRequest,
	recordThrottle,
	releaseSlot,
	touchLastRequest,
} from "./concurrency-control";
import { effectiveApiRegion } from "./config";
import { classifyKiroCredentialTier, type MultiTokenManager } from "./multi-token-manager";
import { pfetch } from "./proxy";
import { isAccountSuspended, isMonthlyRequestLimit, isSuspiciousThrottle } from "./quota-check";
import {
	type CallContext,
	KiroApiError,
	KiroQuotaError,
	type McpRequest,
	type McpResponse,
} from "./types";

const BASE_DELAY_MS = 200;
const MAX_DELAY_MS = 2000;
const MAX_THROTTLE_RETRIES = 3;

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
		const failedCredentialIds = new Set<string>();
		let lastError: Error | null = null;

		for (let attempt = 0; attempt < maxRetries; attempt++) {
			throwIfAborted(signal);

			let ctx: CallContext;
			try {
				ctx = await this.tokenManager.acquireContext(null, failedCredentialIds);
			} catch (err) {
				lastError = err instanceof Error ? err : new Error(String(err));
				break;
			}
			throwIfAborted(signal);

			const headers = this.buildHeaders(ctx, attempt, maxRetries);
			const url = this.buildUrl(ctx);
			const tier = classifyKiroCredentialTier(ctx.credentials);

			// Inject profileArn from credentials if available
			let effectiveBody = body;
			if (ctx.credentials.profileArn) {
				const parsed = JSON.parse(body);
				parsed.profileArn = ctx.credentials.profileArn;
				effectiveBody = JSON.stringify(parsed);
			}

			let res: Response;
			const slotState = { held: false };
			try {
				await acquireSlot(ctx.id, tier, ctx.credentials.subscriptionTitle);
				slotState.held = true;
				res = await this.fetchWithThrottleRetry(
					() =>
						pfetch(url, {
							method: "POST",
							headers,
							body: effectiveBody,
							signal,
						}),
					ctx,
					slotState,
				);
			} catch (err) {
				if (slotState.held) releaseSlot(ctx.id);
				if (signal?.aborted || isAbortError(err)) throw new Error("Aborted");
				lastError = err instanceof Error ? err : new Error(String(err));
				if (attempt + 1 < maxRetries) await this.delay(attempt, signal);
				continue;
			}

			const releaseHeld = () => {
				if (!slotState.held) return;
				releaseSlot(ctx.id);
				slotState.held = false;
			};

			if (res.ok) {
				concurrencySuccess(ctx.id);
				releaseHeld();
				this.tokenManager.reportSuccess(ctx.id);
				return (await res.json()) as McpResponse;
			}

			const resBody = await res.text().catch(() => "");

			if (isSuspiciousThrottle(resBody)) {
				concurrencyThrottle(ctx.id);
				touchLastRequest(ctx.id);
				recordThrottle(ctx.id);
				releaseHeld();
				const hasFailoverCredential = this.tokenManager.hasAlternativeAvailableCredential(
					ctx.id,
					failedCredentialIds,
				);
				if (hasFailoverCredential) failedCredentialIds.add(ctx.id);
				this.tokenManager.reportSuspiciousThrottle(ctx.id, failedCredentialIds);
				lastError = new KiroApiError("SUSPICIOUS_THROTTLE", resBody, res.status);
				continue;
			}

			if (res.status === 400) {
				if (isMonthlyRequestLimit(resBody)) {
					releaseHeld();
					failedCredentialIds.add(ctx.id);
					const hasAvailable = this.tokenManager.reportQuotaExhausted(ctx.id);
					if (!hasAvailable) throw new KiroQuotaError(resBody);
					lastError = new KiroApiError("QUOTA_EXHAUSTED", resBody, 400);
					continue;
				}
				releaseHeld();
				throw new KiroApiError("BAD_REQUEST", resBody, 400);
			}

			if (res.status === 402 && isMonthlyRequestLimit(resBody)) {
				releaseHeld();
				failedCredentialIds.add(ctx.id);
				const hasAvailable = this.tokenManager.reportQuotaExhausted(ctx.id);
				if (!hasAvailable) throw new KiroQuotaError(resBody);
				lastError = new KiroApiError("QUOTA_EXHAUSTED", resBody, 402);
				continue;
			}

			if (res.status === 401 || res.status === 403) {
				releaseHeld();
				failedCredentialIds.add(ctx.id);
				const hasAvailable = this.tokenManager.reportFailure(
					ctx.id,
					isAccountSuspended(resBody) ? "account_suspended" : undefined,
				);
				if (!hasAvailable) {
					throw new KiroApiError("AUTH_FAILED", resBody, res.status);
				}
				lastError = new KiroApiError("AUTH_FAILED", resBody, res.status);
				continue;
			}

			if (res.status === 429) {
				releaseHeld();
				throw new KiroApiError("THROTTLED", resBody, 429);
			}

			if (res.status === 408 || res.status >= 500) {
				releaseHeld();
				lastError = new KiroApiError("TRANSIENT", resBody, res.status);
				if (attempt + 1 < maxRetries) await this.delay(attempt, signal);
				continue;
			}

			releaseHeld();
			throw new KiroApiError("API_ERROR", resBody, res.status);
		}

		throw lastError ?? new Error("MCP: max retries exceeded");
	}

	private async fetchWithThrottleRetry(
		fetchFn: () => Promise<Response>,
		ctx: CallContext,
		slotState: { held: boolean },
	): Promise<Response> {
		const tier = classifyKiroCredentialTier(ctx.credentials);
		for (let i = 0; ; i++) {
			recordRequest(ctx.id);
			const response = await fetchFn();
			if (response.status !== 429 || i >= MAX_THROTTLE_RETRIES) return response;

			const bodyText = await response.text().catch(() => "");
			if (isSuspiciousThrottle(bodyText)) {
				return new Response(bodyText, { status: 429, headers: response.headers });
			}

			if (i === 0) concurrencyThrottle(ctx.id);
			touchLastRequest(ctx.id);
			recordThrottle(ctx.id);
			releaseSlot(ctx.id);
			slotState.held = false;
			await new Promise((resolve) => setTimeout(resolve, getBackoffMs(ctx.id)));
			await acquireSlotPriority(ctx.id, tier, ctx.credentials.subscriptionTitle);
			slotState.held = true;
		}
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
