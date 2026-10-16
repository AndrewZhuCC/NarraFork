/**
 * The publish requests of ONE personal version, as a compact list for its author.
 *
 * Shown under the linked draft editor and on the standalone personal-entry page. Each row is a
 * link to the request page (`/knowledge/requests/$submissionId`), where findings, the review
 * history and the author's withdraw / re-submit actions live — a request has exactly one page,
 * so no actions are duplicated here.
 */
import { Badge, Card, Divider, Group, Paper, Stack, Text } from "@mantine/core";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { usePersonalEntrySubmissions } from "../../hooks/useKnowledge";
import { formatLocaleDateTime } from "../../lib/intl-format";
import { knowledgeRequestDestination } from "../../lib/knowledge-review-nav";
import cardClasses from "./KnowledgeCards.module.css";
import { SubmissionFindings } from "./SubmissionFindings";
import { SubmissionStatusBadge } from "./SubmissionStatusBadge";

export function PersonalEntryRequests({ personalEntryId }: { personalEntryId: string }) {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	const submissions = usePersonalEntrySubmissions(personalEntryId);
	const rows = submissions.data ?? [];

	return (
		<Paper withBorder p="md" radius="md">
			<Stack gap="sm">
				<Text size="sm" fw={600}>
					{t("myRequestsTitle")}
				</Text>
				<Divider />
				{submissions.isLoading ? (
					<Text size="sm" c="dimmed">
						{t("loading")}
					</Text>
				) : rows.length === 0 ? (
					<Text size="sm" c="dimmed">
						{t("noSubmissions")}
					</Text>
				) : (
					<Stack gap="xs">
						{rows.map((s) => (
							<Card
								key={s.id}
								withBorder
								padding="xs"
								radius="sm"
								className={cardClasses.reviewCard}
								data-testid="personal-entry-request"
								onClick={() => navigate(knowledgeRequestDestination(s.id))}
							>
								<Group justify="space-between" wrap="nowrap" align="center">
									<div style={{ minWidth: 0, flex: 1 }}>
										<Text size="xs" truncate="end">
											{s.changeNote || t("subFlowRound", { round: s.round ?? 1 })}
										</Text>
										<Text size="xs" c="dimmed">
											{formatLocaleDateTime(s.createdAt)}
										</Text>
									</div>
									<Group gap={4} wrap="nowrap" style={{ flexShrink: 0 }}>
										{(s.round ?? 1) > 1 ? (
											<Badge size="xs" variant="outline" color="grape">
												{t("subFlowRound", { round: s.round })}
											</Badge>
										) : null}
										<SubmissionStatusBadge status={s.status} />
									</Group>
								</Group>
								<SubmissionFindings findings={s.findingsJson} />
							</Card>
						))}
					</Stack>
				)}
			</Stack>
		</Paper>
	);
}
