/**
 * Read-page header (§5.2): title, effective level / status / tags, owner, last update and
 * version; one primary action from `viewerCaps` (see {@link entryReadActions}); secondary entry
 * points: History, Settings (when any settings capability), copy link.
 */
import { ActionIcon, Badge, Group, Stack, Text, Title, Tooltip } from "@mantine/core";
import { IconCheck, IconHistory, IconLink, IconPencil, IconSettings } from "@tabler/icons-react";
import { useRouter } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { KnowledgeCollectionListItem, KnowledgeEntryDetail } from "../../../lib/api";
import { copyTextToClipboard } from "../../../lib/clipboard";
import { formatLocaleDateTime } from "../../../lib/intl-format";
import { PersonalVersionAction } from "../personal/PersonalVersionAction";
import { ActionIconLink, ButtonLink } from "../router-links";
import { entryReadActions } from "./entry-actions";

export function EntryReadHeader({
	entry,
	collection,
	promptCreate,
	onPromptClosed,
}: {
	entry: KnowledgeEntryDetail;
	collection: KnowledgeCollectionListItem | undefined;
	promptCreate: boolean;
	onPromptClosed: () => void;
}) {
	const { t } = useTranslation("knowledge");
	const router = useRouter();
	const [copied, setCopied] = useState(false);
	useEffect(() => {
		if (!copied) return;
		const timer = window.setTimeout(() => setCopied(false), 1500);
		return () => window.clearTimeout(timer);
	}, [copied]);
	const caps = entry.viewerCaps;
	const actions = entryReadActions(caps);
	// The level the read gate actually ranks: the entry's own, else the collection default.
	const level = entry.classificationLevel ?? collection?.defaultLevel ?? null;
	const inherited = !entry.classificationLevel && !!level;

	const copyLink = () => {
		const { href } = router.buildLocation({
			to: "/knowledge/$entryId",
			params: { entryId: entry.id },
		});
		copyTextToClipboard(new URL(href, window.location.href).toString()).then(
			() => setCopied(true),
			() => setCopied(false),
		);
	};

	return (
		<Group justify="space-between" align="flex-start" wrap="wrap" gap="md">
			<Stack gap={6} style={{ minWidth: 0, flex: 1 }}>
				<Title order={2} style={{ wordBreak: "break-word" }}>
					{entry.title}
				</Title>
				<Group gap={6} wrap="wrap">
					{level ? (
						<Tooltip label={t("levelInherited")} disabled={!inherited}>
							<Badge size="sm" color="grape" variant={inherited ? "outline" : "light"}>
								{level}
							</Badge>
						</Tooltip>
					) : null}
					{entry.status === "archived" ? (
						<Badge size="sm" color="gray" variant="light">
							{t("statusArchived")}
						</Badge>
					) : null}
					{(entry.tagsJson ?? []).map((tag) => (
						<Badge key={tag} size="sm" variant="light">
							{tag}
						</Badge>
					))}
				</Group>
				<Text size="xs" c="dimmed">
					{[
						t("entryOwnerLine", { name: entry.ownerUsername ?? t("entryNoOwner") }),
						t("entryUpdatedLine", { time: formatLocaleDateTime(entry.updatedAt) }),
						entry.currentVersion ? `v${entry.currentVersion}` : null,
					]
						.filter(Boolean)
						.join(" · ")}
				</Text>
			</Stack>

			<Group gap="xs" wrap="wrap">
				{actions.primary === "edit" ? (
					<ButtonLink
						to="/knowledge/$entryId/edit"
						params={{ entryId: entry.id }}
						size="sm"
						leftSection={<IconPencil size={14} />}
					>
						{t("edit")}
					</ButtonLink>
				) : null}
				<PersonalVersionAction
					entryId={entry.id}
					myPersonalEntryId={caps.myPersonalEntryId}
					canWriteMain={caps.canWriteMain}
					promptCreate={promptCreate}
					onPromptClosed={onPromptClosed}
				/>
				<Tooltip label={t("entryHistory")}>
					<ActionIconLink
						to="/knowledge/$entryId/history"
						params={{ entryId: entry.id }}
						variant="default"
						size="lg"
						aria-label={t("entryHistory")}
					>
						<IconHistory size={16} />
					</ActionIconLink>
				</Tooltip>
				{actions.settings ? (
					<Tooltip label={t("tabSettings")}>
						<ActionIconLink
							to="/knowledge/$entryId/settings"
							params={{ entryId: entry.id }}
							variant="default"
							size="lg"
							aria-label={t("tabSettings")}
						>
							<IconSettings size={16} />
						</ActionIconLink>
					</Tooltip>
				) : null}
				<Tooltip label={copied ? t("linkCopied") : t("copyLink")}>
					<ActionIcon variant="default" size="lg" onClick={copyLink} aria-label={t("copyLink")}>
						{copied ? <IconCheck size={16} /> : <IconLink size={16} />}
					</ActionIcon>
				</Tooltip>
			</Group>
		</Group>
	);
}
