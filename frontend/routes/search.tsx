import {
	Alert,
	Badge,
	Card,
	Chip,
	Group,
	Loader,
	SegmentedControl,
	Select,
	Stack,
	Text,
	Title,
} from "@mantine/core";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useSearch } from "../hooks/useSearch";
import { formatSmartTime } from "../lib/format";
import {
	DEFAULT_SEARCH_SORT,
	getSearchResultDisplayTitle,
	highlightSearchText,
	normalizeSearchInclude,
	normalizeSearchSort,
	normalizeSearchType,
	SEARCH_INCLUDES,
	type SearchInclude,
	type SearchSortMode,
	sortVisibleSearchResults,
	summarizeSearchRuntimeState,
} from "../lib/search-utils";

const linkStyle = { textDecoration: "none", color: "inherit" } as const;
/** Badge color per result type; unknown types fall back to grape. */
const RESULT_TYPE_COLORS: Record<string, string> = {
	narrator: "indigo",
	message: "grape",
	knowledge: "teal",
};
const MAX_VISIBLE_RESULTS = 200;
const MAX_SEARCH_TITLE_CHARS = 500;
const MAX_SEARCH_SNIPPET_CHARS = 2_000;
const MAX_SEARCH_META_CHARS = 500;

function clampSearchText(value: string | undefined, maxChars: number): string | undefined {
	if (!value) return value;
	return value.length > maxChars ? value.slice(0, maxChars) : value;
}

function humanizeEnumValue(value: string): string {
	const normalized = value.replace(/[_-]+/g, " ").trim();
	if (!normalized) return value;
	return normalized.replace(/\b\w/g, (char) => char.toUpperCase());
}

interface MessageHit {
	id: string;
	snippet?: string;
	messageRole?: string;
	createdAt?: string;
}

/** Link to one message inside a narrator. */
function messageLink(narratorId: string, messageId: string) {
	return {
		to: "/narrators/$narratorId",
		params: { narratorId },
		hash: `msg-${messageId}`,
	} as const;
}

/** Resolve the navigation target for a search result */
function getResultLink(
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	result: any,
): { to: string; params: Record<string, string>; hash?: string } | null {
	if (result.type === "message") {
		const hash = `msg-${result.id}`;
		if (result.narratorId) {
			return { to: "/narrators/$narratorId", params: { narratorId: result.narratorId }, hash };
		}
	}
	if (result.type === "narrator") {
		return { to: "/narrators/$narratorId", params: { narratorId: result.id } };
	}
	if (result.type === "knowledge") {
		return { to: "/knowledge/$entryId", params: { entryId: result.id } };
	}
	return null;
}

interface SearchParams {
	q?: string;
	type?: string;
	sort?: SearchSortMode;
	/** Comma-separated {@link SearchInclude} names; absent means the default scope. */
	include?: string;
}

export const Route = createFileRoute("/search")({
	component: SearchPage,
	validateSearch: (search: Record<string, unknown>): SearchParams => ({
		q: typeof search.q === "string" ? search.q : undefined,
		type: normalizeSearchType(search.type),
		sort: normalizeSearchSort(search.sort),
		include: normalizeSearchInclude(search.include).join(",") || undefined,
	}),
});

function SearchPage() {
	const { q, type = "all", sort = DEFAULT_SEARCH_SORT, include: includeParam } = Route.useSearch();
	const navigate = Route.useNavigate();
	const include = useMemo(() => normalizeSearchInclude(includeParam), [includeParam]);
	const { data, isLoading } = useSearch(q ?? "", "messages,narrators,knowledge", sort, include);
	const { t } = useTranslation("search");
	const searchRuntimeStatus = useMemo(() => summarizeSearchRuntimeState(data), [data]);
	const translateSearchEnum = (prefix: string, value: string) =>
		t(`${prefix}_${value}`, { defaultValue: humanizeEnumValue(value) });
	const results = useMemo(
		() =>
			sortVisibleSearchResults(data?.results ?? [], normalizeSearchType(type), sort, (id) =>
				t("resultUntitled", { id }),
			),
		[data?.results, type, sort, t],
	);
	const counts = useMemo(() => {
		return ((data?.results ?? []) as Array<{ type?: string }>).reduce(
			(acc, result) => {
				acc.all += 1;
				switch (result.type) {
					case "narrator":
						acc.narrator += 1;
						break;
					case "message":
						acc.message += 1;
						break;
					case "knowledge":
						acc.knowledge += 1;
						break;
				}
				return acc;
			},
			{ all: 0, narrator: 0, message: 0, knowledge: 0 },
		);
	}, [data?.results]);
	const displayedResults = useMemo(() => results.slice(0, MAX_VISIBLE_RESULTS), [results]);
	const displayedResultEntries = useMemo(
		() =>
			displayedResults.map((result) => ({
				result,
				key: `${result.type}-${result.id}`,
				link: getResultLink(result),
				title:
					clampSearchText(
						getSearchResultDisplayTitle(result, (id) => t("resultUntitled", { id })),
						MAX_SEARCH_TITLE_CHARS,
					) ?? "",
				snippet: clampSearchText(result.snippet, MAX_SEARCH_SNIPPET_CHARS),
				projectName: clampSearchText(result.projectName, MAX_SEARCH_META_CHARS),
				narratorTitle: clampSearchText(result.narratorTitle, MAX_SEARCH_META_CHARS),
				collectionName: clampSearchText(result.collectionName, MAX_SEARCH_META_CHARS),
				timestamp: result.updatedAt ?? result.createdAt ?? result.lastMessageAt,
			})),
		[displayedResults, t],
	);
	const hiddenResultCount = results.length - displayedResults.length;

	return (
		<Stack>
			<Title order={2}>{t("title")}</Title>
			{q && (
				<Text c="dimmed" size="sm">
					{t("resultsFor", { query: q })}
				</Text>
			)}

			{searchRuntimeStatus.degraded && (
				<Alert color="yellow" variant="light" title={t("degradedTitle")}>
					<Stack gap={4}>
						<Text size="sm">
							{t("degradedDescription", {
								mode: searchRuntimeStatus.mode ?? t("degradedModeUnknown"),
							})}
						</Text>
						{searchRuntimeStatus.fallbackMessages.map((message) => (
							<Text key={message} size="xs" c="dimmed">
								{message}
							</Text>
						))}
					</Stack>
				</Alert>
			)}

			{q && (
				<Group gap="xs" align="center">
					<Text size="xs" c="dimmed">
						{t("includeLabel")}
					</Text>
					<Chip.Group
						multiple
						value={include}
						onChange={(value) =>
							navigate({
								search: (prev) => ({
									...prev,
									include: normalizeSearchInclude(value).join(",") || undefined,
								}),
								replace: true,
							})
						}
					>
						<Group gap={6}>
							{SEARCH_INCLUDES.map((name: SearchInclude) => (
								<Chip key={name} value={name} size="xs" variant="outline">
									{t(`include_${name}`)}
								</Chip>
							))}
						</Group>
					</Chip.Group>
				</Group>
			)}

			{data?.results?.length ? (
				<Group justify="space-between" align="center">
					<SegmentedControl
						size="xs"
						value={type}
						onChange={(value) =>
							navigate({
								search: (prev) => ({ ...prev, type: value === "all" ? undefined : value }),
								replace: true,
							})
						}
						data={[
							{ value: "all", label: t("typeAll", { count: counts.all }) },
							{ value: "narrator", label: t("typeNarrator", { count: counts.narrator }) },
							{ value: "message", label: t("typeMessage", { count: counts.message }) },
							{ value: "knowledge", label: t("typeKnowledge", { count: counts.knowledge }) },
						]}
					/>
					<Select
						size="xs"
						w={150}
						value={sort}
						allowDeselect={false}
						onChange={(value) =>
							value &&
							navigate({
								search: (prev) => ({
									...prev,
									// `time` is the default, so it stays out of the URL; any other mode is
									// explicit and must survive a reload / shared link.
									sort: value === DEFAULT_SEARCH_SORT ? undefined : normalizeSearchSort(value),
								}),
								replace: true,
							})
						}
						data={[
							{ value: "time", label: t("sortTime") },
							{ value: "relevance", label: t("sortRelevance") },
							{ value: "type", label: t("sortType") },
							{ value: "title", label: t("sortTitle") },
						]}
					/>
				</Group>
			) : null}

			{isLoading ? (
				<Loader />
			) : !data?.results?.length ? (
				<Text c="dimmed">{q ? t("noResults") : t("enterQuery")}</Text>
			) : !results.length ? (
				<Text c="dimmed">{t("noFilteredResults")}</Text>
			) : (
				<Stack>
					{hiddenResultCount > 0 && (
						<Alert color="blue" radius="md">
							<Text size="sm">
								{t("tooManyResults", {
									limit: MAX_VISIBLE_RESULTS,
									total: results.length,
									defaultValue:
										"Showing the first {{limit}} of {{total}} filtered results. Refine the search to see fewer results.",
								})}
							</Text>
						</Alert>
					)}
					{displayedResultEntries.map(
						({
							result,
							key,
							link,
							title,
							snippet,
							projectName,
							narratorTitle,
							collectionName,
							timestamp,
						}) => {
							const hits: MessageHit[] =
								result.type === "message" && result.narratorId && Array.isArray(result.hits)
									? result.hits
									: [];
							const grouped = hits.length > 0;
							const titleNode = (
								<Text fw={500} truncate>
									{highlightSearchText(title, q ?? "")}
								</Text>
							);
							const card = (
								<Card
									key={key}
									shadow="sm"
									padding="md"
									withBorder
									style={{
										textDecoration: "none",
										cursor: link && !grouped ? "pointer" : undefined,
									}}
								>
									<Group gap="xs" mb={6} justify="space-between" wrap="nowrap">
										<Group gap="xs" style={{ minWidth: 0 }}>
											<Badge size="xs" color={RESULT_TYPE_COLORS[result.type] ?? "grape"}>
												{t(`label_${result.type}`)}
											</Badge>
											{grouped && link ? (
												<Link
													to={link.to}
													params={link.params}
													hash={link.hash}
													style={{ ...linkStyle, minWidth: 0 }}
												>
													{titleNode}
												</Link>
											) : (
												titleNode
											)}
											{grouped && (
												<Badge size="xs" variant="light" color="grape">
													{t(result.hitCountIsLowerBound ? "hitCountAtLeast" : "hitCount", {
														count: result.hitCount ?? hits.length,
													})}
												</Badge>
											)}
										</Group>
										<Badge size="xs" variant="light" color="gray">
											{t("score", { score: result.matchScore ?? 0 })}
										</Badge>
									</Group>
									{grouped ? (
										<Stack gap={4} mb={8}>
											{hits.map((hit) => (
												<Link
													key={hit.id}
													{...messageLink(result.narratorId, hit.id)}
													style={linkStyle}
												>
													<Group gap={6} wrap="nowrap" align="flex-start">
														{hit.messageRole && (
															<Badge size="xs" variant="dot" style={{ flexShrink: 0 }}>
																{translateSearchEnum("messageRole", hit.messageRole)}
															</Badge>
														)}
														<Text size="sm" c="dimmed" lineClamp={2} style={{ minWidth: 0 }}>
															{highlightSearchText(
																clampSearchText(hit.snippet, MAX_SEARCH_SNIPPET_CHARS) ?? "",
																q ?? "",
															)}
														</Text>
													</Group>
												</Link>
											))}
										</Stack>
									) : (
										<Text size="sm" c="dimmed" lineClamp={3} mb={8}>
											{highlightSearchText(snippet ?? "", q ?? "")}
										</Text>
									)}
									<Group gap={6} wrap="wrap">
										{projectName && <Badge variant="outline">{projectName}</Badge>}
										{narratorTitle && !grouped && <Badge variant="light">{narratorTitle}</Badge>}
										{collectionName && (
											<Badge variant="outline" color="teal">
												{collectionName}
											</Badge>
										)}
										{result.status && (
											<Badge variant="dot">
												{translateSearchEnum(`status_${result.type}`, result.status)}
											</Badge>
										)}
										{result.messageRole && !grouped && (
											<Badge variant="dot">
												{translateSearchEnum("messageRole", result.messageRole)}
											</Badge>
										)}
										{result.matchField && (
											<Badge variant="outline">
												{t("matchField", {
													field: translateSearchEnum("matchField", result.matchField),
												})}
											</Badge>
										)}
										{timestamp && (
											<Text size="xs" c="dimmed">
												{formatSmartTime(timestamp)}
											</Text>
										)}
									</Group>
								</Card>
							);
							if (!link || grouped) return card;
							return (
								<Link
									key={key}
									to={link.to}
									params={link.params}
									hash={link.hash}
									style={linkStyle}
								>
									{card}
								</Link>
							);
						},
					)}
				</Stack>
			)}
		</Stack>
	);
}
