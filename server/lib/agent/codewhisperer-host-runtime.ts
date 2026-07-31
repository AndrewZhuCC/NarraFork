/**
 * Host bindings for the shared CodeWhisperer protocol layer.
 *
 * The protocol logic lives in `@shared/codewhisperer/*` so that bundled plugin
 * code can reuse it without importing host internals. The few genuinely
 * host-owned capabilities it needs — structured logging, the localized
 * continuation prompt and upload-backed image resolution — are supplied here and
 * passed in explicitly.
 *
 * Every in-core caller (`KiroProvider`, the NUG Kiro channel) should pass these
 * so behaviour matches what shipped before the protocol layer was extracted;
 * omitting them degrades to a silent logger, the English fallback prompt and no
 * image attachments.
 */

import type {
	HistoryRuntime,
	NormalizeRuntime,
	ProtocolLogger,
} from "@shared/codewhisperer/runtime";
import { getToolMessage } from "../i18n";
import { logger } from "../logger";
import { getImagePath, imageToBase64 } from "../uploads";

const protocolLogger: ProtocolLogger = {
	warn: (message, meta) => logger.warn(message, meta),
};

/**
 * Host services for `normalizeKiroChatRequest`.
 *
 * The continuation prompt is read per call so a locale change takes effect
 * without restarting the process.
 */
export function hostNormalizeRuntime(): NormalizeRuntime {
	return {
		logger: protocolLogger,
		continuationPrompt: getToolMessage("resumeAfterTransientError"),
	};
}

/** Host services for `buildKiroHistory`, including upload-backed images. */
export function hostHistoryRuntime(): HistoryRuntime {
	return {
		logger: protocolLogger,
		resolveImage: async ({ narratorId, imageId }) => {
			const filePath = getImagePath(narratorId, imageId);
			if (!filePath) return null;
			try {
				const result = await imageToBase64(filePath);
				return { base64: result.base64, detectedMediaType: result.detectedMediaType };
			} catch {
				// The image file may have been deleted since the message was stored.
				return null;
			}
		},
	};
}
