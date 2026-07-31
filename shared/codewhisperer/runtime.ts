/**
 * Host services the CodeWhisperer protocol layer needs but must not import.
 *
 * The protocol layer is shared by the NarraFork core (for the NUG Kiro channel)
 * and by bundled plugin code that runs in a separate process. Plugins may not
 * import host internals, so the few genuinely host-owned capabilities are passed
 * in instead of imported. Everything here is optional: omitting a dependency
 * degrades to a safe no-op or a built-in default, which keeps existing host call
 * sites working unchanged.
 *
 * Deliberately NOT in this contract:
 * - Model-ID parsing — pure text, lives in `@shared/model-id`.
 * - Stream read timeouts, side-car projection, error diagnostics and reasoning
 *   source comparison — pure functions, moved into `shared/`.
 *
 * See `shared/codewhisperer/shared-core.guard.test.ts` for the bundle-purity
 * boundary this contract exists to preserve.
 */

/** Structured warning sink. Mirrors the subset of the host logger used here. */
export interface ProtocolLogger {
	warn(message: string, meta?: Record<string, unknown>): void;
}

/**
 * Resolves an uploaded image to base64 bytes.
 *
 * Image storage is host-owned (upload directory layout, narrator scoping), so
 * history building takes a resolver rather than reaching into the filesystem.
 * Returning `null` means "image unavailable" and the block is skipped.
 */
export type ImageResolver = (input: {
	narratorId: string;
	imageId: string;
}) => Promise<{ base64: string; detectedMediaType?: string } | null>;

/** Optional host services for request normalization. */
export interface NormalizeRuntime {
	logger?: ProtocolLogger;
	/**
	 * Localized prompt injected when a turn would otherwise reach the upstream
	 * with no user content. Defaults to the English wording used by the host.
	 */
	continuationPrompt?: string;
}

/** Optional host services for history building. */
export interface HistoryRuntime {
	logger?: ProtocolLogger;
	resolveImage?: ImageResolver;
}

/**
 * Fallback continuation prompt.
 *
 * Kept byte-identical to the host's `tool.resumeAfterTransientError` English
 * message so behaviour is unchanged when no prompt is injected.
 */
export const DEFAULT_CONTINUATION_PROMPT =
	"Your previous response was interrupted by a temporary connection issue. Please continue from where you left off.";

/** A logger that drops everything, used when the caller supplies none. */
export const silentProtocolLogger: ProtocolLogger = {
	warn: () => undefined,
};
