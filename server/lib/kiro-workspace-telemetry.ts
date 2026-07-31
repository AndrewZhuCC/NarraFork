/**
 * Re-export shim for Kiro workspace telemetry injection.
 *
 * The implementation moved to `@shared/codewhisperer/workspace-telemetry` so the
 * NUG Kiro channel and bundled plugin code share one copy of the signing logic.
 * The HMAC key bytes are part of the on-the-wire contract with upstream
 * Kiro-protocol gateways and are unchanged by the move.
 */
export { prepareKiroWorkspaceState } from "@shared/codewhisperer/workspace-telemetry";
