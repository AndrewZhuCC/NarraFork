/**
 * Re-export shim for thinking-signature source identity.
 *
 * The implementation moved to `@shared/codewhisperer/reasoning-source` so the
 * CodeWhisperer protocol layer — reused by bundled plugin code — has no
 * `server/` imports. Host importers keep using this path unchanged.
 */
export {
	type ReasoningSourceKey,
	signatureSourcesCompatible,
} from "@shared/codewhisperer/reasoning-source";
