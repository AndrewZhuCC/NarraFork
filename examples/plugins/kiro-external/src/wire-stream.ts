/**
 * Decode Kiro's response body into the parsed events the plugin maps to host events.
 *
 * ## Why this module exists
 *
 * Kiro answers `GenerateAssistantResponse` over **AWS event-stream**: length-prefixed binary
 * frames with CRC32 checksums. `KiroApiClient.sendChatRequest` hands back the raw `fetch`
 * Response, so whatever reads `response.body` must speak that format — which is why the
 * built-in path uses `parseEventStream`.
 *
 * This plugin previously read it with `parseSSEStream`, which scans for `event:` / `data:` text
 * lines. That parser belongs to NUG, a gateway that re-emits Kiro content as text SSE; pointed
 * at binary frames it matches no lines and yields **nothing at all**. A chat turn produced no
 * content, no usage and no error — it just ended empty.
 *
 * ## What is reused rather than rewritten
 *
 * Only the byte-decoding layer differs. Frame → `StreamEvent` is `parseEventStream`, and
 * `StreamEvent` → `ParsedStreamEvent` is `parseKiroStreamEvent` plus
 * `KiroStreamToolCallAccumulator` — the same two functions `parseSSEStream` calls. So the
 * mapping, usage accounting and leaked-XML-tool-call recovery all keep behaving identically;
 * this swaps the transport reader underneath them and nothing else.
 *
 * `parseTextToolCalls` is forced off for the per-event pass because the accumulator does that
 * work at stream level, where a tool call split across text deltas can still be recovered.
 * Leaving it on would parse the same call twice, which is why `parseSSEStream` does the same.
 */

import { parseEventStream } from "@server/lib/kiro/event-stream-parser";
import {
	type KiroStreamParseOptions,
	KiroStreamToolCallAccumulator,
	parseKiroStreamEvent,
} from "@shared/codewhisperer/protocol";
import type { ParsedStreamEvent } from "@shared/codewhisperer/types";

/**
 * Stream parsed events from an AWS event-stream response body.
 *
 * Throws `KiroApiError` when the body carries an `exception` frame — including an account
 * suspension delivered inside an HTTP 200, which is the case the caller's failover depends on.
 * Events produced before that point are yielded first, so the caller can tell whether any
 * content already reached the user and therefore whether a replay would duplicate it.
 */
export async function* parseKiroWireStream(
	body: ReadableStream<Uint8Array>,
	options: KiroStreamParseOptions = {},
): AsyncGenerator<ParsedStreamEvent> {
	const toolCallAccumulator = new KiroStreamToolCallAccumulator(options);
	// `parseEventStream` guards its own reads with `readWithTimeout`, so a stalled stream raises
	// `StreamStaleError` rather than hanging the turn. It takes no timeout argument — the default
	// applies — so `options.staleTimeoutMs` has no effect here and is not forwarded.
	for await (const event of parseEventStream(body)) {
		yield toolCallAccumulator.process(
			parseKiroStreamEvent(event, { ...options, parseTextToolCalls: false }),
		);
	}
	// A tool call that ended exactly at the stream's end is only complete once the accumulator
	// is flushed; without this the final call would be dropped.
	const trailing = toolCallAccumulator.finalize();
	if (trailing) yield trailing;
}
