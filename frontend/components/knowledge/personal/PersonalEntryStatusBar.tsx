/**
 * The status bar of a personal-version page (§5.5): kind, the linked entry (link) or the
 * publish target, drift state, the current open request (link), archived state, last update.
 * Every display name comes from the ACL-checked detail payload — null means "not readable".
 */
import { Badge, Group, Text } from "@mantine/core";
import { IconFileSymlink, IconFolder, IconGitMerge } from "@tabler/icons-react";
import { useTranslation } from "react-i18next";
import type { KnowledgePersonalEntryDetail } from "../../../lib/api";
import { formatLocaleDateTime } from "../../../lib/intl-format";
import { AnchorLink } from "../router-links";
import { SubmissionStatusBadge } from "../SubmissionStatusBadge";

export function PersonalEntryStatusBar({ entry }: { entry: KnowledgePersonalEntryDetail }) {
	const { t } = useTranslation("knowledge");
	const linked = !!entry.entryId;
	const open = entry.openSubmissions[0];

	return (
		<Group gap="xs" wrap="wrap" data-testid="personal-entry-status">
			<Badge size="sm" variant="light" color={linked ? "blue" : "grape"}>
				{linked ? t("personalEntryLinked") : t("personalEntryStandalone")}
			</Badge>
			{entry.status === "archived" ? (
				<Badge size="sm" variant="light" color="gray">
					{t("personalEntryStatus_archived")}
				</Badge>
			) : null}

			{linked && entry.entryId ? (
				<Group gap={4} wrap="nowrap">
					<IconFileSymlink size={14} />
					{entry.entryTitle ? (
						<AnchorLink to="/knowledge/$entryId" params={{ entryId: entry.entryId }} size="sm">
							{t("mineLinkedTo", { title: entry.entryTitle })}
						</AnchorLink>
					) : (
						<Text size="sm" c="dimmed">
							{t("mineLinkedUnreadable")}
						</Text>
					)}
				</Group>
			) : (
				<Group gap={4} wrap="nowrap">
					<IconFolder size={14} />
					<Text size="sm" c="dimmed">
						{!entry.targetCollectionId
							? t("noTargetCollection")
							: `${t("publishTarget")}: ${entry.targetCollectionName ?? t("mineTargetUnreadable")}`}
					</Text>
				</Group>
			)}

			{entry.drift ? (
				entry.drift.drifted ? (
					<Badge size="sm" variant="light" color="orange" leftSection={<IconGitMerge size={12} />}>
						{entry.drift.versionsBehind > 0
							? t("personalBehind", { count: entry.drift.versionsBehind })
							: t("driftBadge")}
					</Badge>
				) : (
					<Badge size="sm" variant="light" color="green">
						{t("personalUpToDate")}
					</Badge>
				)
			) : null}

			{open ? (
				<AnchorLink
					to="/knowledge/requests/$submissionId"
					params={{ submissionId: open.id }}
					underline="never"
					aria-label={t("personalOpenRequest")}
				>
					<SubmissionStatusBadge status={open.status} size="sm" style={{ cursor: "pointer" }} />
				</AnchorLink>
			) : null}

			<Text size="xs" c="dimmed">
				{t("personalEntryUpdatedAt", { time: formatLocaleDateTime(entry.updatedAt) })}
			</Text>
		</Group>
	);
}
