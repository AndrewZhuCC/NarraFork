/**
 * Delete (archive) confirmation for a personal version (§5.5). Spells out which open publish
 * requests the delete withdraws — the server closes every pending / conflict / changes-requested
 * request of the entry in the same transaction, and the detail payload lists exactly those.
 */
import { Alert, Button, Group, List, Modal, Stack, Text } from "@mantine/core";
import { IconAlertTriangle } from "@tabler/icons-react";
import { useTranslation } from "react-i18next";
import type { KnowledgePersonalEntryDetail } from "../../../lib/api";
import { formatLocaleDateTime } from "../../../lib/intl-format";
import { AnchorLink } from "../router-links";
import { SubmissionStatusBadge } from "../SubmissionStatusBadge";

export function PersonalEntryDeleteModal({
	entry,
	name,
	opened,
	onClose,
	onConfirm,
	loading,
	error,
}: {
	entry: KnowledgePersonalEntryDetail;
	name: string;
	opened: boolean;
	onClose: () => void;
	onConfirm: () => void;
	loading: boolean;
	error: string | null;
}) {
	const { t } = useTranslation("knowledge");
	const open = entry.openSubmissions;
	return (
		<Modal opened={opened} onClose={onClose} title={t("deleteConfirmTitle")}>
			<Stack>
				<Text size="sm">{t("deletePersonalEntryConfirm", { name })}</Text>
				{open.length > 0 ? (
					<Alert color="orange" icon={<IconAlertTriangle size={16} />}>
						<Stack gap={4}>
							<Text size="sm">{t("deletePersonalEntryWithdraws", { count: open.length })}</Text>
							<List size="sm">
								{open.map((s) => (
									<List.Item key={s.id}>
										<Group gap={6} wrap="nowrap">
											<AnchorLink
												to="/knowledge/requests/$submissionId"
												params={{ submissionId: s.id }}
												size="sm"
											>
												{t("subFlowRound", { round: s.round })}
											</AnchorLink>
											<SubmissionStatusBadge status={s.status} />
											<Text size="xs" c="dimmed">
												{formatLocaleDateTime(s.createdAt)}
											</Text>
										</Group>
									</List.Item>
								))}
							</List>
						</Stack>
					</Alert>
				) : null}
				{error ? (
					<Text size="xs" c="red">
						{error}
					</Text>
				) : null}
				<Group justify="flex-end">
					<Button variant="subtle" onClick={onClose}>
						{t("cancel")}
					</Button>
					<Button color="red" loading={loading} onClick={onConfirm}>
						{t("delete")}
					</Button>
				</Group>
			</Stack>
		</Modal>
	);
}
