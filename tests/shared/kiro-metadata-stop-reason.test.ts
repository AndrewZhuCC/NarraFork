/**
 * Kiro's `metadataEvent` carries the turn's real stop reason, and nothing read it.
 *
 * ## Evidence this is a live gap, not a hypothetical
 *
 * A real chat against `q.us-east-1.amazonaws.com` (api_key credential, model `glm-5`) returned
 * this frame sequence:
 *
 *   initial-response        {conversationId:""}
 *   assistantResponseEvent  {content:"", modelId:"glm-5"}
 *   assistantResponseEvent  {content:"PROBE_OK", modelId:"glm-5"}
 *   metadataEvent           {stopReason:"END_TURN"}
 *   contextUsageEvent       {contextUsagePercentage:1.851}
 *   meteringEvent           {unit:"credit", usage:0.0437}
 *
 * `metadataEvent` appeared in every successful turn, and `grep` across the whole repo found no
 * handler for it — so upstream told us how the turn ended and every consumer discarded it.
 *
 * ## Why that matters beyond tidiness
 *
 * `ParsedStreamEvent.stopReason` already exists and other providers populate it (Anthropic maps
 * `max_tokens`). The host acts on it: `remote-provider-adapter.ts` turns `max_output_tokens` into
 * an `invalidState`, which is how a truncated answer gets reported as truncated. With the field
 * never set for Kiro, a reply cut off at the output limit is indistinguishable from one that
 * finished normally — the user sees a silently truncated answer and no explanation.
 *
 * This is a shared-layer gap, so it affects the built-in Kiro provider as well as the plugin.
 */

import { describe, expect, test } from "bun:test";
import { parseKiroStreamEvent } from "@shared/codewhisperer/protocol";
import type { StreamEvent } from "@shared/codewhisperer/protocol-types";

function parse(eventType: string, data: Record<string, unknown>) {
	return parseKiroStreamEvent({ eventType, data } as StreamEvent, {});
}

describe("Kiro metadataEvent → stopReason", () => {
	test("maps END_TURN to the contract's end_turn", () => {
		// The exact payload observed in a live turn.
		expect(parse("metadataEvent", { stopReason: "END_TURN" }).stopReason).toBe("end_turn");
	});

	test("maps an output-limit stop to max_output_tokens", () => {
		// The one that changes user-visible behaviour: the host converts this into an
		// `invalidState`, so a truncated answer is reported instead of passing as complete.
		expect(parse("metadataEvent", { stopReason: "MAX_TOKENS" }).stopReason).toBe(
			"max_output_tokens",
		);
	});

	test("maps a tool-use stop to tool_use", () => {
		expect(parse("metadataEvent", { stopReason: "TOOL_USE" }).stopReason).toBe("tool_use");
	});

	test("maps a content filter stop to content_filter", () => {
		expect(parse("metadataEvent", { stopReason: "CONTENT_FILTERED" }).stopReason).toBe(
			"content_filter",
		);
	});

	test("accepts the lowercase spelling", () => {
		// Upstream spelling is not guaranteed stable across models, and a case mismatch would
		// silently fall back to "unknown" — which reads as a normal end.
		expect(parse("metadataEvent", { stopReason: "max_tokens" }).stopReason).toBe(
			"max_output_tokens",
		);
	});

	test("leaves an unrecognised stop reason unset rather than guessing", () => {
		// Reporting a wrong reason is worse than reporting none: the host branches on it. An
		// absent value keeps the turn's own status authoritative.
		expect(parse("metadataEvent", { stopReason: "SOMETHING_NEW" }).stopReason).toBeUndefined();
	});

	test("ignores a metadataEvent with no stop reason", () => {
		expect(parse("metadataEvent", {}).stopReason).toBeUndefined();
	});

	test("does not emit text, usage or tool calls", () => {
		// It is metadata only; treating any of it as content would inject stray output into the
		// transcript.
		const result = parse("metadataEvent", { stopReason: "END_TURN" });
		expect(result.text).toBeUndefined();
		expect(result.toolUses).toBeUndefined();
		expect(result.metering).toBeUndefined();
		expect(result.invalidState).toBeUndefined();
	});

	test("the initial-response frame contributes nothing", () => {
		// It leads every real turn and carries only an empty conversationId. It must not be
		// mistaken for content, or every turn would start with a stray empty text delta.
		const result = parse("initial-response", { conversationId: "" });
		expect(result.text).toBeUndefined();
		expect(result.stopReason).toBeUndefined();
	});

	test("an empty assistantResponseEvent yields no text delta", () => {
		// The live capture began with `{content:"", modelId:"glm-5"}`. An empty string must not
		// become a `text.delta`, which would be a visible no-op event on every turn.
		expect(parse("assistantResponseEvent", { content: "", modelId: "glm-5" }).text).toBeFalsy();
	});
});
