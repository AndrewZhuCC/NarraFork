/**
 * Re-export shim for side-car projection helpers.
 *
 * The implementation moved to `@shared/codewhisperer/sidecar` so the
 * CodeWhisperer protocol layer — reused by bundled plugin code — has no
 * `server/` imports. Host importers keep using this path unchanged.
 */
export {
	appendSideCarsForApi,
	formatSideCarForApi,
	formatSideCarsForApi,
	outputToText,
	type StoredSideCar,
	sideCarsForToolResult,
	sideCarsForUserMessage,
	sortSideCars,
} from "@shared/codewhisperer/sidecar";
