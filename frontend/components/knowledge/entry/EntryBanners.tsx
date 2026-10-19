/**
 * Read-page notices (§5.2):
 *  - "N open requests to review" → the review inbox filtered to this entry, for viewers with
 *    review authority on it (`viewerCaps.canReview`; the reviewer list never includes their
 *    own requests);
 *  - "your request is <status>" → the request page, when the caller's personal version of this
 *    entry has an open request (pending / conflict / changes requested).
 * Both queries run only when their banner can apply.
 */
import { Alert, Group, Text } from "@mantine/core";
import { IconGitPullRequest } from "@tabler/icons-react";
import { useTranslation } from "react-i18next";
import { useKnowledgeSubmissions, usePersonalEntry } from "../../../hooks/useKnowledge";
import type { KnowledgeEntryViewerCaps } from "../../../lib/api";
import { knowledgeRequestDestination } from "../../../lib/knowledge-review-nav";
import { AnchorLink } from "../router-links";
import { SubmissionStatusBadge } from "../SubmissionStatusBadge";

export function EntryBanners({
	entryId,
	caps,
}: {
	entryId: string;
	caps: KnowledgeEntryViewerCaps;
}) {
	const { t } = useTranslation("knowledge");
	// Same "open" set as the inbox default and the nav badge: pending + conflict.
	const pending = useKnowledgeSubmissions(
		{ entryId, status: "pending" },
		{ enabled: caps.canReview },
	);
	const conflict = useKnowledgeSubmissions(
		{ entryId, status: "conflict" },
		{ enabled: caps.canReview },
	);
	const mine = usePersonalEntry(caps.myPersonalEntryId ?? undefined);

	const reviewCount = caps.canReview
		? (pending.data?.length ?? 0) + (conflict.data?.length ?? 0)
		: 0;
	const myOpen = caps.myPersonalEntryId ? mine.data?.openSubmissions?.[0] : undefined;

	if (reviewCount === 0 && !myOpen) return null;
	return (
		<>
			{reviewCount > 0 ? (
				<Alert color="indigo" variant="light" p="xs" icon={<IconGitPullRequest size={16} />}>
					<Group gap="xs" justify="space-between" wrap="wrap">
						<Text size="sm">{t("entryReviewBanner", { count: reviewCount })}</Text>
						<AnchorLink to="/knowledge/review" search={{ entry: entryId }} size="sm">
							{t("entryReviewBannerOpen")}
						</AnchorLink>
					</Group>
				</Alert>
			) : null}
			{myOpen ? (
				<Alert color="grape" variant="light" p="xs" icon={<IconGitPullRequest size={16} />}>
					<Group gap="xs" justify="space-between" wrap="wrap">
						<Group gap="xs">
							<Text size="sm">{t("entryMyRequestBanner")}</Text>
							<SubmissionStatusBadge status={myOpen.status} />
						</Group>
						<AnchorLink {...knowledgeRequestDestination(myOpen.id)} size="sm">
							{t("submittedOpenRequest")}
						</AnchorLink>
					</Group>
				</Alert>
			) : null}
		</>
	);
}
