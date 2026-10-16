import { Group, Text, Tooltip } from "@mantine/core";
import { useTranslation } from "react-i18next";
import type { GitLineStats } from "../../../shared/git-line-stats";
import type { ApiError } from "../../lib/api";

/** Keep the last measured totals while updating; unknown and failed are never zero. */
export function GitLineStatsSummary({
	data,
	isFetching,
	error,
	compact = false,
}: {
	data?: Omit<GitLineStats, "files">;
	isFetching: boolean;
	error: Error | null;
	compact?: boolean;
}) {
	const { t } = useTranslation("git");
	const message = error
		? t(
				(error as ApiError).data?.code === "GIT_LINE_STATS_UNSUPPORTED"
					? "lineStats.unsupported"
					: "lineStats.failed",
			)
		: isFetching || !data
			? t(data ? "lineStats.updating" : "lineStats.loading")
			: undefined;
	if (message && (!data || error))
		return (
			<Tooltip label={message}>
				<Text span size="xs" c="dimmed" role="status" aria-label={message}>
					{compact && !error ? "…" : message}
				</Text>
			</Tooltip>
		);
	if (!data) return null;
	return (
		<Group gap={5} wrap="nowrap">
			<Text span size="xs" c="green" fw={600}>
				+{data.linesAdded}
			</Text>
			<Text span size="xs" c="red" fw={600}>
				-{data.linesRemoved}
			</Text>
			{isFetching && (
				<Tooltip label={t("lineStats.updating")}>
					<Text span size="xs" c="dimmed" role="status" aria-label={t("lineStats.updating")}>
						{compact ? "…" : t("lineStats.updating")}
					</Text>
				</Tooltip>
			)}
			{!data.complete && (
				<Tooltip label={t("lineStats.partial")}>
					<Text span size="xs" c="dimmed" aria-label={t("lineStats.partial")}>
						{compact ? "≈" : t("lineStats.partial")}
					</Text>
				</Tooltip>
			)}
		</Group>
	);
}
