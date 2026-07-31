/**
 * Re-export shim for CodeWhisperer model-ID resolution.
 *
 * The implementation moved to `@shared/codewhisperer/resolve-model` so the
 * CodeWhisperer protocol layer — reused by bundled plugin code — has no
 * `server/` imports. Host importers keep using this path unchanged.
 */
export { resolveModel } from "@shared/codewhisperer/resolve-model";
