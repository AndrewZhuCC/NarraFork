/**
 * Re-export shim for bounded API error diagnostics.
 *
 * The implementation moved to `@shared/codewhisperer/error-diagnostics` so the
 * CodeWhisperer protocol layer — reused by bundled plugin code — has no
 * `server/` imports. Host importers keep using this path unchanged.
 */
export {
	diagnosticsFromError,
	ERROR_DIAGNOSTICS_SCHEMA,
	MAX_DIAGNOSTIC_HEADERS,
	MAX_DIAGNOSTIC_JSON_CHARS,
	MAX_DIAGNOSTIC_TEXT_CHARS,
	normalizeApiRequestDiagnostics,
	normalizeDiagnosticHeaders,
	parseErrorDiagnostics,
} from "@shared/codewhisperer/error-diagnostics";
