/**
 * Review inbox (docs/KNOWLEDGE_UI_REDESIGN.md §5.6): the publish requests the caller may review.
 *
 * One row per request, linked and standalone alike; every row opens the request page
 * (`/knowledge/requests/$submissionId`). Filters live in search params: `status` (open =
 * pending + conflict by default, pending, conflict, all), `collection`, and `entry` (set by the
 * legacy `#submissions` redirect). Rows come from the reviewer list, so the caller's own
 * requests are not here — authors reach theirs from "My library" or the bell.
 */
import {
	Anchor,
	Badge,
	Button,
	Card,
	CloseButton,
	Container,
	Group,
	Paper,
	SegmentedControl,
	Select,
	Stack,
	Text,
	ThemeIcon,
	Title,
	Tooltip,
} from "@mantine/core";
import {
	IconArrowLeft,
	IconFileText,
	IconFolder,
	IconGitMerge,
	IconGitPullRequest,
} from "@tabler/icons-react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import cardClasses from "../../../components/knowledge/KnowledgeCards.module.css";
import { ReviewScopeNotice } from "../../../components/knowledge/ReviewScopeNotice";
import { SubmissionStatusBadge } from "../../../components/knowledge/SubmissionStatusBadge";
import { useKnowledgeCollections, useKnowledgeSubmissions } from "../../../hooks/useKnowledge";
import type { KnowledgeSubmissionListItem } from "../../../lib/api";
import { formatLocaleDateTime } from "../../../lib/intl-format";
import {
	type KnowledgeReviewStatusFilter,
	knowledgeRequestDestination,
	parseKnowledgeReviewSearch,
} from "../../../lib/knowledge-review-nav";

export const Route = createFileRoute("/knowledge/review/")({
	component: ReviewInboxPage,
	validateSearch: parseKnowledgeReviewSearch,
});

/** Rows for the active status filter; `open` merges the pending and conflict lists. */
function useInboxRows(status: KnowledgeReviewStatusFilter, entryId: string | undefined) {
	const open = status === "open";
	const pending = useKnowledgeSubmissions({
		entryId,
		status: open ? "pending" : status === "all" ? undefined : status,
	});
	const conflicts = useKnowledgeSubmissions({ entryId, status: "conflict" }, { enabled: open });
	const rows = useMemo(() => {
		const list = [...(pending.data ?? []), ...(open ? (conflicts.data ?? []) : [])];
		return list.sort((a, b) => b.createdAt.localeCompare(a.createdAt));
	}, [pending.data, conflicts.data, open]);
	return { rows, isLoading: pending.isLoading || (open && conflicts.isLoading) };
}

function ReviewInboxPage() {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate({ from: "/knowledge/review/" });
	const search = Route.useSearch();
	const status = search.status ?? "open";
	const collections = useKnowledgeCollections();
	const { rows, isLoading } = useInboxRows(status, search.entry);
	// Collection filter is applied to the (bounded) reviewer list on the client: linked rows only
	// know their collection through the entry, which the server resolves per row.
	const visible = useMemo(
		() =>
			search.collection ? rows.filter((r) => r.targetCollectionId === search.collection) : rows,
		[rows, search.collection],
	);
	const collectionOptions = useMemo(
		() => (collections.data ?? []).map((c) => ({ value: c.id, label: c.name })),
		[collections.data],
	);
	const setSearch = (patch: Partial<typeof search>) =>
		navigate({
			search: (prev) => parseKnowledgeReviewSearch({ ...prev, ...patch }),
			replace: true,
		});

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
			<Stack gap="md">
				<div>
					<Title order={2}>{t("reviewInboxTitle")}</Title>
					<Text size="sm" c="dimmed">
						{t("reviewInboxDesc")}
					</Text>
				</div>
				<ReviewScopeNotice />
				<Paper withBorder p="sm" radius="md">
					<Group justify="space-between" gap="sm" wrap="wrap">
						<SegmentedControl
							size="xs"
							value={status}
							onChange={(v) => setSearch({ status: v as KnowledgeReviewStatusFilter })}
							data={[
								{ value: "open", label: t("reviewInboxFilter_open") },
								{ value: "pending", label: t("submissionStatus_pending") },
								{ value: "conflict", label: t("submissionStatus_conflict") },
								{ value: "all", label: t("reviewCenterAll") },
							]}
						/>
						<Group gap="xs" wrap="wrap">
							{search.entry ? (
								<Badge
									variant="light"
									rightSection={
										<CloseButton
											size="xs"
											aria-label={t("reviewInboxClearEntry")}
											onClick={() => setSearch({ entry: undefined })}
										/>
									}
								>
									{t("reviewInboxEntryFilter", {
										title: rows[0]?.entryTitle ?? search.entry.slice(0, 8),
									})}
								</Badge>
							) : null}
							<Select
								size="xs"
								w={220}
								clearable
								placeholder={t("reviewInboxAllCollections")}
								data={collectionOptions}
								value={search.collection ?? null}
								onChange={(v) => setSearch({ collection: v ?? undefined })}
							/>
						</Group>
					</Group>
				</Paper>

				{isLoading ? (
					<Text size="sm" c="dimmed" ta="center" py="xl">
						{t("loading")}
					</Text>
				) : visible.length === 0 ? (
					<Paper withBorder p="xl" radius="md" ta="center">
						<ThemeIcon variant="light" size="xl" radius="xl" color="gray" mb="xs">
							<IconGitPullRequest size={24} />
						</ThemeIcon>
						<Text size="sm" fw={600} c="dimmed">
							{status === "open" ? t("reviewInboxEmpty") : t("noSubmissions")}
						</Text>
						{search.entry ? (
							<Anchor
								component="button"
								type="button"
								size="sm"
								onClick={() =>
									search.entry &&
									navigate({ to: "/knowledge/$entryId", params: { entryId: search.entry } })
								}
							>
								{t("reviewInboxBackToEntry")}
							</Anchor>
						) : null}
					</Paper>
				) : (
					<Stack gap="xs">
						{visible.map((row) => (
							<InboxRow key={row.id} row={row} />
						))}
					</Stack>
				)}
			</Stack>
		</Container>
	);
}

function InboxRow({ row }: { row: KnowledgeSubmissionListItem }) {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	const linked = !!row.entryId;
	const title = linked
		? (row.entryTitle ?? t("requestUnreadableEntry"))
		: row.title || t("reviewCenterUntitledEntry");
	return (
		<Card
			withBorder
			padding="sm"
			radius="md"
			className={cardClasses.reviewCard}
			data-testid="knowledge-review-inbox-row"
			onClick={() => navigate(knowledgeRequestDestination(row.id))}
		>
			<Group justify="space-between" wrap="nowrap" align="center" gap="sm">
				<div style={{ flex: 1, minWidth: 0 }}>
					<Group gap="xs" wrap="nowrap" mb={2}>
						{linked ? (
							<IconFileText
								size={16}
								style={{ color: "var(--mantine-color-blue-5)", flexShrink: 0 }}
							/>
						) : (
							<IconFolder
								size={16}
								style={{ color: "var(--mantine-color-teal-5)", flexShrink: 0 }}
							/>
						)}
						<Text size="sm" fw={600} truncate="end">
							{title}
						</Text>
					</Group>
					<Text size="xs" c="dimmed" truncate="end">
						{[
							row.submitterUsername ?? t("requestUnknownUser"),
							formatLocaleDateTime(row.createdAt),
							row.collectionName,
							row.changeNote,
						]
							.filter(Boolean)
							.join(" · ")}
					</Text>
				</div>
				<Group gap={6} wrap="nowrap" style={{ flexShrink: 0 }}>
					{row.drifted ? (
						<Tooltip label={t("requestDriftTooltip")} withArrow>
							<Badge
								size="xs"
								variant="light"
								color="orange"
								leftSection={<IconGitMerge size={10} />}
							>
								{t("driftBadge")}
							</Badge>
						</Tooltip>
					) : null}
					{(row.round ?? 1) > 1 ? (
						<Badge size="xs" variant="outline" color="grape">
							{t("subFlowRound", { round: row.round })}
						</Badge>
					) : null}
					<Badge size="xs" variant="light" color={linked ? "blue" : "teal"}>
						{linked ? t("requestKindLinked") : t("reviewCenterNewEntry")}
					</Badge>
					<SubmissionStatusBadge status={row.status} />
				</Group>
			</Group>
		</Card>
	);
}
