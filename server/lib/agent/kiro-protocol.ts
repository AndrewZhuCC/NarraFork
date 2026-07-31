/**
 * Re-export shim for the CodeWhisperer / Kiro chat protocol.
 *
 * The implementation moved to `@shared/codewhisperer/protocol` so that a single
 * copy serves both the NUG `kiro` channel in this process and bundled plugin
 * code running in its own process. Host importers keep using this path.
 *
 * Host callers should pass the bindings from `./codewhisperer-host-runtime` into
 * `normalizeKiroChatRequest` and `buildKiroHistory`; without them the protocol
 * layer falls back to a silent logger, the English continuation prompt and no
 * image attachments.
 */
export {
	buildKiroHistory,
	buildKiroReasoningContent,
	buildKiroRequest,
	extractImageFileName,
	KiroHallucinationMarkerFilter,
	type KiroReasoningBlockInput,
	type KiroRedactedThinkingInput,
	type KiroStreamParseOptions,
	KiroStreamToolCallAccumulator,
	KiroTextToolCallStreamParser,
	normalizeKiroChatRequest,
	parseKiroStreamEvent,
	parseKiroTextToolCalls,
	parseSSEStream,
	shouldFilterHallucinatedMarkers,
} from "@shared/codewhisperer/protocol";
