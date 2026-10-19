/**
 * Review history of one proposal on the request page: every round (re-submits of the same
 * personal version to the same target), newest first, each with its verdict, reviewer and
 * findings. The current request is highlighted; the others link to their own pages.
 */
import { Anchor, Badge, Group, Paper, Stack, Text, Timeline } from "@mantine/core";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import type { KnowledgeSubmissionHistoryItem } from "../../lib/api";
import { formatLocaleDateTime } from "../../lib/intl-format";
import { knowledgeRequestDestination } from "../../lib/knowledge-review-nav";
import { SubmissionFindings } from "./SubmissionFindings";
import { SubmissionStatusBadge } from "./SubmissionStatusBadge";

export function SubmissionTimeline({
	currentId,
	history,
}: {
	currentId: string;
	history: KnowledgeSubmissionHistoryItem[];
}) {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	if (history.length === 0) return null;
	const activeIndex = Math.max(
		0,
		history.findIndex((h) => h.id === currentId),
	);
	return (
		<Paper withBorder p="md" radius="md">
			<Stack gap="sm">
				<Text size="sm" fw={600}>
					{t("requestHistory")}
				</Text>
				<Timeline active={activeIndex} bulletSize={14} lineWidth={2}>
					{history.map((h) => (
						<Timeline.Item
							key={h.id}
							title={
								<Group gap={6} wrap="wrap">
									{h.id === currentId ? (
										<Text size="sm" fw={600}>
											{t("subFlowRound", { round: h.round ?? 1 })}
										</Text>
									) : (
										<Anchor
											component="button"
											type="button"
											size="sm"
											onClick={() => navigate(knowledgeRequestDestination(h.id))}
										>
											{t("subFlowRound", { round: h.round ?? 1 })}
										</Anchor>
									)}
									<SubmissionStatusBadge status={h.status} />
									{h.id === currentId ? (
										<Badge size="xs" variant="outline">
											{t("requestHistoryCurrent")}
										</Badge>
									) : null}
								</Group>
							}
						>
							<Text size="xs" c="dimmed">
								{t("requestSubmittedAt", { time: formatLocaleDateTime(h.createdAt) })}
							</Text>
							{h.changeNote ? (
								<Text size="xs" style={{ whiteSpace: "pre-wrap" }}>
									{h.changeNote}
								</Text>
							) : null}
							{h.reviewedAt ? (
								<Text size="xs" c="dimmed">
									{t("requestReviewedBy", {
										name: h.reviewerUsername ?? t("requestUnknownUser"),
										time: formatLocaleDateTime(h.reviewedAt),
									})}
								</Text>
							) : null}
							<SubmissionFindings findings={h.findingsJson} />
							{h.status === "superseded" ? (
								<Text size="xs" c="dimmed">
									{t("subFlowSupersededHint")}
								</Text>
							) : null}
						</Timeline.Item>
					))}
				</Timeline>
			</Stack>
		</Paper>
	);
}
