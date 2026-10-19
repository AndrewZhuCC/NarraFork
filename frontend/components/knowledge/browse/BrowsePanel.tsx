/**
 * The browse view of `/knowledge` (§5.1): collection tree | search + filter bar + entry table
 * with cursor "Load more" (P0). Every control writes the URL search params; the list query is
 * derived from them, so a filtered view is linkable and survives reload.
 */
import {
	Button,
	Grid,
	Group,
	Paper,
	Select,
	Stack,
	Text,
	TextInput,
	ThemeIcon,
} from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { IconFileText, IconFolderOff, IconSearch, IconTag } from "@tabler/icons-react";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	useKnowledgeCollections,
	useKnowledgeEntriesPaged,
	useKnowledgeLevels,
} from "../../../hooks/useKnowledge";
import type { KnowledgeEntrySort, KnowledgeEntryStatus } from "../../../lib/api";
import {
	browseEmptyState,
	effectiveBrowseSort,
	type KnowledgeBrowseSearch,
	parseKnowledgeBrowseSearch,
} from "../../../lib/knowledge-browse-search";
import { CollectionTree } from "./CollectionTree";
import { EntryTable, toBrowseRow } from "./EntryTable";

export function BrowsePanel({ search }: { search: KnowledgeBrowseSearch }) {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate({ from: "/knowledge/" });
	const collections = useKnowledgeCollections();
	const levels = useKnowledgeLevels();
	const setSearch = (patch: Partial<KnowledgeBrowseSearch>) =>
		navigate({
			search: (prev) => parseKnowledgeBrowseSearch({ ...prev, ...patch }),
			replace: true,
		});

	// Text inputs are local while typing and reach the URL debounced.
	const [qInput, setQInput] = useState(search.q ?? "");
	const [tagInput, setTagInput] = useState(search.tag ?? "");
	const [qDebounced] = useDebouncedValue(qInput.trim(), 300);
	const [tagDebounced] = useDebouncedValue(tagInput.trim(), 300);
	// URL → input (back/forward, tag badge clicks), without clobbering trailing spaces mid-typing.
	useEffect(() => {
		const next = search.q ?? "";
		setQInput((prev) => (prev.trim() === next ? prev : next));
	}, [search.q]);
	useEffect(() => {
		const next = search.tag ?? "";
		setTagInput((prev) => (prev.trim() === next ? prev : next));
	}, [search.tag]);
	// biome-ignore lint/correctness/useExhaustiveDependencies: only the debounced text drives the URL
	useEffect(() => {
		if (qDebounced !== (search.q ?? "")) setSearch({ q: qDebounced || undefined });
	}, [qDebounced]);
	// biome-ignore lint/correctness/useExhaustiveDependencies: only the debounced text drives the URL
	useEffect(() => {
		if (tagDebounced !== (search.tag ?? "")) setSearch({ tag: tagDebounced || undefined });
	}, [tagDebounced]);

	const sort = effectiveBrowseSort(search);
	const entries = useKnowledgeEntriesPaged({
		collectionId: search.collection,
		q: search.q,
		tag: search.tag,
		level: search.level,
		status: search.status,
		sort,
	});
	const collectionsById = useMemo(
		() => new Map((collections.data ?? []).map((c) => [c.id, c])),
		[collections.data],
	);
	const rows = useMemo(
		() =>
			(entries.data?.pages ?? [])
				.flatMap((p) => p.items)
				.map((e) => toBrowseRow(e, collectionsById)),
		[entries.data, collectionsById],
	);
	const sortOptions: { value: KnowledgeEntrySort; label: string }[] = [
		...(search.q ? [{ value: "relevance" as const, label: t("browseSort_relevance") }] : []),
		{ value: "updated", label: t("browseSort_updated") },
		{ value: "title", label: t("browseSort_title") },
	];
	const empty = browseEmptyState(search, collections.data?.length ?? 0);

	return (
		<Grid gap="md">
			<Grid.Col span={{ base: 12, sm: 4, md: 3 }}>
				<CollectionTree
					collections={collections.data ?? []}
					activeId={search.collection ?? null}
					onSelect={(id) => setSearch({ collection: id ?? undefined })}
				/>
			</Grid.Col>

			<Grid.Col span={{ base: 12, sm: 8, md: 9 }}>
				<Stack gap="md">
					<Paper withBorder p="sm" radius="md">
						<Stack gap="xs">
							<TextInput
								placeholder={t("searchPlaceholder")}
								value={qInput}
								onChange={(e) => setQInput(e.currentTarget.value)}
								leftSection={<IconSearch size={16} />}
								aria-label={t("searchPlaceholder")}
							/>
							<Group gap="xs" wrap="wrap">
								<TextInput
									size="xs"
									w={160}
									placeholder={t("filterByTag")}
									aria-label={t("filterByTag")}
									leftSection={<IconTag size={14} />}
									value={tagInput}
									onChange={(e) => setTagInput(e.currentTarget.value)}
								/>
								<Select
									size="xs"
									w={160}
									placeholder={t("browseAnyLevel")}
									aria-label={t("classificationLevel")}
									clearable
									data={(levels.data ?? []).map((l) => ({
										value: l.name,
										label: l.label || l.name,
									}))}
									value={search.level ?? null}
									onChange={(v) => setSearch({ level: v ?? undefined })}
								/>
								<Select
									size="xs"
									w={140}
									placeholder={t("browseAnyStatus")}
									aria-label={t("status")}
									clearable
									data={[
										{ value: "active", label: t("statusActive") },
										{ value: "archived", label: t("statusArchived") },
									]}
									value={search.status ?? null}
									onChange={(v) =>
										setSearch({ status: (v as KnowledgeEntryStatus | null) ?? undefined })
									}
								/>
								<Select
									size="xs"
									w={170}
									aria-label={t("browseSort")}
									allowDeselect={false}
									data={sortOptions}
									value={sort}
									onChange={(v) => v && setSearch({ sort: v as KnowledgeEntrySort })}
								/>
							</Group>
						</Stack>
					</Paper>

					{entries.isLoading ? (
						<Paper withBorder p="xl" radius="md" ta="center">
							<Text size="sm" c="dimmed">
								{t("loading")}
							</Text>
						</Paper>
					) : entries.isError ? (
						<Paper withBorder p="xl" radius="md" ta="center">
							<Text size="sm" c="red">
								{(entries.error as Error).message}
							</Text>
						</Paper>
					) : rows.length === 0 && !entries.hasNextPage ? (
						<Paper withBorder p="xl" radius="md" ta="center">
							<ThemeIcon variant="light" size="xl" radius="xl" color="gray" mb="xs">
								{empty === "noCollections" ? (
									<IconFolderOff size={24} />
								) : (
									<IconFileText size={24} />
								)}
							</ThemeIcon>
							<Text size="sm" fw={600} c="dimmed">
								{t(`browseEmpty_${empty}`)}
							</Text>
							<Text size="xs" c="dimmed" mt={4}>
								{t(`browseEmptyHint_${empty}`)}
							</Text>
						</Paper>
					) : (
						<Paper withBorder radius="md" p="xs">
							{rows.length === 0 ? (
								// A non-empty cursor with no rows: the server's scan budget ran out
								// behind entries this user cannot read. More may follow.
								<Text size="xs" c="dimmed" ta="center" py="xs">
									{t("entriesNoneVisibleYet")}
								</Text>
							) : (
								<EntryTable rows={rows} onTagClick={(tag) => setSearch({ tag })} />
							)}
							{entries.hasNextPage ? (
								<Group justify="center" my="xs">
									<Button
										variant="default"
										size="xs"
										loading={entries.isFetchingNextPage}
										onClick={() => entries.fetchNextPage()}
									>
										{t("loadMoreEntries")}
									</Button>
								</Group>
							) : null}
						</Paper>
					)}
				</Stack>
			</Grid.Col>
		</Grid>
	);
}
