/**
 * Author-side actions on a publish request: withdraw and re-submit.
 *
 * These close the two open ends of the review state machine:
 *  - An open request (pending / conflict / changes requested) can be retracted explicitly
 *    instead of by editing the draft and relying on auto-invalidation. -> Withdraw.
 *  - A `changes_requested` request is re-submitted as a linked next round. -> Re-submit.
 *
 * Visibility comes from the request's server-computed `viewerCaps` (`canWithdraw` /
 * `canResubmit`: submitter-or-admin plus the status each action accepts), so the buttons
 * appear exactly when the endpoint would accept the call. Rendered on the request page.
 */
import { Alert, Button, Group, Modal, Stack, Text, Textarea } from "@mantine/core";
import { IconAlertTriangle, IconArrowBackUp, IconSend } from "@tabler/icons-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
	useResubmitKnowledgeSubmission,
	useWithdrawKnowledgeSubmission,
} from "../../hooks/useKnowledge";
import type { KnowledgeSubmissionViewerCaps } from "../../lib/api";

export interface SubmissionAuthorActionsProps {
	submissionId: string;
	/** Server-computed: whether withdraw / resubmit would be accepted for this viewer. */
	caps: Pick<KnowledgeSubmissionViewerCaps, "canWithdraw" | "canResubmit">;
	/** Personal entry (draft) id, used only to scope cache invalidation. */
	personalEntryId?: string;
	/** Called with the new round's submission id after a successful re-submit. */
	onResubmitted?: (submissionId: string) => void;
	size?: "compact-xs" | "xs";
}

export function SubmissionAuthorActions({
	submissionId,
	caps,
	personalEntryId,
	onResubmitted,
	size = "compact-xs",
}: SubmissionAuthorActionsProps) {
	const { t } = useTranslation("knowledge");
	const withdraw = useWithdrawKnowledgeSubmission();
	const resubmit = useResubmitKnowledgeSubmission();
	const [withdrawOpen, setWithdrawOpen] = useState(false);
	const [resubmitOpen, setResubmitOpen] = useState(false);
	const [reason, setReason] = useState("");
	const [note, setNote] = useState("");

	const { canWithdraw, canResubmit } = caps;
	if (!canWithdraw && !canResubmit) return null;

	// The new round is built from the personal version's SAVED content (the version's editor
	// guards unsaved edits on its own page, so none can be pending here).
	const doResubmit = () => {
		resubmit.mutate(
			{ id: submissionId, changeNote: note.trim() || undefined, personalEntryId },
			{
				onSuccess: (next) => {
					setResubmitOpen(false);
					setNote("");
					onResubmitted?.(next.id);
				},
			},
		);
	};

	return (
		<>
			<Group gap="xs">
				{canWithdraw ? (
					<Button
						size={size}
						variant="light"
						color="gray"
						leftSection={<IconArrowBackUp size={12} />}
						onClick={() => {
							setReason("");
							setWithdrawOpen(true);
						}}
					>
						{t("subFlowWithdraw")}
					</Button>
				) : null}
				{canResubmit ? (
					<Button
						size={size}
						variant="light"
						leftSection={<IconSend size={12} />}
						onClick={() => {
							setNote("");
							setResubmitOpen(true);
						}}
					>
						{t("subFlowResubmit")}
					</Button>
				) : null}
			</Group>

			<Modal
				opened={withdrawOpen}
				onClose={() => setWithdrawOpen(false)}
				title={t("subFlowWithdrawTitle")}
			>
				<Stack gap="md">
					<Text size="sm">{t("subFlowWithdrawDesc")}</Text>
					<Textarea
						label={t("subFlowWithdrawReason")}
						placeholder={t("subFlowWithdrawReasonPlaceholder")}
						value={reason}
						onChange={(e) => setReason(e.currentTarget.value)}
						autosize
						minRows={2}
						maxRows={5}
					/>
					{withdraw.isError ? (
						<Text size="xs" c="red">
							{(withdraw.error as Error).message}
						</Text>
					) : null}
					<Group justify="flex-end">
						<Button variant="subtle" size="xs" onClick={() => setWithdrawOpen(false)}>
							{t("cancel")}
						</Button>
						<Button
							size="xs"
							color="gray"
							loading={withdraw.isPending}
							onClick={() =>
								withdraw.mutate(
									{ id: submissionId, reason: reason.trim() || undefined, personalEntryId },
									{ onSuccess: () => setWithdrawOpen(false) },
								)
							}
						>
							{t("subFlowWithdraw")}
						</Button>
					</Group>
				</Stack>
			</Modal>

			<Modal
				opened={resubmitOpen}
				onClose={() => setResubmitOpen(false)}
				title={t("subFlowResubmitTitle")}
			>
				<Stack gap="md">
					<Alert color="blue" icon={<IconAlertTriangle size={16} />} p="xs">
						<Text size="xs">{t("subFlowResubmitDesc")}</Text>
					</Alert>
					<Textarea
						label={t("subFlowResubmitNote")}
						placeholder={t("subFlowResubmitNotePlaceholder")}
						value={note}
						onChange={(e) => setNote(e.currentTarget.value)}
						autosize
						minRows={2}
						maxRows={6}
					/>
					{resubmit.isError ? (
						<Text size="xs" c="red">
							{(resubmit.error as Error).message}
						</Text>
					) : null}
					<Group justify="flex-end">
						<Button variant="subtle" size="xs" onClick={() => setResubmitOpen(false)}>
							{t("cancel")}
						</Button>
						<Button size="xs" loading={resubmit.isPending} onClick={doResubmit}>
							{t("subFlowResubmit")}
						</Button>
					</Group>
				</Stack>
			</Modal>
		</>
	);
}
