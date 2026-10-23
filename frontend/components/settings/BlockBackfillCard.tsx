import type { BlockBackfillStatus, BlockJobState, BlockProjectionBlock } from "@frontend/lib/api";
import { api } from "@frontend/lib/api";
import { Badge, Button, Group, Paper, Progress, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

const QUERY_KEY = ["storage", "block-backfill"] as const;

/** The hint for what a projection job is missing (`unavailable` hides the section). */
const PROJECTION_HINTS = {
	compact_json_off: "storageBlocksNeedsCompactJson",
	compact_json_on: "storageBlocksNeedsCompactJsonOff",
	backfill_incomplete: "storageBlocksNeedsBackfill",
} as const satisfies Record<Exclude<BlockProjectionBlock, "unavailable">, string>;

type ProjectionAction = "clear" | "restore" | "stop";

function percent(job: BlockJobState | null, maxRowid: number): number {
	if (!job) return 0;
	if (job.complete) return 100;
	return maxRowid > 0 ? Math.min(99, Math.floor((job.cursor / maxRowid) * 100)) : 0;
}

/**
 * Progress of the background jobs that move older messages to block storage, with a
 * pause control, and the admin-started jobs that remove or restore messages' duplicate
 * `content_json` copies. Storage shape only: conversations read the same before and after.
 */
export function BlockBackfillCard() {
	const { t } = useTranslation("settings");
	const queryClient = useQueryClient();
	const { data } = useQuery({
		queryKey: QUERY_KEY,
		queryFn: () => api.getBlockBackfillStatus().then((response) => response.status),
		// Refresh while a job is moving; a finished or paused job stays put.
		refetchInterval: (query) =>
			query.state.data?.running || query.state.data?.projectionRunning ? 5_000 : false,
	});
	const projection = useMutation({
		mutationFn: (action: ProjectionAction) =>
			api.runBlockProjectionAction(action).then((response) => response.status),
		onSuccess: (status) => queryClient.setQueryData<BlockBackfillStatus>(QUERY_KEY, status),
		onError: (error) =>
			notifications.show({
				color: "red",
				message: error instanceof Error ? error.message : String(error),
			}),
	});
	const pause = useMutation({
		mutationFn: (paused: boolean) =>
			api.setBlockBackfillPaused(paused).then((response) => response.status),
		onSuccess: (status) => queryClient.setQueryData<BlockBackfillStatus>(QUERY_KEY, status),
		onError: (error) =>
			notifications.show({
				color: "red",
				message: error instanceof Error ? error.message : String(error),
			}),
	});

	if (!data?.available) return null;
	const paused = !!data.convert?.paused;
	const done = !!data.convert?.complete && !!data.verify?.complete;
	const jobs = [
		{ key: "convert", label: t("storageBlocksConvert"), job: data.convert },
		{ key: "verify", label: t("storageBlocksVerify"), job: data.verify },
	];
	// A projection job is listed once started; one runs at a time.
	const projectionJobs = [
		{ key: "clear", label: t("storageBlocksClearJob"), job: data.clear },
		{ key: "restore", label: t("storageBlocksRestoreJob"), job: data.restore },
	].filter(({ job }) => job && (job.complete || job.processed > 0 || !job.paused));
	const projectionActive = projectionJobs.some(({ job }) => job && !job.complete && !job.paused);
	const hints = [
		!data.rowsOnly || !data.blocked.restore ? data.blocked.clear : null,
		data.rowsOnly ? data.blocked.restore : null,
	].flatMap((block) => (block && block !== "unavailable" ? [PROJECTION_HINTS[block]] : []));

	return (
		<Paper p="md" radius="sm" withBorder data-storage-blocks>
			<Stack gap="sm">
				<Group justify="space-between" wrap="nowrap">
					<div>
						<Text fw={600}>{t("storageBlocksTitle")}</Text>
						<Text size="sm" c="dimmed">
							{t("storageBlocksDescription")}
						</Text>
					</div>
					{!done && (
						<Button
							variant="default"
							size="xs"
							loading={pause.isPending}
							onClick={() => pause.mutate(!paused)}
						>
							{paused ? t("storageBlocksResume") : t("storageBlocksPause")}
						</Button>
					)}
				</Group>
				{jobs.map(({ key, label, job }) => (
					<Stack key={key} gap={4}>
						<Group justify="space-between" wrap="nowrap">
							<Text size="sm">{label}</Text>
							<Group gap="xs" wrap="nowrap">
								<Text size="xs" c="dimmed">
									{t("storageBlocksProcessed", { count: job?.processed ?? 0 })}
								</Text>
								<Badge
									size="sm"
									variant="light"
									color={job?.complete ? "green" : paused ? "gray" : "blue"}
								>
									{job?.complete
										? t("storageBlocksComplete")
										: paused
											? t("storageBlocksPaused")
											: data.running
												? t("storageBlocksRunning")
												: t("storageBlocksWaiting")}
								</Badge>
							</Group>
						</Group>
						<Progress value={percent(job, data.maxRowid)} size="sm" />
					</Stack>
				))}
				{data.diagnostics > 0 && (
					<Text size="sm" c="orange">
						{t("storageBlocksDiagnostics", { count: data.diagnostics })}
					</Text>
				)}
				{data.blocked.clear !== "unavailable" && (
					<Stack gap="xs" pt="sm" data-storage-blocks-projection>
						<div>
							<Text fw={600} size="sm">
								{t("storageBlocksProjectionTitle")}
							</Text>
							<Text size="sm" c="dimmed">
								{t("storageBlocksProjectionDescription")}
							</Text>
						</div>
						<Group gap="xs">
							{projectionActive ? (
								<Button
									variant="default"
									size="xs"
									loading={projection.isPending}
									onClick={() => projection.mutate("stop")}
								>
									{t("storageBlocksStop")}
								</Button>
							) : (
								<>
									<Button
										variant="default"
										size="xs"
										disabled={!!data.blocked.clear}
										loading={projection.isPending && projection.variables === "clear"}
										onClick={() => projection.mutate("clear")}
									>
										{t("storageBlocksClear")}
									</Button>
									<Button
										variant="default"
										size="xs"
										disabled={!data.rowsOnly || !!data.blocked.restore}
										loading={projection.isPending && projection.variables === "restore"}
										onClick={() => projection.mutate("restore")}
									>
										{t("storageBlocksRestore")}
									</Button>
								</>
							)}
						</Group>
						{projectionJobs.map(({ key, label, job }) => (
							<Stack key={key} gap={4}>
								<Group justify="space-between" wrap="nowrap">
									<Text size="sm">{label}</Text>
									<Group gap="xs" wrap="nowrap">
										<Text size="xs" c="dimmed">
											{t("storageBlocksProcessed", { count: job?.processed ?? 0 })}
										</Text>
										<Badge
											size="sm"
											variant="light"
											color={job?.complete ? "green" : job?.paused ? "gray" : "blue"}
										>
											{job?.complete
												? t("storageBlocksComplete")
												: job?.paused
													? t("storageBlocksPaused")
													: data.projectionRunning
														? t("storageBlocksRunning")
														: t("storageBlocksWaiting")}
										</Badge>
									</Group>
								</Group>
								<Progress value={percent(job, data.maxRowid)} size="sm" />
							</Stack>
						))}
						{hints.map((hint) => (
							<Text key={hint} size="sm" c="dimmed">
								{t(hint)}
							</Text>
						))}
						{data.rowsOnly && (
							<Text size="sm" c="dimmed">
								{t("storageBlocksRowsOnly")}
							</Text>
						)}
					</Stack>
				)}
			</Stack>
		</Paper>
	);
}
