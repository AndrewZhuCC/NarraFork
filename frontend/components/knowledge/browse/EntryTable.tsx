/**
 * Table-like browse rows (§5.1): title (+ search snippet), tags, effective level, last update,
 * owner, and "my version" / "behind main" markers from the server's per-row annotations. The
 * title is a real link; the rest of the row opens the entry too. Clicking a tag filters by it.
 */
import { Badge, Group, Stack, Table, Text, Tooltip } from "@mantine/core";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import type { KnowledgeCollectionListItem, KnowledgeEntryListItem } from "../../../lib/api";
import { formatLocaleDateTime } from "../../../lib/intl-format";
import cardClasses from "../KnowledgeCards.module.css";
import { AnchorLink } from "../router-links";

export interface BrowseRow {
	id: string;
	title: string;
	snippet: string | null;
	tags: string[];
	level: string | null;
	levelInherited: boolean;
	status: string;
	updatedAt: string;
	ownerUsername: string | null;
	myPersonalEntryId: string | null;
	behindMain: boolean;
}

/** List rows carry `tagsJson`; search rows carry `tags` + `snippet`. Level is the effective one. */
export function toBrowseRow(
	e: KnowledgeEntryListItem,
	collectionsById: Map<string, KnowledgeCollectionListItem>,
): BrowseRow {
	const isSearch = "snippet" in e;
	const tags = isSearch ? (e.tags ?? []) : Array.isArray(e.tagsJson) ? e.tagsJson : [];
	const own = e.classificationLevel ?? null;
	const inherited = own ? null : (collectionsById.get(e.collectionId)?.defaultLevel ?? null);
	return {
		id: e.id,
		title: e.title,
		snippet: isSearch ? e.snippet : null,
		tags,
		level: own ?? inherited,
		levelInherited: !own && !!inherited,
		status: e.status,
		updatedAt: e.updatedAt,
		ownerUsername: e.ownerUsername,
		myPersonalEntryId: e.myPersonalEntryId,
		behindMain: e.myPersonalEntryBehindMain,
	};
}

export function EntryTable({
	rows,
	onTagClick,
}: {
	rows: BrowseRow[];
	onTagClick: (tag: string) => void;
}) {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	return (
		<Table.ScrollContainer minWidth={640}>
			<Table verticalSpacing="sm" highlightOnHover={false}>
				<Table.Thead>
					<Table.Tr>
						<Table.Th>{t("title_field")}</Table.Th>
						<Table.Th w={160}>{t("tags")}</Table.Th>
						<Table.Th w={110}>{t("classificationLevel")}</Table.Th>
						<Table.Th w={150}>{t("browseUpdated")}</Table.Th>
						<Table.Th w={120}>{t("owner")}</Table.Th>
					</Table.Tr>
				</Table.Thead>
				<Table.Tbody>
					{rows.map((r) => (
						<Table.Tr
							key={r.id}
							className={cardClasses.browseRow}
							onClick={() => navigate({ to: "/knowledge/$entryId", params: { entryId: r.id } })}
						>
							<Table.Td>
								<Stack gap={2}>
									<Group gap={6} wrap="wrap">
										<AnchorLink
											to="/knowledge/$entryId"
											params={{ entryId: r.id }}
											size="sm"
											fw={600}
											onClick={(e) => e.stopPropagation()}
										>
											{r.title}
										</AnchorLink>
										{r.status === "archived" ? (
											<Badge size="xs" color="gray" variant="light">
												{t("statusArchived")}
											</Badge>
										) : null}
										{r.myPersonalEntryId ? (
											<Badge size="xs" color="grape" variant="light">
												{t("browseHasMyVersion")}
											</Badge>
										) : null}
										{r.behindMain ? (
											<Tooltip label={t("driftBadgeTooltip")}>
												<Badge size="xs" color="yellow" variant="light">
													{t("driftBadge")}
												</Badge>
											</Tooltip>
										) : null}
									</Group>
									{r.snippet ? (
										<Text size="xs" c="dimmed" lineClamp={2} style={{ wordBreak: "break-word" }}>
											{r.snippet}
										</Text>
									) : null}
								</Stack>
							</Table.Td>
							<Table.Td>
								<Group gap={4}>
									{r.tags.slice(0, 4).map((tag) => (
										<Badge
											key={tag}
											size="xs"
											variant="light"
											style={{ cursor: "pointer" }}
											onClick={(e) => {
												e.stopPropagation();
												onTagClick(tag);
											}}
										>
											{tag}
										</Badge>
									))}
								</Group>
							</Table.Td>
							<Table.Td>
								{r.level ? (
									<Tooltip label={t("levelInherited")} disabled={!r.levelInherited}>
										<Badge size="xs" color="grape" variant={r.levelInherited ? "outline" : "light"}>
											{r.level}
										</Badge>
									</Tooltip>
								) : (
									<Text size="xs" c="dimmed">
										—
									</Text>
								)}
							</Table.Td>
							<Table.Td>
								<Text size="xs" c="dimmed">
									{formatLocaleDateTime(r.updatedAt)}
								</Text>
							</Table.Td>
							<Table.Td>
								<Text size="xs" c="dimmed" truncate="end">
									{r.ownerUsername ?? "—"}
								</Text>
							</Table.Td>
						</Table.Tr>
					))}
				</Table.Tbody>
			</Table>
		</Table.ScrollContainer>
	);
}
