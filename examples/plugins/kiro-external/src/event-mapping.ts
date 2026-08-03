/**
 * Maps Kiro's `ParsedStreamEvent` onto the plugin RPC stream-event vocabulary.
 *
 * This is the load-bearing translation in the plugin. Both sides already exist:
 * `shared/codewhisperer/protocol.ts` parses Kiro's upstream SSE frames, and
 * `providerStreamEventSchema` (`server/lib/plugins/protocol.ts`) defines what a plugin may
 * emit. Everything Kiro produces has a slot, so no custom event type is introduced — and
 * because every event schema is `.strict()`, inventing one would be rejected anyway.
 *
 * ## Kiro reports credits, not tokens
 *
 * Kiro's upstream sends `meteringEvent` (credit consumption) and `contextUsageEvent`
 * (percentage of the context window used) instead of token counts. Both map onto existing
 * `providerUsageSchema` fields: `metering{unit,unitPlural,usage}` and
 * `contextUsagePercentage`.
 *
 * **Token counts are deliberately left absent, never zeroed.** The host treats a `0` as a
 * real measurement: it would report a zero-cost request and compute context headroom from a
 * prompt size it believes is empty. Omitting the fields makes the host fall back to
 * `contextUsagePercentage`, which is the only truthful signal available here. `usageFrom`
 * below therefore returns `undefined` rather than an object full of zeros when Kiro has
 * reported nothing.
 *
 * ## Ordering
 *
 * The host reconstructs interleaved content from `outputIndex`, so text, reasoning and tool
 * calls each carry the provider-native index when Kiro supplies one. Where Kiro gives no
 * index the field is omitted rather than defaulted to 0, which would collapse separate
 * blocks onto each other.
 */

import type { ParsedStreamEvent } from "@shared/codewhisperer/types";

/** Subset of the host contract this module emits. Mirrors `providerStreamEventSchema`. */
export type PluginStreamEvent =
	| { type: "request_started"; credentialId?: string; reasoningSource?: string }
	| { type: "text.delta"; text: string; outputIndex?: number }
	| {
			type: "reasoning.delta";
			blockId: string;
			text: string;
			outputIndex?: number;
			metadata?: { source: string; format: string; data: unknown };
	  }
	| {
			type: "reasoning.metadata";
			blockId: string;
			outputIndex?: number;
			metadata: { source: string; format: string; data: unknown };
	  }
	| { type: "reasoning.redacted"; data: string; outputIndex?: number; source?: string }
	| { type: "tool_call.start"; toolUseId: string; name: string; outputIndex?: number }
	| { type: "tool_call.delta"; toolUseId: string; argumentsDelta: string }
	| { type: "tool_call.end"; toolUseId: string }
	| {
			type: "tool_call.complete";
			toolUseId: string;
			name: string;
			input: Record<string, unknown>;
			outputIndex?: number;
	  }
	| { type: "usage"; usage: PluginUsage }
	| {
			type: "done";
			status: "completed" | "cancelled" | "failed";
			stopReason: string;
			credentialId?: string;
			usage?: PluginUsage;
	  };

export interface PluginUsage {
	promptTokens?: number;
	inputTokens?: number;
	completionTokens?: number;
	cachedInputTokens?: number;
	contextWindow?: number;
	contextUsagePercentage?: number;
	metering?: { unit: string; unitPlural: string; usage: number };
}

/** Accumulates the usage seen across a stream so `done` can carry the final figures. */
export class UsageAccumulator {
	private usage: PluginUsage = {};
	private dirty = false;

	/** Fold one parsed event's usage-bearing fields in. Returns true if anything changed. */
	observe(event: ParsedStreamEvent): boolean {
		let changed = false;
		if (typeof event.contextUsagePercentage === "number") {
			// Clamp: the schema rejects out-of-range values, and a bad upstream number should
			// not fail the whole request.
			const pct = Math.min(100, Math.max(0, event.contextUsagePercentage));
			if (this.usage.contextUsagePercentage !== pct) {
				this.usage.contextUsagePercentage = pct;
				changed = true;
			}
		}
		if (event.metering) {
			this.usage.metering = {
				unit: event.metering.unit,
				unitPlural: event.metering.unitPlural,
				usage: event.metering.usage,
			};
			changed = true;
		}
		// Token counts are only forwarded when Kiro actually reports them, which normally it
		// does not. See the header: absent is truthful, zero is a lie.
		const source = event.usage;
		if (source) {
			for (const key of [
				"promptTokens",
				"inputTokens",
				"completionTokens",
				"cachedInputTokens",
			] as const) {
				const value = source[key];
				if (typeof value === "number" && Number.isFinite(value) && value > 0) {
					this.usage[key] = Math.trunc(value);
					changed = true;
				}
			}
		}
		if (changed) this.dirty = true;
		return changed;
	}

	/** Record the model's context window so the host can show absolute headroom. */
	setContextWindow(tokens: number | undefined): void {
		if (typeof tokens === "number" && tokens > 0) {
			this.usage.contextWindow = Math.trunc(tokens);
			this.dirty = true;
		}
	}

	/** Snapshot, or `undefined` when nothing was ever reported. */
	snapshot(): PluginUsage | undefined {
		if (!this.dirty) return undefined;
		return {
			...this.usage,
			...(this.usage.metering ? { metering: { ...this.usage.metering } } : {}),
		};
	}
}

/** Stable block id for a reasoning stream. Kiro does not supply one. */
function reasoningBlockId(outputIndex: number | undefined): string {
	return `reasoning-${outputIndex ?? 0}`;
}

/**
 * Translate one parsed Kiro event into zero or more plugin events.
 *
 * Returns an array because a single upstream frame can carry text *and* usage, and the host
 * contract keeps those separate. Order within the array is significant: content first, then
 * usage, so a consumer sees the text that the usage describes.
 */
export function mapKiroEvent(
	event: ParsedStreamEvent,
	usage: UsageAccumulator,
): PluginStreamEvent[] {
	const out: PluginStreamEvent[] = [];

	if (typeof event.text === "string" && event.text.length > 0) {
		out.push({
			type: "text.delta",
			text: event.text,
			...(typeof event.textOutputIndex === "number" ? { outputIndex: event.textOutputIndex } : {}),
		});
	}

	if (typeof event.reasoning === "string" && event.reasoning.length > 0) {
		const outputIndex = event.reasoningOutputIndex;
		out.push({
			type: "reasoning.delta",
			blockId: reasoningBlockId(outputIndex),
			text: event.reasoning,
			...(typeof outputIndex === "number" ? { outputIndex } : {}),
			// Carries the Anthropic thinking signature when present, which the host must echo
			// back on the next turn or upstream rejects the history.
			...(event.reasoningMetadata
				? {
						metadata: {
							source: "kiro",
							format: "anthropic",
							data: event.reasoningMetadata as unknown,
						},
					}
				: {}),
		});
	} else if (event.reasoningMetadata) {
		// Signature arriving without text still has to reach the host.
		const outputIndex = event.reasoningOutputIndex;
		out.push({
			type: "reasoning.metadata",
			blockId: reasoningBlockId(outputIndex),
			...(typeof outputIndex === "number" ? { outputIndex } : {}),
			metadata: { source: "kiro", format: "anthropic", data: event.reasoningMetadata as unknown },
		});
	}

	if (event.redactedThinking?.data) {
		out.push({
			type: "reasoning.redacted",
			data: event.redactedThinking.data,
			...(typeof event.redactedThinking.outputIndex === "number"
				? { outputIndex: event.redactedThinking.outputIndex }
				: {}),
			source: "kiro",
		});
	}

	// Streaming tool-call chunks. `stop` closes the block; a chunk may carry a name (start)
	// and/or an argument fragment.
	const chunk = event.toolUseChunk;
	if (chunk) {
		if (chunk.name) {
			out.push({
				type: "tool_call.start",
				toolUseId: chunk.toolUseId,
				name: chunk.name,
				...(typeof chunk.outputIndex === "number" ? { outputIndex: chunk.outputIndex } : {}),
			});
		}
		if (typeof chunk.input === "string" && chunk.input.length > 0) {
			out.push({
				type: "tool_call.delta",
				toolUseId: chunk.toolUseId,
				argumentsDelta: chunk.input,
			});
		}
		if (chunk.stop) out.push({ type: "tool_call.end", toolUseId: chunk.toolUseId });
	}

	// Fully-formed tool uses (non-streaming path, or recovered from text).
	for (const toolUse of event.toolUses ?? []) {
		out.push({
			type: "tool_call.complete",
			toolUseId: toolUse.toolUseId,
			name: toolUse.name,
			input: toolUse.input,
			...(typeof toolUse.outputIndex === "number" ? { outputIndex: toolUse.outputIndex } : {}),
		});
	}

	if (usage.observe(event)) {
		const snapshot = usage.snapshot();
		if (snapshot) out.push({ type: "usage", usage: snapshot });
	}

	return out;
}

/**
 * Classify a Kiro failure for the host's error event.
 *
 * `retryable: false` on quota exhaustion matters: the host would otherwise retry a request
 * that cannot succeed until the quota window rolls over.
 */
export function classifyKiroError(error: unknown): {
	classification: "api" | "transport" | "invalid_state" | "protocol" | "cancelled";
	code: string;
	message: string;
	retryable?: boolean;
	statusCode?: number;
} {
	const name = error instanceof Error ? error.name : "";
	const message = error instanceof Error ? error.message : String(error);

	if (name === "AbortError") {
		return { classification: "cancelled", code: "CANCELLED", message: "Request was cancelled" };
	}
	if (name === "KiroQuotaError") {
		return {
			classification: "api",
			code: "QUOTA_EXHAUSTED",
			message,
			retryable: false,
			statusCode: 429,
		};
	}
	if (name === "KiroAuthError") {
		return {
			classification: "api",
			code: "AUTH_FAILED",
			message,
			retryable: false,
			statusCode: 401,
		};
	}
	if (name === "MissingCredentialError") {
		return { classification: "invalid_state", code: "NOT_CONFIGURED", message, retryable: false };
	}
	if (name === "KiroApiError") {
		// KiroApiError carries a structured `code` and optional `statusCode` from
		// `api-client.ts`. The built-in path (`server/lib/agent/error-handling.ts`)
		// classifies each case independently; we must match to avoid the plugin path
		// retrying errors the built-in path treats as terminal.
		const apiCode =
			error && typeof error === "object" && "code" in error
				? (error as { code: unknown }).code
				: undefined;
		const statusCode =
			error && typeof error === "object" && "statusCode" in error
				? (error as { statusCode: unknown }).statusCode
				: undefined;
		const numericStatus = typeof statusCode === "number" ? statusCode : undefined;

		switch (apiCode) {
			// 400 + context too long: deterministic, will fail again with the same input.
			case "CONTEXT_LENGTH_EXCEEDED":
				return {
					classification: "api",
					code: "CONTEXT_LENGTH_EXCEEDED",
					message,
					retryable: false,
					statusCode: numericStatus,
				};
			// 400 generic: malformed request, retrying the same payload is pointless.
			case "BAD_REQUEST":
				return {
					classification: "api",
					code: "BAD_REQUEST",
					message,
					retryable: false,
					statusCode: numericStatus,
				};
			// 401/403: credential invalid or account suspended. Retrying will not fix
			// a revoked token or a banned account. The built-in path also treats these
			// as non-retryable (they trigger credential failover, not request retry).
			case "AUTH_FAILED":
				return {
					classification: "api",
					code: "AUTH_FAILED",
					message,
					retryable: false,
					statusCode: numericStatus,
				};
			// 429: upstream rate limit. The request itself is fine; retrying after a
			// back-off period is the correct response, same as the built-in path.
			case "THROTTLED":
				return {
					classification: "api",
					code: "THROTTLED",
					message,
					retryable: true,
					statusCode: numericStatus,
				};
			// 400/402 + monthly quota exhausted: retrying burns time until the billing
			// window resets. Handled identically to KiroQuotaError but surfaced here
			// when the multi-credential loop still had alternatives and continued past
			// the first exhausted credential (the lastError it throws carries this code).
			case "QUOTA_EXHAUSTED":
				return {
					classification: "api",
					code: "QUOTA_EXHAUSTED",
					message,
					retryable: false,
					statusCode: numericStatus,
				};
			// 408/5xx: transient upstream failure. The built-in client already retried
			// internally; if it still threw, it means all internal retries were spent.
			// The host-level retry is still worthwhile (different timing, potential
			// recovery of the upstream).
			case "TRANSIENT":
				return {
					classification: "api",
					code: "UPSTREAM_ERROR",
					message,
					retryable: true,
					statusCode: numericStatus,
				};
			// Catch-all for unknown codes: if the status is a client error (4xx) the
			// request is likely malformed and retrying won't help; otherwise assume
			// transient (same heuristic as the built-in `isRetryableError`).
			default:
				return {
					classification: "api",
					code: typeof apiCode === "string" ? apiCode : "UPSTREAM_ERROR",
					message,
					retryable: numericStatus != null ? numericStatus >= 500 || numericStatus === 408 : true,
					statusCode: numericStatus,
				};
		}
	}
	return { classification: "transport", code: "REQUEST_FAILED", message, retryable: true };
}
