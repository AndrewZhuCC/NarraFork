import { Button, Group, Loader, SegmentedControl, Stack, Text, Title } from "@mantine/core";
import { createFileRoute } from "@tanstack/react-router";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { EntityResultCard } from "../components/search/EntityResultCard";
import { SearchFilterBar, type SearchFilterState } from "../components/search/SearchFilterBar";
import { SessionResultCard } from "../components/search/SessionResultCard";
import { SEARCH_SECTION_ORDER, useSearchOverview, useSearchSection } from "../hooks/useSearch";
import type {
	EntitySearchResult,
	SearchRequest,
	SearchSectionName,
	SearchSectionPage,
	SessionSearchResult,
} from "../lib/api";
import {
	DEFAULT_SEARCH_SORT,
	formatSearchTotal,
	normalizeSearchInclude,
	normalizeSearchRange,
	normalizeSearchSort,
	normalizeSearchTab,
	type SearchTab,
	type SearchTimeRange,
	searchRangeStart,
} from "../lib/search-utils";

interface SearchParams {
	q?: string;
	/** The open tab; absent means the overview of every section. */
	tab?: string;
	sort?: string;
	/** Comma-separated content added to the default scope. */
	include?: string;
	project?: string;
	chapter?: string;
	range?: string;
	mine?: string;
}

const optionalId = (value: unknown) =>
	typeof value === "string" && value.length > 0 && value.length <= 64 ? value : undefined;

export const Route = createFileRoute("/search")({
	component: SearchPage,
	validateSearch: (search: Record<string, unknown>): SearchParams => {
		// `type` is the pre-sections name of `tab`; old links keep opening the right tab.
		const tab = normalizeSearchTab(search.tab ?? search.type);
		const sort = normalizeSearchSort(search.sort);
		return {
			q: typeof search.q === "string" ? search.q : undefined,
			tab: tab === "all" ? undefined : tab,
			sort: sort === DEFAULT_SEARCH_SORT ? undefined : sort,
			include: normalizeSearchInclude(search.include).join(",") || undefined,
			project: optionalId(search.project),
			chapter: optionalId(search.chapter),
			range: normalizeSearchRange(search.range),
			mine: search.mine === "1" || search.mine === 1 || search.mine === true ? "1" : undefined,
		};
	},
});

/** Every section's cards render through this one switch. */
function ResultCard({
	result,
	request,
}: {
	result: SessionSearchResult | EntitySearchResult;
	request: SearchRequest;
}) {
	return result.type === "session" ? (
		<SessionResultCard result={result} request={request} />
	) : (
		<EntityResultCard result={result} query={request.q} />
	);
}

/** The overview: each section's best few, with a way into the full section. */
function Overview({
	sections,
	request,
	onOpen,
}: {
	sections: Partial<
		Record<SearchSectionName, SearchSectionPage<SessionSearchResult | EntitySearchResult>>
	>;
	request: SearchRequest;
	onOpen: (tab: SearchSectionName) => void;
}) {
	const { t } = useTranslation("search");
	const visible = SEARCH_SECTION_ORDER.filter((name) => (sections[name]?.items.length ?? 0) > 0);
	if (visible.length === 0) return <Text c="dimmed">{t("noResults")}</Text>;
	return (
		<Stack gap="lg">
			{visible.map((name) => {
				const page = sections[name] as SearchSectionPage<SessionSearchResult | EntitySearchResult>;
				return (
					<Stack key={name} gap="xs">
						<Group justify="space-between" align="center">
							<Title order={4}>
								{t(`section_${name}`)}{" "}
								<Text span c="dimmed" fw={400}>
									{formatSearchTotal(page)}
								</Text>
							</Title>
							{page.nextCursor && (
								<Button size="compact-sm" variant="subtle" onClick={() => onOpen(name)}>
									{t("viewAll", { count: formatSearchTotal(page) })}
								</Button>
							)}
						</Group>
						{page.items.map((result) => (
							<ResultCard key={`${result.type}-${result.id}`} result={result} request={request} />
						))}
					</Stack>
				);
			})}
		</Stack>
	);
}

/** One section, paged with "load more". */
function SectionList({ section, request }: { section: SearchSectionName; request: SearchRequest }) {
	const { t } = useTranslation("search");
	const query = useSearchSection(request, section, true);
	const items =
		query.data?.pages.flatMap(
			(page) => page.items as Array<SessionSearchResult | EntitySearchResult>,
		) ?? [];
	if (query.isLoading) return <Loader />;
	if (items.length === 0) return <Text c="dimmed">{t("noResults")}</Text>;
	return (
		<Stack>
			{items.map((result) => (
				<ResultCard key={`${result.type}-${result.id}`} result={result} request={request} />
			))}
			{query.hasNextPage && (
				<Button
					variant="light"
					loading={query.isFetchingNextPage}
					onClick={() => query.fetchNextPage()}
				>
					{t("loadMore")}
				</Button>
			)}
		</Stack>
	);
}

function SearchPage() {
	const params = Route.useSearch();
	const navigate = Route.useNavigate();
	const { t } = useTranslation("search");
	const tab: SearchTab = normalizeSearchTab(params.tab);
	const filters: SearchFilterState = useMemo(
		() => ({
			sort: normalizeSearchSort(params.sort),
			include: normalizeSearchInclude(params.include),
			projectId: params.project,
			chapterId: params.chapter,
			range: normalizeSearchRange(params.range) as SearchTimeRange | undefined,
			mine: params.mine === "1",
		}),
		[params],
	);
	const request: SearchRequest = useMemo(
		() => ({
			q: (params.q ?? "").trim(),
			sort: filters.sort,
			include: filters.include,
			projectId: filters.projectId,
			chapterId: filters.chapterId,
			from: filters.range ? searchRangeStart(filters.range) : undefined,
			mine: filters.mine,
		}),
		[params.q, filters],
	);
	const overview = useSearchOverview(request);
	const sections = overview.data?.sections ?? {};

	const setFilters = (patch: Partial<SearchFilterState>) => {
		const next = { ...filters, ...patch };
		navigate({
			search: (prev) => ({
				...prev,
				sort: next.sort === DEFAULT_SEARCH_SORT ? undefined : next.sort,
				include: next.include.join(",") || undefined,
				project: next.projectId,
				chapter: next.chapterId,
				range: next.range,
				mine: next.mine ? "1" : undefined,
			}),
			replace: true,
		});
	};
	const openTab = (value: SearchTab) =>
		navigate({
			search: (prev) => ({ ...prev, tab: value === "all" ? undefined : value }),
			replace: true,
		});

	const tabLabel = (name: SearchSectionName) =>
		t(`tab_${name}`, { count: formatSearchTotal(sections[name]) });

	return (
		<Stack>
			<Title order={2}>{t("title")}</Title>
			{request.q ? (
				<Text c="dimmed" size="sm">
					{t("resultsFor", { query: request.q })}
				</Text>
			) : (
				<Text c="dimmed">{t("enterQuery")}</Text>
			)}

			{request.q && (
				<>
					<SearchFilterBar state={filters} onChange={setFilters} />
					<SegmentedControl
						size="xs"
						value={tab}
						onChange={(value) => openTab(normalizeSearchTab(value))}
						data={[
							{ value: "all", label: t("tab_all") },
							...SEARCH_SECTION_ORDER.map((name) => ({ value: name, label: tabLabel(name) })),
						]}
						style={{ alignSelf: "flex-start" }}
					/>
					{tab === "all" ? (
						overview.isLoading ? (
							<Loader />
						) : (
							<Overview sections={sections} request={request} onOpen={openTab} />
						)
					) : (
						<SectionList key={tab} section={tab} request={request} />
					)}
				</>
			)}
		</Stack>
	);
}
