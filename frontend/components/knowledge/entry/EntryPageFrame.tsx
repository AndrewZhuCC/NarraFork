/**
 * Shared frame of the entry pages (read / history / edit / settings, §5.2): loads the entry
 * (one cached query — `useKnowledgeEntry`), renders the loading and not-found states, and the
 * breadcrumb "Knowledge › collection [› entry › section]". The pages only lay out their body.
 */
import { Anchor, Breadcrumbs, Container, Paper, Stack, Text, Title } from "@mantine/core";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useKnowledgeCollections, useKnowledgeEntry } from "../../../hooks/useKnowledge";
import type { KnowledgeCollectionListItem, KnowledgeEntryDetail } from "../../../lib/api";
import { AnchorLink } from "../router-links";

export function EntryPageFrame({
	entryId,
	section,
	children,
}: {
	entryId: string;
	/** Sub-page name (History / Edit / Settings); omitted on the read page. */
	section?: string;
	children: (
		entry: KnowledgeEntryDetail,
		collection: KnowledgeCollectionListItem | undefined,
	) => ReactNode;
}) {
	const { t } = useTranslation("knowledge");
	const entry = useKnowledgeEntry(entryId);
	const collections = useKnowledgeCollections();

	if (entry.isLoading) {
		return (
			<Container size="lg" py="lg">
				<Text c="dimmed">{t("loading")}</Text>
			</Container>
		);
	}
	if (!entry.data) {
		return (
			<Container size="lg" py="lg">
				<Paper withBorder p="xl" radius="md" ta="center">
					<Stack gap="xs" align="center">
						<Title order={4}>{t("entryNotFound")}</Title>
						<Text size="sm" c="dimmed">
							{t("entryNotFoundDesc")}
						</Text>
						<AnchorLink to="/knowledge" size="sm">
							{t("backToList")}
						</AnchorLink>
					</Stack>
				</Paper>
			</Container>
		);
	}

	const e = entry.data;
	const collection = collections.data?.find((c) => c.id === e.collectionId);
	const crumbs = [
		<AnchorLink key="kb" to="/knowledge" size="sm">
			{t("title")}
		</AnchorLink>,
		collection ? (
			<AnchorLink key="col" to="/knowledge" search={{ collection: collection.id }} size="sm">
				{collection.name}
			</AnchorLink>
		) : (
			<Anchor key="col" component="span" size="sm" c="dimmed">
				…
			</Anchor>
		),
	];
	if (section) {
		crumbs.push(
			<AnchorLink key="entry" to="/knowledge/$entryId" params={{ entryId }} size="sm">
				{e.title}
			</AnchorLink>,
			<Text key="section" size="sm" c="dimmed">
				{section}
			</Text>,
		);
	}

	return (
		<Container size="lg" py="lg">
			<Breadcrumbs mb="sm" separatorMargin={6} style={{ flexWrap: "wrap" }}>
				{crumbs}
			</Breadcrumbs>
			{children(e, collection)}
		</Container>
	);
}
