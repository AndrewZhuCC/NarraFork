/**
 * Knowledge review rows inside the notification bell.
 *
 * These are not `notifications` table rows. Review events stay out of that table so a
 * classified entry title is never stored there. Reviewers see the ACL-checked inbox;
 * authors see their own recent verdicts (`/my-submission-outcomes`, ids and findings
 * only). A `knowledge:review_inbox_changed` push invalidates both queries.
 */
import { Stack, Text, UnstyledButton } from "@mantine/core";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import {
	useKnowledgeSubmissions,
	useMyKnowledgeSubmissionOutcomes,
	useReviewInboxCount,
} from "../../hooks/useKnowledge";
import type { KnowledgeSubmission, KnowledgeSubmissionOutcome } from "../../lib/api";
import { formatLocaleDateTime } from "../../lib/intl-format";
import { knowledgeReviewDestination } from "../../lib/knowledge-review-nav";

const REVIEWER_ROW_CAP = 5;

export function KnowledgeReviewNotices({ onOpen }: { onOpen: () => void }) {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	const inbox = useReviewInboxCount();
	const outcomes = useMyKnowledgeSubmissionOutcomes();
	const pending = useKnowledgeSubmissions({ status: "pending" });
	const conflicts = useKnowledgeSubmissions({ status: "conflict" });

	const reviewerRows = [...(pending.data ?? []), ...(conflicts.data ?? [])].slice(
		0,
		REVIEWER_ROW_CAP,
	);
	const authorRows = outcomes.data ?? [];
	const inboxCount = inbox.data?.count ?? 0;
	if (inboxCount === 0 && reviewerRows.length === 0 && authorRows.length === 0) return null;

	const openReviewer = (row: KnowledgeSubmission) => {
		const dest = knowledgeReviewDestination({
			audience: "reviewer",
			entryId: row.entryId,
			draftId: row.draftId,
			submissionId: row.id,
		});
		if (dest.to === "entry") {
			void navigate({
				to: "/knowledge/$entryId",
				params: { entryId: dest.entryId },
				hash: dest.hash,
			});
		} else if (dest.to === "review-center") {
			void navigate({
				to: "/knowledge",
				search: { tab: "review", submission: dest.submissionId },
			});
		}
		onOpen();
	};

	const openAuthor = (row: KnowledgeSubmissionOutcome) => {
		const dest = knowledgeReviewDestination({
			audience: "author",
			entryId: row.entryId,
			draftId: row.draftId,
			submissionId: row.id,
		});
		if (dest.to === "entry") {
			void navigate({
				to: "/knowledge/$entryId",
				params: { entryId: dest.entryId },
				hash: dest.hash,
			});
		} else if (dest.to === "personal") {
			void navigate({
				to: "/knowledge/personal/$personalEntryId",
				params: { personalEntryId: dest.personalEntryId },
			});
		} else {
			void navigate({
				to: "/knowledge",
				search: { tab: "review", submission: dest.submissionId },
			});
		}
		onOpen();
	};

	const inboxLabel = inbox.data?.capped
		? t("reviewNoticeInboxCapped", { count: inboxCount })
		: t("reviewNoticeInbox", { count: inboxCount });

	return (
		<Stack gap={4} data-testid="knowledge-review-notices">
			<Text size="xs" fw={600} c="dimmed">
				{t("reviewNoticeSection")}
			</Text>
			{inboxCount > 0 ? (
				<UnstyledButton
					data-testid="knowledge-review-inbox"
					onClick={() => {
						void navigate({ to: "/knowledge", search: { tab: "review" } });
						onOpen();
					}}
					style={{ width: "100%", textAlign: "left", padding: "8px 12px", borderRadius: 8 }}
				>
					<Text size="sm" fw={600}>
						{inboxLabel}
					</Text>
					<Text size="xs" c="dimmed">
						{t("reviewNoticeOpenCenter")}
					</Text>
				</UnstyledButton>
			) : null}
			{reviewerRows.map((row) => (
				<UnstyledButton
					key={row.id}
					data-testid="knowledge-review-notice"
					data-submission-id={row.id}
					onClick={() => openReviewer(row)}
					style={{ width: "100%", textAlign: "left", padding: "8px 12px", borderRadius: 8 }}
				>
					<Text size="sm" fw={600} lineClamp={1}>
						{t("reviewNoticeAwaiting")}
					</Text>
					<Text size="xs" c="dimmed">
						{t(`submissionStatus_${row.status}`)} · {formatLocaleDateTime(row.createdAt)}
					</Text>
				</UnstyledButton>
			))}
			{authorRows.map((row) => {
				const finding = row.findingsJson?.[0];
				return (
					<UnstyledButton
						key={row.id}
						data-testid="knowledge-review-outcome"
						data-submission-id={row.id}
						onClick={() => openAuthor(row)}
						style={{ width: "100%", textAlign: "left", padding: "8px 12px", borderRadius: 8 }}
					>
						<Text size="sm" fw={600} lineClamp={1}>
							{t(`reviewNoticeOutcome_${row.status}`)}
						</Text>
						{finding ? (
							<Text size="xs" c="dimmed" lineClamp={2}>
								[{t(`severity_${finding.severity}`)}] {finding.message}
							</Text>
						) : (
							<Text size="xs" c="dimmed">
								{formatLocaleDateTime(row.reviewedAt ?? row.createdAt)}
							</Text>
						)}
					</UnstyledButton>
				);
			})}
		</Stack>
	);
}
