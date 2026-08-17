// Kiro API client — reverse-engineered from kiro CLI chat
// Multi-credential failover mode using MultiTokenManager

import { randomUUID } from "node:crypto";
import { applyKiroTokenTypeHeader } from "./auth-method";
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
import {
	type CallContext,
	type DisabledReason,
	KiroApiError,
	type KiroChatRequest,
	KiroQuotaError,
} from "./types";

const BASE_DELAY_MS = 200;
const MAX_DELAY_MS = 2000;
const MAX_THROTTLE_RETRIES = 3;

export interface ChatResponse {
	response: Response;
	credentialId: string;
	/** Release the held streaming slot. Must be called when response body consumption ends. */
	release: () => void;
	/**
	 * Attribute a failure discovered while CONSUMING the stream to the credential
	 * that served it.
	 *
	 * Kiro speaks AWS event-stream, so an upstream verdict can arrive as an
	 * `exception` frame inside an HTTP 200 body — after `sendChatRequest` already
	 * returned and already called `reportSuccess`. Without this hook the retry loop
	 * below is unreachable from the stream reader, which is exactly how a suspended
	 * credential kept being reselected on every subsequent request.
	 *
	 * Returns whether any other credential remains available.
	 */
	reportStreamFailure: (reason?: DisabledReason) => boolean;
	/**
	 * Re-dispatch the SAME logical request on a different credential.
	 *
	 * Reuses this call's `failedCredentialIds` set, so the credential that just
	 * failed mid-stream (and any earlier casualty) is excluded by
	 * `acquireContext`. Bounded by the same `maxRetries` budget as the initial
	 * dispatch, so a fleet where every credential is suspended terminates instead
	 * of replaying forever.
	 *
	 * Only safe while the failed stream produced no client-visible output — the
	 * caller owns that judgement.
	 */
	retryWithNextCredential: () => Promise<ChatResponse>;
}

export class KiroApiClient {
	constructor(private tokenManager: MultiTokenManager) {}

	/**
	 * Send a chat request, return raw Response + credential ID for streaming.
	 *
	 * `failedCredentialIds` is threaded through recursive stream-failure replays
	 * (see {@link ChatResponse.retryWithNextCredential}) so one logical call never
	 * reuses a credential that already failed for it, no matter which layer
	 * discovered the failure. `replaysLeft` bounds that recursion.
	 */
	async sendChatRequest(
		request: KiroChatRequest,
		signal?: AbortSignal,
		carriedFailedCredentialIds?: Set<string>,
		replaysLeft?: number,
	): Promise<ChatResponse> {
		const model = request.conversationState.currentMessage.userInputMessage.modelId;
		const maxRetries = this.tokenManager.maxRetries;
		const failedCredentialIds = carriedFailedCredentialIds ?? new Set<string>();
		const remainingReplays = replaysLeft ?? maxRetries;
		let lastError: Error | null = null;
		// One id for this whole logical call, minted before the retry loop and reused by
		// every attempt — that correlation is the header's entire purpose, and it is what
		// lets the service deduplicate attempts and size its adaptive throttling. The AWS
		// SDK does exactly this: `@smithy/core` sets amz-sdk-invocation-id once outside the
		// loop and only rewrites amz-sdk-request per attempt. Regenerating it per attempt
		// would emit `attempt=2` under an id the server has never seen, which no AWS client
		// produces and which makes N retries look like N independent first calls.
		const invocationId = randomUUID();
		// Attempt numbering spans both retry layers (credential failover here, 429
		// throttle inside fetchWithThrottleRetry) because every dispatch below is a real
		// attempt against the service. Counting only the outer loop would report the same
		// `attempt=` for several distinct requests.
		const maxAttempts = maxRetries * (MAX_THROTTLE_RETRIES + 1);
		let attemptNumber = 0;

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
			const headers = this.buildHeaders(ctx, agentMode, invocationId);
			// Inject profileArn from credentials if not already set
			const body = { ...request };
			if (!body.profileArn && ctx.credentials.profileArn) {
				body.profileArn = ctx.credentials.profileArn;
			}
			const region = effectiveApiRegion(this.tokenManager.currentConfig, ctx.credentials);
			const url = `https://q.${region}.amazonaws.com`;
			const tier = classifyKiroCredentialTier(ctx.credentials);
			const slotState = { held: false };
			let res: Response;

			try {
				await acquireSlot(ctx.id, tier, ctx.credentials.subscriptionTitle);
				slotState.held = true;
				res = await this.fetchWithThrottleRetry(
					() => {
						// Stamped immediately before each dispatch, mirroring the AWS SDK, which
						// rewrites this header inside the retry loop while the invocation id above
						// stays fixed. fetchWithThrottleRetry can call this several times.
						attemptNumber++;
						headers["amz-sdk-request"] = `attempt=${attemptNumber}; max=${maxAttempts}`;
						return pfetch(url, {
							method: "POST",
							headers,
							body: JSON.stringify(body),
							signal,
						});
					},
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
				const failedId = ctx.id;
				return {
					response: res,
					credentialId: failedId,
					release: releaseHeld,
					reportStreamFailure: (reason) => {
						// The stream reader found the real verdict after the HTTP layer had
						// already accepted this credential. Exclude it from this call and
						// mark it so later calls skip it too.
						failedCredentialIds.add(failedId);
						return this.tokenManager.reportFailure(failedId, reason);
					},
					retryWithNextCredential: () => {
						if (remainingReplays <= 0) {
							throw new KiroApiError(
								"STREAM_REPLAY_EXHAUSTED",
								"Exhausted credential replays after mid-stream failures",
							);
						}
						failedCredentialIds.add(failedId);
						return this.sendChatRequest(request, signal, failedCredentialIds, remainingReplays - 1);
					},
				};
			}

			const resBody = await res.text().catch(() => "");

			// Suspicious activity throttle may arrive as 400 or 429. Prefer failover,
			// but keep retrying the same credential when no usable alternative remains.
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
				// A suspension can arrive as 400 rather than 401/403. Without this it fell
				// into the generic BAD_REQUEST throw below, which neither marks the
				// credential nor fails over, so every later request picked the same
				// suspended account again.
				if (isAccountSuspended(resBody)) {
					releaseHeld();
					failedCredentialIds.add(ctx.id);
					const hasAvailable = this.tokenManager.reportFailure(ctx.id, "account_suspended");
					if (!hasAvailable) throw new KiroApiError("ACCOUNT_SUSPENDED", resBody, 400);
					lastError = new KiroApiError("ACCOUNT_SUSPENDED", resBody, 400);
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
				const suspended = isAccountSuspended(resBody);
				const hasAvailable = this.tokenManager.reportFailure(
					ctx.id,
					suspended ? "account_suspended" : undefined,
				);
				const code = suspended ? "ACCOUNT_SUSPENDED" : "AUTH_FAILED";
				if (!hasAvailable) {
					throw new KiroApiError(code, resBody, res.status);
				}
				lastError = new KiroApiError(code, resBody, res.status);
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

	/**
	 * Build the request headers for one credential.
	 *
	 * `amz-sdk-request` is deliberately NOT set here: it carries the per-attempt
	 * counter and is stamped by the caller immediately before each dispatch, so a
	 * value baked in at header-build time cannot go stale across retries.
	 * `invocationId` is supplied by the caller because it must stay constant for
	 * every attempt of one logical call.
	 */
	private buildHeaders(
		ctx: CallContext,
		agentMode: string,
		invocationId: string,
	): Record<string, string> {
		const config = this.tokenManager.currentConfig;
		const region = effectiveApiRegion(config, ctx.credentials);
		const { kiroVersion } = config;
		const [os, osVersion] = config.systemVersion.split("#");

		const headers: Record<string, string> = {
			"Content-Type": "application/x-amz-json-1.0",
			"x-amz-target": "AmazonCodeWhispererStreamingService.GenerateAssistantResponse",
			"x-amzn-codewhisperer-optout": "true",
			"x-amzn-kiro-agent-mode": agentMode,
			"User-Agent": `AmazonQ-For-CLI/Version/${kiroVersion}`,
			Host: `q.${region}.amazonaws.com`,
			"amz-sdk-invocation-id": invocationId,
			Authorization: `Bearer ${ctx.token}`,
			"x-amzn-codewhisperer-os": os ?? "linux",
			"x-amzn-codewhisperer-os-version": osVersion ?? "unknown",
			"x-amzn-codewhisperer-ide": "kiro",
			"x-amzn-codewhisperer-ide-version": kiroVersion,
		};
		applyKiroTokenTypeHeader(headers, ctx.credentials);
		return headers;
	}

	private delay(attempt: number): Promise<void> {
		const exp = Math.min(BASE_DELAY_MS * 2 ** attempt, MAX_DELAY_MS);
		const jitter = Math.random() * (exp / 4);
		return new Promise((r) => setTimeout(r, exp + jitter));
	}
}
