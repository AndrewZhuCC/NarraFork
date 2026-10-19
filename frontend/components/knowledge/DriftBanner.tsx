import { Alert, Button, Group, Modal, Stack, Text } from "@mantine/core";
import { IconAlertTriangle, IconArrowBackUp, IconGitMerge } from "@tabler/icons-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useRebaseKnowledgeDraft } from "../../hooks/useKnowledge";
import { DiffView } from "../narrator/diff/DiffView";

/**
 * Drift handling for a linked personal version: the "main moved ahead" alert, the
 * "rebase (merge)" action, the confirmed "take main" escape hatch, and the conflict diff when a
 * merge cannot be applied.
 *
 * `onResolved` fires after a successful merge or take-main, i.e. once the server holds new
 * personal-version content — the caller drops its local edits so the refreshed copy shows.
 */
export function DriftBanner({
	draftId,
	entryId,
	drifted,
	versionsBehind,
	onResolved,
}: {
	draftId: string;
	entryId: string;
	drifted: boolean;
	versionsBehind: number;
	onResolved?: () => void;
}) {
	const { t } = useTranslation("knowledge");
	const rebaseDraft = useRebaseKnowledgeDraft();
	const [conflict, setConflict] = useState<{ theirs: string; yours: string } | null>(null);
	// "Take main" discards the author's edits irreversibly → always confirm first.
	const [takeMainOpen, setTakeMainOpen] = useState(false);

	const onRebase = () => {
		setConflict(null);
		rebaseDraft.mutate(
			{ draftId, entryId },
			{
				onSuccess: (res) => {
					if (res.ok) {
						// Merged content is now persisted; drop local edits so the fresh draft shows.
						onResolved?.();
					} else if (res.conflict) {
						setConflict({ theirs: res.conflict.theirs, yours: res.conflict.yours });
					}
				},
			},
		);
	};

	// strategy=theirs replaces the draft with main verbatim — it never conflicts, and it
	// throws away the local edits, which is exactly the point (catching up when main already
	// covers what you were writing).
	const onTakeMain = () => {
		setConflict(null);
		rebaseDraft.mutate(
			{ draftId, entryId, strategy: "theirs" },
			{
				onSuccess: () => {
					onResolved?.();
					setTakeMainOpen(false);
				},
			},
		);
	};

	return (
		<>
			{drifted ? (
				<Alert color="orange" title={t("driftBanner")} icon={<IconGitMerge size={16} />}>
					<Stack gap="xs">
						<Text size="sm">
							{versionsBehind > 0 ? t("driftDesc", { count: versionsBehind }) : t("driftDesc_zero")}
						</Text>
						<Group>
							<Button
								size="xs"
								color="orange"
								leftSection={<IconGitMerge size={14} />}
								loading={rebaseDraft.isPending}
								onClick={onRebase}
							>
								{rebaseDraft.isPending ? t("rebasing") : t("rebaseDraft")}
							</Button>
							{/* Escape hatch when the merge conflicts (or you simply want main): take
							    main verbatim. Confirmed in a modal because local edits are lost. */}
							<Button
								size="xs"
								variant="light"
								color="gray"
								leftSection={<IconArrowBackUp size={14} />}
								disabled={rebaseDraft.isPending}
								onClick={() => setTakeMainOpen(true)}
							>
								{t("driftTakeMain")}
							</Button>
							<Text size="xs" c="dimmed">
								{t("rebaseHint")}
							</Text>
						</Group>
					</Stack>
				</Alert>
			) : null}

			<Modal
				opened={takeMainOpen}
				onClose={() => setTakeMainOpen(false)}
				title={t("driftTakeMainTitle")}
			>
				<Stack gap="md">
					<Alert color="red" icon={<IconAlertTriangle size={16} />} p="xs">
						<Text size="xs">{t("driftTakeMainDesc")}</Text>
					</Alert>
					<Text size="xs" c="dimmed">
						{t("driftTakeMainHint")}
					</Text>
					{rebaseDraft.isError ? (
						<Text size="xs" c="red">
							{(rebaseDraft.error as Error).message}
						</Text>
					) : null}
					<Group justify="flex-end">
						<Button variant="subtle" size="xs" onClick={() => setTakeMainOpen(false)}>
							{t("cancel")}
						</Button>
						<Button size="xs" color="red" loading={rebaseDraft.isPending} onClick={onTakeMain}>
							{t("driftTakeMain")}
						</Button>
					</Group>
				</Stack>
			</Modal>

			{conflict ? (
				<Alert
					color="red"
					title={t("rebaseConflict")}
					withCloseButton
					onClose={() => setConflict(null)}
				>
					<Stack gap="xs">
						<Text size="sm">{t("rebaseConflictDesc")}</Text>
						<Text size="xs" c="dimmed">
							{t("rebaseConflictMain")} → {t("rebaseConflictYours")}
						</Text>
						<DiffView
							oldStr={conflict.theirs}
							newStr={conflict.yours}
							language="markdown"
							maxHeight={280}
							wordWrap
						/>
					</Stack>
				</Alert>
			) : null}
		</>
	);
}
