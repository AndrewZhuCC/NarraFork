/**
 * The entry page's personal-version entry point (§5.2), replacing the old "My draft" tab:
 *  - `viewerCaps.myPersonalEntryId` set → "My personal version", a link to its page;
 *  - otherwise → "Propose a change" (or, for someone who can also edit main directly, the
 *    secondary "Create personal version"), which confirms, creates the copy (idempotent
 *    `createDraft`) and opens `/knowledge/mine/<id>`.
 *
 * `promptCreate` opens the confirmation on arrival — the landing of the legacy `#draft` hash
 * when there is no personal version yet (§4.2). Creating is never done by a redirect.
 */
import { Alert, Button, Group, Modal, Stack, Text } from "@mantine/core";
import { IconGitFork, IconPencil } from "@tabler/icons-react";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCreateKnowledgeDraft } from "../../../hooks/useKnowledge";
import { knowledgePersonalEntryDestination } from "../../../lib/knowledge-review-nav";
import { ButtonLink } from "../router-links";

export function PersonalVersionAction({
	entryId,
	myPersonalEntryId,
	canWriteMain,
	promptCreate,
	onPromptClosed,
}: {
	entryId: string;
	myPersonalEntryId: string | null;
	canWriteMain: boolean;
	promptCreate: boolean;
	onPromptClosed: () => void;
}) {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	const create = useCreateKnowledgeDraft();
	const [open, setOpen] = useState(false);

	// The read page shows ONE primary action (§5.2): "Edit" for direct writers, this one for
	// everyone else — so it is filled only when the caller cannot write main.
	const variant = canWriteMain ? "default" : "filled";

	if (myPersonalEntryId) {
		return (
			<ButtonLink
				{...knowledgePersonalEntryDestination(myPersonalEntryId)}
				size="sm"
				variant={variant}
				color="grape"
				leftSection={<IconPencil size={14} />}
			>
				{t("openMyVersion")}
			</ButtonLink>
		);
	}

	const opened = open || promptCreate;
	const close = () => {
		setOpen(false);
		if (promptCreate) onPromptClosed();
	};
	const label = canWriteMain ? t("createPersonalVersion") : t("proposeChange");

	return (
		<>
			<Button
				size="sm"
				variant={variant}
				color="grape"
				leftSection={<IconGitFork size={14} />}
				onClick={() => setOpen(true)}
			>
				{label}
			</Button>
			<Modal opened={opened} onClose={close} title={t("createPersonalVersionTitle")}>
				<Stack gap="md">
					<Text size="sm">{t("createPersonalVersionDesc")}</Text>
					{create.isError ? (
						<Alert color="red" p="xs">
							<Text size="xs">{(create.error as Error).message}</Text>
						</Alert>
					) : null}
					<Group justify="flex-end">
						<Button variant="subtle" size="xs" onClick={close}>
							{t("cancel")}
						</Button>
						<Button
							size="xs"
							loading={create.isPending}
							onClick={() =>
								create.mutate(
									{ entryId },
									{
										onSuccess: (draft) => {
											setOpen(false);
											navigate(knowledgePersonalEntryDestination(draft.id));
										},
									},
								)
							}
						>
							{t("createPersonalVersionConfirm")}
						</Button>
					</Group>
				</Stack>
			</Modal>
		</>
	);
}
