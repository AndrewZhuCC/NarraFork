import { Badge, Button, Group, Loader, Stack, Text, ThemeIcon } from "@mantine/core";
import { IconAlertTriangle, IconClockPause, IconHandStop } from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { lazy, Suspense, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { loadedHumanAttentionItems, useHumanAttention } from "../../hooks/useHumanAttention";
import { api } from "../../lib/api";
import { formatSmartTime } from "../../lib/format";
import classes from "./DashboardSessions.module.css";
import {
	buildDashboardSessions,
	type DashboardNarrator,
	type DashboardSessionRow,
	directoryLabel,
} from "./dashboard-sessions";

const NotificationCenterDrawer = lazy(() =>
	import("../notifications/NotificationCenterDrawer").then((m) => ({
		default: m.NotificationCenterDrawer,
	})),
);

const SESSIONS_QUERY_GC_TIME_MS = 30_000;
const LIVE_REFETCH_INTERVAL_MS = 15_000;
const LIVE_LIST_LIMIT = 30;
const RECENT_LIST_LIMIT = 50;

function liveQuery(status: "working" | "waiting") {
	return {
		queryKey: ["narrators", "dashboard", status],
		queryFn: () =>
			api.listNarratorsPaginated({
				standalone: "all",
				status,
				limit: LIVE_LIST_LIMIT,
				sortBy: "updatedAt",
				sortOrder: "desc",
			}),
		gcTime: SESSIONS_QUERY_GC_TIME_MS,
		refetchInterval: LIVE_REFETCH_INTERVAL_MS,
	};
}

/** Dashboard session data: grouped rows plus recently used directories for the composer. */
export function useDashboardSessions() {
	const attention = useHumanAttention();
	const working = useQuery(liveQuery("working"));
	const waiting = useQuery(liveQuery("waiting"));
	const recent = useQuery({
		queryKey: ["narrators", "dashboard", "recent"],
		queryFn: () =>
			api.listNarratorsPaginated({
				standalone: "all",
				limit: RECENT_LIST_LIMIT,
				sortBy: "updatedAt",
				sortOrder: "desc",
			}),
		gcTime: SESSIONS_QUERY_GC_TIME_MS,
	});

	const attentionPages = attention.data?.pages;
	const attentionItems = useMemo(() => loadedHumanAttentionItems(attentionPages), [attentionPages]);
	const workingItems = working.data?.items as DashboardNarrator[] | undefined;
	const waitingItems = waiting.data?.items as DashboardNarrator[] | undefined;
	const recentItems = recent.data?.items as DashboardNarrator[] | undefined;

	const groups = useMemo(
		() =>
			buildDashboardSessions({
				attentionItems,
				working: workingItems ?? [],
				waiting: waitingItems ?? [],
				recent: recentItems ?? [],
			}),
		[attentionItems, workingItems, waitingItems, recentItems],
	);

	const recentDirectories = useMemo(() => {
		const seen = new Set<string>();
		for (const n of recentItems ?? []) if (n.cwd) seen.add(n.cwd);
		return [...seen];
	}, [recentItems]);

	return {
		groups,
		recentDirectories,
		attention,
		isLoading: working.isLoading || waiting.isLoading || recent.isLoading || attention.isLoading,
	};
}

export function DashboardSessions({ data }: { data: ReturnType<typeof useDashboardSessions> }) {
	const { t } = useTranslation("dashboard");
	const { t: tn } = useTranslation("narrator");
	const [inboxOpened, setInboxOpened] = useState(false);
	const { groups, attention, isLoading } = data;
	const empty =
		groups.attention.length === 0 && groups.running.length === 0 && groups.recent.length === 0;

	if (isLoading && empty) {
		return (
			<Group justify="center" py="xl">
				<Loader size="sm" type="dots" />
			</Group>
		);
	}

	return (
		<Stack gap="lg">
			{attention.isError && (
				<Group gap="xs" px="sm">
					<Text size="sm" c="red">
						{tn("humanAttentionLoadError")}
					</Text>
					<Button
						size="compact-xs"
						variant="subtle"
						onClick={() => void attention.refetch()}
						loading={attention.isFetching}
					>
						{tn("humanAttentionRetry")}
					</Button>
				</Group>
			)}

			{groups.attention.length > 0 && (
				<Section
					title={t("sections.needsYou")}
					count={groups.attention.length}
					more={attention.hasNextPage}
					action={
						<Button
							size="compact-xs"
							variant="subtle"
							color="gray"
							onClick={() => setInboxOpened(true)}
						>
							{t("openInbox")}
						</Button>
					}
				>
					<div className={classes.attentionGroup}>
						{groups.attention.map((row) => (
							<SessionRow key={row.narratorId} row={row} />
						))}
					</div>
				</Section>
			)}

			<Section title={t("sections.running")} count={groups.running.length}>
				{groups.running.length === 0 ? (
					<Text size="sm" c="dimmed" px="sm">
						{t("noActiveNarrators")}
					</Text>
				) : (
					groups.running.map((row) => <SessionRow key={row.narratorId} row={row} />)
				)}
			</Section>

			{groups.recent.length > 0 && (
				<Section
					title={t("sections.recent")}
					action={
						<Button
							size="compact-xs"
							variant="subtle"
							color="gray"
							component={Link}
							to="/narrators"
						>
							{t("viewAll")}
						</Button>
					}
				>
					{groups.recent.map((row) => (
						<SessionRow key={row.narratorId} row={row} />
					))}
				</Section>
			)}

			{inboxOpened && (
				<Suspense fallback={null}>
					<NotificationCenterDrawer
						opened
						onClose={() => setInboxOpened(false)}
						initialTab="attention"
					/>
				</Suspense>
			)}
		</Stack>
	);
}

function Section({
	title,
	count,
	more,
	action,
	children,
}: {
	title: string;
	count?: number;
	more?: boolean;
	action?: React.ReactNode;
	children: React.ReactNode;
}) {
	return (
		<Stack gap={4}>
			<Group justify="space-between" px="sm" mb={2}>
				<Group gap={6}>
					<Text size="xs" fw={600} c="dimmed" tt="uppercase" style={{ letterSpacing: 0.4 }}>
						{title}
					</Text>
					{count != null && count > 0 && (
						<Text size="xs" c="dimmed">
							{count}
							{more ? "+" : ""}
						</Text>
					)}
				</Group>
				{action}
			</Group>
			{children}
		</Stack>
	);
}

function RowIndicator({ row }: { row: DashboardSessionRow }) {
	if (row.reason === "decision") {
		return (
			<ThemeIcon variant="light" color={row.blocking ? "yellow" : "blue"} radius="xl" size={26}>
				<IconHandStop size={15} />
			</ThemeIcon>
		);
	}
	if (row.reason === "waiting") {
		return (
			<ThemeIcon variant="light" color="yellow" radius="xl" size={26}>
				<IconClockPause size={15} />
			</ThemeIcon>
		);
	}
	if (row.reason === "error") {
		return (
			<ThemeIcon variant="light" color="red" radius="xl" size={26}>
				<IconAlertTriangle size={15} />
			</ThemeIcon>
		);
	}
	if (row.status === "working") return <Loader size={14} color="blue" />;
	return <span className={classes.dot} style={{ background: "var(--mantine-color-gray-6)" }} />;
}

function SessionRow({ row }: { row: DashboardSessionRow }) {
	const { t } = useTranslation("dashboard");
	const subtitle =
		row.reason === "waiting"
			? t("reason.waiting")
			: row.reason === "decision" && !row.detail
				? t(row.blocking ? "reason.blockingDecision" : "reason.decision")
				: row.detail;

	return (
		<Link
			to="/narrators/$narratorId"
			params={{ narratorId: row.narratorId }}
			className={classes.row}
		>
			<span className={classes.indicator}>
				<RowIndicator row={row} />
			</span>
			<Stack gap={0} style={{ flex: 1, minWidth: 0 }}>
				<Group gap={6} wrap="nowrap">
					<Text size="sm" fw={500} truncate>
						{row.title}
					</Text>
					{row.decisionCount != null && row.decisionCount > 1 && (
						<Badge size="xs" variant="light" color={row.blocking ? "yellow" : "blue"} circle>
							{row.decisionCount}
						</Badge>
					)}
				</Group>
				{(subtitle || row.cwd) && (
					<Text size="xs" c={row.reason === "error" ? "red" : "dimmed"} truncate>
						{subtitle ?? (row.cwd ? directoryLabel(row.cwd) : "")}
					</Text>
				)}
			</Stack>
			<Stack gap={0} align="flex-end" style={{ flexShrink: 0 }}>
				{row.time && (
					<Text size="xs" c="dimmed">
						{formatSmartTime(row.time)}
					</Text>
				)}
				{subtitle && row.cwd && (
					<Text size="xs" c="dimmed" visibleFrom="sm" maw={160} truncate title={row.cwd}>
						{directoryLabel(row.cwd)}
					</Text>
				)}
			</Stack>
		</Link>
	);
}
