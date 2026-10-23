import { Chip, Group, SegmentedControl, Select, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useProjects } from "../../hooks/useProjects";
import { api } from "../../lib/api";
import {
	normalizeSearchInclude,
	normalizeSearchRange,
	SEARCH_INCLUDES,
	SEARCH_TIME_RANGES,
	type SearchInclude,
	type SearchSortMode,
	type SearchTimeRange,
} from "../../lib/search-utils";

export interface SearchFilterState {
	sort: SearchSortMode;
	include: SearchInclude[];
	projectId?: string;
	chapterId?: string;
	range?: SearchTimeRange;
	mine: boolean;
}

interface NamedEntity {
	id: string;
	name?: string;
	title?: string;
}

/**
 * Scope and filters for global search: extra content to include, project and chapter, a
 * time window, only the viewer's own sessions, and the sort. Every change goes to the URL,
 * so a filtered search survives reloads and shared links.
 */
export function SearchFilterBar({
	state,
	onChange,
}: {
	state: SearchFilterState;
	onChange: (patch: Partial<SearchFilterState>) => void;
}) {
	const { t } = useTranslation("search");
	const projects = useProjects();
	const chapters = useQuery({
		queryKey: ["chapters", { projectId: state.projectId, status: undefined }],
		queryFn: () => api.listChapters(state.projectId as string),
		enabled: !!state.projectId,
	});
	const projectOptions = ((projects.data ?? []) as NamedEntity[]).map((project) => ({
		value: project.id,
		label: project.name || project.id,
	}));
	const chapterOptions = ((chapters.data ?? []) as NamedEntity[]).map((chapter) => ({
		value: chapter.id,
		label: chapter.title || chapter.id,
	}));

	return (
		<Stack gap="xs">
			<Group gap="xs" align="center">
				<Text size="xs" c="dimmed">
					{t("includeLabel")}
				</Text>
				<Chip.Group
					multiple
					value={state.include}
					onChange={(value) => onChange({ include: normalizeSearchInclude(value) })}
				>
					<Group gap={6}>
						{SEARCH_INCLUDES.map((name) => (
							<Chip key={name} value={name} size="xs" variant="outline">
								{t(`include_${name}`)}
							</Chip>
						))}
					</Group>
				</Chip.Group>
			</Group>
			<Group gap="xs" align="center" wrap="wrap">
				<Select
					size="xs"
					w={180}
					placeholder={t("projectPlaceholder")}
					data={projectOptions}
					value={state.projectId ?? null}
					onChange={(value) => onChange({ projectId: value ?? undefined, chapterId: undefined })}
					searchable
					clearable
					aria-label={t("projectPlaceholder")}
				/>
				{state.projectId && (
					<Select
						size="xs"
						w={180}
						placeholder={t("chapterPlaceholder")}
						data={chapterOptions}
						value={state.chapterId ?? null}
						onChange={(value) => onChange({ chapterId: value ?? undefined })}
						searchable
						clearable
						aria-label={t("chapterPlaceholder")}
					/>
				)}
				<SegmentedControl
					size="xs"
					value={state.range ?? "all"}
					onChange={(value) => onChange({ range: normalizeSearchRange(value) })}
					data={[
						{ value: "all", label: t("range_all") },
						...Object.keys(SEARCH_TIME_RANGES).map((range) => ({
							value: range,
							label: t(`range_${range}`),
						})),
					]}
				/>
				<Chip
					size="xs"
					variant="outline"
					checked={state.mine}
					onChange={(checked) => onChange({ mine: checked })}
				>
					{t("mineOnly")}
				</Chip>
				<Select
					size="xs"
					w={120}
					value={state.sort}
					allowDeselect={false}
					onChange={(value) => value && onChange({ sort: value === "time" ? "time" : "relevance" })}
					data={[
						{ value: "relevance", label: t("sortRelevance") },
						{ value: "time", label: t("sortTime") },
					]}
					aria-label={t("sortLabel")}
				/>
			</Group>
		</Stack>
	);
}
