/**
 * Publish area of a personal-version page (§5.5): change note + "Submit for review".
 *
 * Whether submitting is possible, and why not, is the server's `viewerCaps.submitBlockers` —
 * the same check `submitForReview` runs — listed in full rather than guessed here. After a
 * submit the new request is linked, and a drift warning (stale base) is shown on the spot so
 * the author can still rebase before a reviewer hits the conflict.
 */
import { Alert, Button, Group, List, Paper, Stack, Text, TextInput } from "@mantine/core";
import { IconAlertTriangle, IconCircleCheck, IconSend } from "@tabler/icons-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { usePublishPersonalEntry } from "../../../hooks/useKnowledge";
import type { KnowledgePersonalEntryDetail } from "../../../lib/api";
import { AnchorLink } from "../router-links";
import { submitBlockerKey } from "./personal-entry-view";

export function PersonalPublishPanel({
	entry,
	beforeSubmit,
	pendingLocalChanges,
}: {
	entry: KnowledgePersonalEntryDetail;
	/** Persist unsaved body edits first, so the request carries the latest content. */
	beforeSubmit: () => Promise<void>;
	/** Unsaved metadata the blockers do not reflect yet. */
	pendingLocalChanges: boolean;
}) {
	const { t } = useTranslation("knowledge");
	const publish = usePublishPersonalEntry();
	const [changeNote, setChangeNote] = useState("");
	const [submitted, setSubmitted] = useState<{ id: string; behind: number | null } | null>(null);
	const caps = entry.viewerCaps;
	const linked = !!entry.entryId;

	const onSubmit = async () => {
		setSubmitted(null);
		await beforeSubmit();
		publish.mutate(
			{ id: entry.id, changeNote: changeNote.trim() || undefined },
			{
				onSuccess: (res) => {
					setChangeNote("");
					setSubmitted({ id: res.id, behind: res.driftWarning?.versionsBehind ?? null });
				},
			},
		);
	};

	return (
		<Paper withBorder p="md" radius="md">
			<Stack gap="sm">
				<div>
					<Text size="sm" fw={600}>
						{t("publishPersonalEntry")}
					</Text>
					<Text size="xs" c="dimmed">
						{linked ? t("publishPersonalEntryDescLinked") : t("publishPersonalEntryDesc")}
					</Text>
				</div>

				{submitted ? (
					<Alert
						color={submitted.behind !== null ? "yellow" : "green"}
						icon={
							submitted.behind !== null ? (
								<IconAlertTriangle size={16} />
							) : (
								<IconCircleCheck size={16} />
							)
						}
						title={submitted.behind !== null ? t("submitDriftTitle") : t("submitted")}
						withCloseButton
						onClose={() => setSubmitted(null)}
					>
						<Stack gap={4}>
							{submitted.behind !== null ? (
								<Text size="sm">{t("submitDriftDesc", { count: submitted.behind })}</Text>
							) : null}
							<AnchorLink
								to="/knowledge/requests/$submissionId"
								params={{ submissionId: submitted.id }}
								size="sm"
							>
								{t("submittedOpenRequest")}
							</AnchorLink>
						</Stack>
					</Alert>
				) : null}

				<TextInput
					label={t("changeNote")}
					placeholder={t("changeNotePlaceholder")}
					value={changeNote}
					onChange={(e) => setChangeNote(e.currentTarget.value)}
					disabled={!caps.canEdit}
				/>
				<Group justify="space-between" align="flex-start" wrap="nowrap">
					{caps.submitBlockers.length > 0 ? (
						<List size="xs" c="orange" data-testid="submit-blockers">
							{caps.submitBlockers.map((b) => (
								<List.Item key={b}>{t(submitBlockerKey(b))}</List.Item>
							))}
						</List>
					) : (
						<Text size="xs" c={pendingLocalChanges ? "orange" : "dimmed"}>
							{pendingLocalChanges ? t("publishSaveMetaFirst") : t("publishReady")}
						</Text>
					)}
					<Button
						size="xs"
						leftSection={<IconSend size={14} />}
						loading={publish.isPending}
						disabled={!caps.canSubmit || pendingLocalChanges}
						onClick={onSubmit}
					>
						{t("submitForReview")}
					</Button>
				</Group>
				{publish.isError ? (
					<Text size="xs" c="red">
						{(publish.error as Error).message}
					</Text>
				) : null}
			</Stack>
		</Paper>
	);
}
