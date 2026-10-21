/**
 * vlist-subagent-recovery-dismiss.ts — live close handler for the post-error
 * subagent recovery card.
 *
 * The card offers exactly two mutations ("resume and notify" / "resume and wait"),
 * both of which restart work. Closing it is the third answer — "leave these
 * subagents stopped" — and it must be a server call, not a local hide: the card is
 * derived from a persisted `disp` message, so hiding it in React state alone would
 * bring it back on the next reload while the user believes it is gone.
 *
 * The DELETE removes only that display row (never the subagents, never the
 * transcript). Mirrors vlist-injection-guard-actions: the render layer declares the
 * callback, this module owns the call + cache prune, and the shell injects it.
 */

import { api } from "@frontend/lib/api";
import { notifications } from "@mantine/notifications";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useRef } from "react";
import { useTranslation } from "react-i18next";
import { removeMessagesFromCache } from "../message/messages-query-cache";

/** Dismiss one recovery card, identified by the display message that carries it. */
export type SubagentRecoveryDismiss = (messageId: string) => void;

/**
 * Build the dismiss callback for one narrator.
 *
 * Identity is stable for as long as the narrator, query client and translator are,
 * because the shell hands this to EVERY mounted row and `ExactRow`'s memo compares
 * it identity-wise — an unstable callback would repaint the whole window on each
 * scroll shift. The in-flight guard lives in a ref for the same reason: a state
 * flag would rebuild the callback mid-flight.
 */
export function useSubagentRecoveryDismiss(narratorId: string): SubagentRecoveryDismiss {
	const { t } = useTranslation("narrator");
	const qc = useQueryClient();
	const inFlightRef = useRef(false);

	const dismiss = useCallback(
		async (messageId: string) => {
			if (!narratorId || inFlightRef.current) return;
			inFlightRef.current = true;
			try {
				const result = await api.dismissSubagentRecoveryMessage(narratorId, messageId);
				// The server also broadcasts `messages_deleted` (which the shell turns
				// into a structural reload), but pruning the paged messages query keeps
				// the reader's cache consistent in the meantime.
				removeMessagesFromCache(qc, narratorId, result.deletedMessageIds ?? [messageId]);
			} catch {
				notifications.show({
					title: t("deleteMessageFailed"),
					message: t("deleteMessageFailedDesc"),
					color: "red",
					autoClose: 5000,
				});
			} finally {
				inFlightRef.current = false;
			}
		},
		[narratorId, qc, t],
	);

	return useCallback<SubagentRecoveryDismiss>(
		(messageId) => {
			void dismiss(messageId);
		},
		[dismiss],
	);
}
