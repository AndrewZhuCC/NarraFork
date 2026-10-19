/**
 * One personal version on "My" (§5.4): title, kind (linked to «entry» / standalone · target),
 * behind-main and open-request badges, last update. The whole card opens its one page,
 * `/knowledge/mine/$personalEntryId`, for linked and standalone entries alike.
 */
import { Badge, Card, Group, Text, Tooltip } from "@mantine/core";
import { IconBook2, IconFileSymlink, IconFolder, IconGitMerge } from "@tabler/icons-react";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import type { KnowledgeOpenSubmission, KnowledgePersonalEntrySummary } from "../../../lib/api";
import { formatLocaleDateTime } from "../../../lib/intl-format";
import { knowledgePersonalEntryDestination } from "../../../lib/knowledge-review-nav";
import cardClasses from "../KnowledgeCards.module.css";
import { SubmissionStatusBadge } from "../SubmissionStatusBadge";
import { personalEntryDisplayTitle } from "./personal-entry-view";

/** "Linked to «title»" / "Standalone · target" — shared by the list and the request rows. */
export function PersonalEntryKindLabel({
	entry,
}: {
	entry: Pick<
		KnowledgePersonalEntrySummary,
		"entryId" | "entryTitle" | "targetCollectionId" | "targetCollectionName"
	>;
}) {
	const { t } = useTranslation("knowledge");
	if (entry.entryId) {
		return (
			<Group gap={4} wrap="nowrap" style={{ minWidth: 0 }}>
				<IconFileSymlink size={12} style={{ flexShrink: 0 }} />
				<Text size="xs" c="dimmed" truncate="end">
					{entry.entryTitle
						? t("mineLinkedTo", { title: entry.entryTitle })
						: t("mineLinkedUnreadable")}
				</Text>
			</Group>
		);
	}
	return (
		<Group gap={4} wrap="nowrap" style={{ minWidth: 0 }}>
			<IconFolder size={12} style={{ flexShrink: 0 }} />
			<Text size="xs" c="dimmed" truncate="end">
				{!entry.targetCollectionId
					? t("mineStandaloneNoTarget")
					: entry.targetCollectionName
						? t("mineStandaloneTarget", { name: entry.targetCollectionName })
						: t("mineStandaloneTargetUnreadable")}
			</Text>
		</Group>
	);
}

export function PersonalEntryRow({
	entry,
	openRequest,
}: {
	entry: KnowledgePersonalEntrySummary;
	openRequest: KnowledgeOpenSubmission | undefined;
}) {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	const title = personalEntryDisplayTitle(entry) ?? t("mineUntitled");
	const archived = entry.status === "archived";

	return (
		<Card
			withBorder
			padding="md"
			radius="md"
			className={cardClasses.entryCard}
			data-testid="mine-personal-entry"
			onClick={() => navigate(knowledgePersonalEntryDestination(entry.id))}
		>
			<Group justify="space-between" wrap="nowrap" align="center">
				<div style={{ flex: 1, minWidth: 0 }}>
					<Group gap="xs" align="center" mb={4} wrap="nowrap">
						<IconBook2 size={16} style={{ color: "var(--mantine-color-grape-5)", flexShrink: 0 }} />
						<Text size="sm" fw={600} truncate="end" c={archived ? "dimmed" : undefined}>
							{title}
						</Text>
					</Group>
					<PersonalEntryKindLabel entry={entry} />
				</div>
				<Group gap={6} wrap="nowrap" style={{ flexShrink: 0 }}>
					{entry.drifted ? (
						<Tooltip label={t("driftBadgeTooltip")} withArrow>
							<Badge
								size="xs"
								variant="light"
								color="orange"
								leftSection={<IconGitMerge size={10} />}
							>
								{t("driftBadge")}
							</Badge>
						</Tooltip>
					) : null}
					{openRequest ? <SubmissionStatusBadge status={openRequest.status} /> : null}
					{archived ? (
						<Badge size="xs" variant="light" color="gray">
							{t("personalEntryStatus_archived")}
						</Badge>
					) : null}
					<Text size="xs" c="dimmed" visibleFrom="sm">
						{formatLocaleDateTime(entry.updatedAt)}
					</Text>
				</Group>
			</Group>
		</Card>
	);
}
