/**
 * "My" — `/knowledge/mine` (KNOWLEDGE_UI_REDESIGN §5.4): the single entry point to the caller's
 * personal versions (linked copies and standalone entries in one list) and the publish requests
 * they started. Replaces the browse page's "My library" tab and its always-on create form;
 * creating goes through `/knowledge/mine/new`.
 */
import {
	Button,
	Container,
	Grid,
	Group,
	Paper,
	Stack,
	Switch,
	Text,
	ThemeIcon,
	Title,
} from "@mantine/core";
import { IconArrowLeft, IconNotebook, IconPlus } from "@tabler/icons-react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { MyRequestsSection } from "../../../components/knowledge/personal/MyRequestsSection";
import { PersonalEntryRow } from "../../../components/knowledge/personal/PersonalEntryRow";
import {
	hasUnknownDrafts,
	myRequestSections,
	openRequestByDraft,
	parseKnowledgeMineSearch,
	visiblePersonalEntries,
} from "../../../components/knowledge/personal/personal-entry-view";
import {
	useMyKnowledgeSubmissionOutcomes,
	useMyOpenKnowledgeSubmissions,
	useMyPersonalEntries,
} from "../../../hooks/useKnowledge";
import type { KnowledgePersonalEntrySummary } from "../../../lib/api";

export const Route = createFileRoute("/knowledge/mine/")({
	component: KnowledgeMinePage,
	validateSearch: parseKnowledgeMineSearch,
});

function KnowledgeMinePage() {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	const { archived: showArchived = false } = Route.useSearch();

	// Active versions always; archived ones only when shown, or when a request row needs one
	// for its label (an approved request archives its version). Both lists are server-bounded.
	const active = useMyPersonalEntries({ status: "active" });
	const openSubs = useMyOpenKnowledgeSubmissions();
	const outcomes = useMyKnowledgeSubmissionOutcomes();
	const sections = useMemo(
		() => myRequestSections(openSubs.data ?? [], outcomes.data ?? []),
		[openSubs.data, outcomes.data],
	);
	const needArchived =
		showArchived || hasUnknownDrafts([...sections.open, ...sections.outcomes], active.data ?? []);
	const archived = useMyPersonalEntries({ status: "archived" }, { enabled: needArchived });

	const all = useMemo(
		() =>
			[...(active.data ?? []), ...(needArchived ? (archived.data ?? []) : [])].sort((a, b) =>
				b.updatedAt.localeCompare(a.updatedAt),
			),
		[active.data, archived.data, needArchived],
	);
	const rows = visiblePersonalEntries(all, showArchived);
	const entriesById = useMemo(
		() => new Map<string, KnowledgePersonalEntrySummary>(all.map((p) => [p.id, p])),
		[all],
	);
	const openByDraft = useMemo(() => openRequestByDraft(openSubs.data ?? []), [openSubs.data]);
	const listLoading = active.isLoading || (showArchived && archived.isLoading);

	return (
		<Container size="lg" py="lg">
			<Group mb="xs">
				<Button
					component={Link}
					to="/knowledge"
					variant="subtle"
					size="compact-sm"
					leftSection={<IconArrowLeft size={14} />}
				>
					{t("backToList")}
				</Button>
			</Group>
			<Group justify="space-between" align="flex-start" mb="md" wrap="wrap">
				<Stack gap="xs">
					<Title order={2}>{t("mineTitle")}</Title>
					<Text size="sm" c="dimmed">
						{t("mineDesc")}
					</Text>
				</Stack>
				<Button component={Link} to="/knowledge/mine/new" leftSection={<IconPlus size={16} />}>
					{t("newPersonalEntry")}
				</Button>
			</Group>

			<Grid gap="lg">
				<Grid.Col span={{ base: 12, md: 7 }}>
					<Stack gap="sm">
						<Group justify="space-between" align="center">
							<Text size="sm" fw={600} c="dimmed" tt="uppercase">
								{t("mineVersionsTitle")}
							</Text>
							<Switch
								size="xs"
								label={t("mineShowArchived")}
								checked={showArchived}
								onChange={(e) =>
									navigate({
										to: "/knowledge/mine",
										search: e.currentTarget.checked ? { archived: true } : {},
										replace: true,
									})
								}
							/>
						</Group>
						{listLoading ? (
							<Paper withBorder p="xl" radius="md" ta="center">
								<Text size="sm" c="dimmed">
									{t("loading")}
								</Text>
							</Paper>
						) : active.isError ? (
							<Paper withBorder p="xl" radius="md" ta="center">
								<Text size="sm" c="red">
									{(active.error as Error).message}
								</Text>
							</Paper>
						) : rows.length === 0 ? (
							<Paper withBorder p="xl" radius="md" ta="center">
								<ThemeIcon variant="light" size="xl" radius="xl" color="gray" mb="xs">
									<IconNotebook size={24} />
								</ThemeIcon>
								<Text size="sm" fw={600} c="dimmed">
									{t("noPersonalEntries")}
								</Text>
							</Paper>
						) : (
							<Stack gap="xs">
								{rows.map((p) => (
									<PersonalEntryRow key={p.id} entry={p} openRequest={openByDraft.get(p.id)} />
								))}
							</Stack>
						)}
					</Stack>
				</Grid.Col>
				<Grid.Col span={{ base: 12, md: 5 }}>
					<MyRequestsSection
						open={sections.open}
						outcomes={sections.outcomes}
						entriesById={entriesById}
						loading={openSubs.isLoading || outcomes.isLoading}
					/>
				</Grid.Col>
			</Grid>
		</Container>
	);
}
