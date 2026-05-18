// Kiro API client — reverse-engineered from kiro CLI chat
// Multi-credential failover mode using MultiTokenManager

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
import {
	isAccountSuspended,
	isContextLengthExceeded,
	isMonthlyRequestLimit,
	isSuspiciousThrottle,
} from "./quota-check";
import { type CallContext, KiroApiError, type KiroChatRequest, KiroQuotaError } from "./types";

const BASE_DELAY_MS = 200;
const MAX_DELAY_MS = 2000;
const MAX_THROTTLE_RETRIES = 3;

export interface ChatResponse {
	response: Response;
	credentialId: string;
	/** Release the held streaming slot. Must be called when response body consumption ends. */
	release: () => void;
}

export class KiroApiClient {
	constructor(private tokenManager: MultiTokenManager) {}

	/** Send a chat request, return raw Response + credential ID for streaming. */
	async sendChatRequest(request: KiroChatRequest, signal?: AbortSignal): Promise<ChatResponse> {
		const model = request.conversationState.currentMessage.userInputMessage.modelId;
		const maxRetries = this.tokenManager.maxRetries;
		const failedCredentialIds = new Set<string>();
		let lastError: Error | null = null;

		for (let attempt = 0; attempt < maxRetries; attempt++) {
			let ctx: CallContext;
			try {
				ctx = await this.tokenManager.acquireContext(model, failedCredentialIds);
			} catch (err) {
				lastError = err instanceof Error ? err : new Error(String(err));
				break;
			}

			const agentMode =
				request.conversationState.currentMessage.userInputMessage.agentMode ??
				request.conversationState.agentTaskType ??
				"vibe";
			const headers = this.buildHeaders(ctx, attempt, maxRetries, agentMode);
			// Inject profileArn from credentials if not already set
			const body = { ...request };
			if (!body.profileArn && ctx.credentials.profileArn) {
				body.profileArn = ctx.credentials.profileArn;
			}
			const region = effectiveApiRegion(this.tokenManager.currentConfig);
			const url = `https://q.${region}.amazonaws.com`;
			const tier = classifyKiroCredentialTier(ctx.credentials);
			const slotState = { held: false };
			let res: Response;

			try {
				await acquireSlot(ctx.id, tier, ctx.credentials.subscriptionTitle);
				slotState.held = true;
				res = await this.fetchWithThrottleRetry(
					() =>
						pfetch(url, {
							method: "POST",
							headers,
							body: JSON.stringify(body),
							signal,
						}),
					ctx,
					slotState,
				);
			} catch (err) {
				if (slotState.held) releaseSlot(ctx.id);
				lastError = err instanceof Error ? err : new Error(String(err));
				if (attempt + 1 < maxRetries) await this.delay(attempt);
				continue;
			}

			const releaseHeld = () => {
				if (!slotState.held) return;
				releaseSlot(ctx.id);
				slotState.held = false;
			};

			if (res.ok) {
				concurrencySuccess(ctx.id);
				this.tokenManager.reportSuccess(ctx.id);
				return {
					response: res,
					credentialId: ctx.id,
					release: releaseHeld,
				};
			}

			const resBody = await res.text().catch(() => "");

			// Suspicious activity throttle may arrive as 400 or 429. Do not retry with
			// the same credential; cool it down and fail over.
			if (isSuspiciousThrottle(resBody)) {
				releaseHeld();
				failedCredentialIds.add(ctx.id);
				this.tokenManager.reportSuspiciousThrottle(ctx.id);
				lastError = new KiroApiError("SUSPICIOUS_THROTTLE", resBody, res.status);
				continue;
			}

			// 400: bad request, don't retry unless it is quota exhaustion.
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
				if (isContextLengthExceeded(resBody)) {
					throw new KiroApiError("CONTEXT_LENGTH_EXCEEDED", resBody, 400);
				}
				throw new KiroApiError("BAD_REQUEST", resBody, 400);
			}

			// 402 + quota exhausted
			if (res.status === 402 && isMonthlyRequestLimit(resBody)) {
				releaseHeld();
				failedCredentialIds.add(ctx.id);
				const hasAvailable = this.tokenManager.reportQuotaExhausted(ctx.id);
				if (!hasAvailable) throw new KiroQuotaError(resBody);
				lastError = new KiroApiError("QUOTA_EXHAUSTED", resBody, 402);
				continue;
			}

			// 401/403: credential failure, failover to next
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

			// 429 after inner backoff retries are exhausted: give up for this request.
			if (res.status === 429) {
				releaseHeld();
				throw new KiroApiError("THROTTLED", resBody, 429);
			}

			// 408/5xx: transient, retry with backoff
			if (res.status === 408 || res.status >= 500) {
				releaseHeld();
				lastError = new KiroApiError("TRANSIENT", resBody, res.status);
				if (attempt + 1 < maxRetries) await this.delay(attempt);
				continue;
			}

			releaseHeld();
			throw new KiroApiError("API_ERROR", resBody, res.status);
		}

		throw lastError ?? new Error("Max retries exceeded");
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

	private buildHeaders(
		ctx: CallContext,
		attempt: number,
		maxRetries: number,
		agentMode: string,
	): Record<string, string> {
		const config = this.tokenManager.currentConfig;
		const region = effectiveApiRegion(config);
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

	private delay(attempt: number): Promise<void> {
		const exp = Math.min(BASE_DELAY_MS * 2 ** attempt, MAX_DELAY_MS);
		const jitter = Math.random() * (exp / 4);
		return new Promise((r) => setTimeout(r, exp + jitter));
	}
}
