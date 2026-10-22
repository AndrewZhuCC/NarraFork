import type { BlockBackfillStatus, BlockJobState } from "@frontend/lib/api";
import { api } from "@frontend/lib/api";
import { Badge, Button, Group, Paper, Progress, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

const QUERY_KEY = ["storage", "block-backfill"] as const;

function percent(job: BlockJobState | null, maxRowid: number): number {
	if (!job) return 0;
	if (job.complete) return 100;
	return maxRowid > 0 ? Math.min(99, Math.floor((job.cursor / maxRowid) * 100)) : 0;
}

/**
 * Progress of the background jobs that move older messages to block storage, with a
 * pause control. Storage shape only: conversations read the same before and after.
 */
export function BlockBackfillCard() {
	const { t } = useTranslation("settings");
	const queryClient = useQueryClient();
	const { data } = useQuery({
		queryKey: QUERY_KEY,
		queryFn: () => api.getBlockBackfillStatus().then((response) => response.status),
		// Refresh while a job is moving; a finished or paused job stays put.
		refetchInterval: (query) => (query.state.data?.running ? 5_000 : false),
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
			</Stack>
		</Paper>
	);
}
