/**
 * "Related entries" below the body of the read page (§5.2): outgoing and incoming links, each
 * grouped by link type. Adding is offered when `viewerCaps.canManageLinks` (the link create
 * endpoint's gate); each link's remove button follows its own `canRemove` (the delete
 * endpoint's gate on the link's source entry). The section carries the `#related` anchor the
 * legacy `#links` tab redirects to.
 */
import {
	ActionIcon,
	Badge,
	Button,
	Group,
	Paper,
	Select,
	Stack,
	Text,
	TextInput,
	Title,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	useCreateEntryLink,
	useDeleteEntryLink,
	useEntryLinks,
	useKnowledgeEntriesPaged,
} from "../../../hooks/useKnowledge";
import type { KnowledgeEntryLink, KnowledgeLinkType } from "../../../lib/api";
import { KNOWLEDGE_RELATED_ANCHOR } from "../../../lib/knowledge-review-nav";
import { AnchorLink } from "../router-links";
import { groupLinksByType, LINK_TYPES } from "./entry-actions";

export function EntryRelatedSection({
	entryId,
	collectionId,
	canManageLinks,
}: {
	entryId: string;
	collectionId: string;
	canManageLinks: boolean;
}) {
	const { t } = useTranslation("knowledge");
	const links = useEntryLinks(entryId, "both");
	const deleteLink = useDeleteEntryLink();
	const [adding, setAdding] = useState(false);

	const all = links.data ?? [];
	const outGroups = groupLinksByType(all.filter((l) => l.direction === "out"));
	const inGroups = groupLinksByType(all.filter((l) => l.direction === "in"));
	const remove = (linkId: string) => deleteLink.mutate({ linkId, entryId });

	return (
		<Stack gap="sm" id={KNOWLEDGE_RELATED_ANCHOR} style={{ scrollMarginTop: 80 }}>
			<Group justify="space-between" align="center">
				<Title order={4}>{t("relatedTitle")}</Title>
				{canManageLinks && !adding ? (
					<Button
						size="compact-sm"
						variant="subtle"
						leftSection={<IconPlus size={14} />}
						onClick={() => setAdding(true)}
					>
						{t("linkAdd")}
					</Button>
				) : null}
			</Group>

			{canManageLinks && adding ? (
				<AddLinkForm
					entryId={entryId}
					collectionId={collectionId}
					onDone={() => setAdding(false)}
				/>
			) : null}

			{links.isLoading ? (
				<Text size="sm" c="dimmed">
					{t("loading")}
				</Text>
			) : all.length === 0 ? (
				<Text size="sm" c="dimmed">
					{t("relatedEmpty")}
				</Text>
			) : (
				<Stack gap="md">
					{outGroups.length > 0 ? (
						<LinkGroups
							heading={t("linkOutgoing")}
							groups={outGroups}
							otherEndOf={(l) => l.toEntry}
							onRemove={remove}
							removing={deleteLink.isPending}
						/>
					) : null}
					{inGroups.length > 0 ? (
						<LinkGroups
							heading={t("linkIncoming")}
							groups={inGroups}
							otherEndOf={(l) => l.fromEntry}
							onRemove={remove}
							removing={deleteLink.isPending}
						/>
					) : null}
				</Stack>
			)}
			{deleteLink.isError ? (
				<Text size="xs" c="red">
					{(deleteLink.error as Error).message}
				</Text>
			) : null}
		</Stack>
	);
}

function LinkGroups({
	heading,
	groups,
	otherEndOf,
	onRemove,
	removing,
}: {
	heading: string;
	groups: { type: KnowledgeLinkType; links: KnowledgeEntryLink[] }[];
	otherEndOf: (l: KnowledgeEntryLink) => KnowledgeEntryLink["toEntry"];
	onRemove: (linkId: string) => void;
	removing: boolean;
}) {
	const { t } = useTranslation("knowledge");
	return (
		<div>
			<Text size="xs" fw={700} c="dimmed" tt="uppercase" mb={6}>
				{heading}
			</Text>
			<Stack gap="xs">
				{groups.map((g) => (
					<Group key={g.type} align="flex-start" gap="sm" wrap="nowrap">
						<Badge size="sm" variant="light" color="indigo" w={110} style={{ flexShrink: 0 }}>
							{t(`linkType_${g.type}`)}
						</Badge>
						<Stack gap={4} style={{ minWidth: 0, flex: 1 }}>
							{g.links.map((l) => {
								const other = otherEndOf(l);
								return (
									<Group key={l.id} gap="xs" wrap="nowrap" justify="space-between">
										<Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
											<AnchorLink
												to="/knowledge/$entryId"
												params={{ entryId: other.id }}
												size="sm"
												truncate="end"
											>
												{other.title}
											</AnchorLink>
											{l.label ? (
												<Text size="xs" c="dimmed" truncate="end">
													{l.label}
												</Text>
											) : null}
										</Group>
										{l.canRemove ? (
											<ActionIcon
												variant="subtle"
												color="red"
												size="sm"
												disabled={removing}
												aria-label={t("linkDelete")}
												onClick={() => onRemove(l.id)}
											>
												<IconTrash size={14} />
											</ActionIcon>
										) : null}
									</Group>
								);
							})}
						</Stack>
					</Group>
				))}
			</Stack>
		</div>
	);
}

function AddLinkForm({
	entryId,
	collectionId,
	onDone,
}: {
	entryId: string;
	collectionId: string;
	onDone: () => void;
}) {
	const { t } = useTranslation("knowledge");
	const createLink = useCreateEntryLink();
	// Candidate targets: readable entries in the same collection (excluding self). Without a
	// search term only the most recently updated page is listed; typing searches the whole
	// collection server-side, so older entries stay reachable.
	const [targetSearch, setTargetSearch] = useState("");
	const [debouncedSearch] = useDebouncedValue(targetSearch.trim(), 250);
	const candidates = useKnowledgeEntriesPaged({
		collectionId,
		q: debouncedSearch || undefined,
		limit: 100,
	});
	const [targetId, setTargetId] = useState<string | null>(null);
	const [targetLabel, setTargetLabel] = useState<string | null>(null);
	const [linkType, setLinkType] = useState<KnowledgeLinkType>("related");
	const [label, setLabel] = useState("");

	const targetOptions = useMemo(() => {
		const rows = (candidates.data?.pages ?? []).flatMap((p) => p.items);
		const opts = rows.filter((r) => r.id !== entryId).map((r) => ({ value: r.id, label: r.title }));
		// Keep the current selection selectable after the result set changes.
		if (targetId && targetLabel && !opts.some((o) => o.value === targetId)) {
			opts.unshift({ value: targetId, label: targetLabel });
		}
		return opts;
	}, [candidates.data, entryId, targetId, targetLabel]);
	const candidatesTruncated = !debouncedSearch && !!candidates.hasNextPage;

	const submit = () => {
		if (!targetId) return;
		createLink.mutate(
			{ entryId, toEntryId: targetId, linkType, label: label.trim() || undefined },
			{ onSuccess: onDone },
		);
	};

	return (
		<Paper withBorder p="md" radius="md">
			<Text size="xs" c="dimmed" mb="sm">
				{t("linkAddDesc")}
			</Text>
			<Group align="flex-end" gap="xs" wrap="wrap">
				<Select
					label={t("linkTarget")}
					placeholder={t("linkTargetPlaceholder")}
					description={candidatesTruncated ? t("linkTargetTruncated") : undefined}
					data={targetOptions}
					value={targetId}
					onChange={(v, option) => {
						setTargetId(v);
						setTargetLabel(option?.label ?? null);
					}}
					searchable
					searchValue={targetSearch}
					onSearchChange={setTargetSearch}
					// Server search (FTS) also matches body text: don't re-filter by label.
					filter={debouncedSearch ? ({ options }) => options : undefined}
					w={260}
					nothingFoundMessage={t("linkNoCandidates")}
				/>
				<Select
					label={t("linkType")}
					data={LINK_TYPES.map((lt) => ({ value: lt, label: t(`linkType_${lt}`) }))}
					value={linkType}
					onChange={(v) => setLinkType((v as KnowledgeLinkType) ?? "related")}
					allowDeselect={false}
					w={160}
				/>
				<TextInput
					label={t("linkLabel")}
					placeholder={t("linkLabelPlaceholder")}
					value={label}
					onChange={(ev) => setLabel(ev.currentTarget.value)}
					w={200}
				/>
				<Button variant="subtle" size="sm" onClick={onDone}>
					{t("cancel")}
				</Button>
				<Button size="sm" loading={createLink.isPending} disabled={!targetId} onClick={submit}>
					{t("linkCreate")}
				</Button>
			</Group>
			{createLink.isError ? (
				<Text size="xs" c="red" mt="xs">
					{(createLink.error as Error).message}
				</Text>
			) : null}
		</Paper>
	);
}
