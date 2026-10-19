import { Badge, type BadgeProps } from "@mantine/core";
import { useTranslation } from "react-i18next";
import type { KnowledgeSubmissionStatus } from "../../lib/api";

/**
 * The single mapping from a publish-request status to its badge color. Every knowledge
 * surface (entry page, personal entry page, my library, review center, review panel) renders
 * status through {@link SubmissionStatusBadge}, so a status never shows in two colors.
 *
 * Open states get a hue (pending = waiting, conflict = needs a merge, changes requested =
 * back with the author); terminal verdicts are green / red; closures that are not a review
 * verdict (withdrawn, superseded) stay neutral.
 */
export const SUBMISSION_STATUS_COLOR: Record<KnowledgeSubmissionStatus, string> = {
	pending: "blue",
	conflict: "orange",
	changes_requested: "yellow",
	approved: "green",
	rejected: "red",
	withdrawn: "gray",
	superseded: "gray",
};

export function submissionStatusColor(status: KnowledgeSubmissionStatus): string {
	return SUBMISSION_STATUS_COLOR[status] ?? "gray";
}

export function SubmissionStatusBadge({
	status,
	size = "xs",
	style,
}: {
	status: KnowledgeSubmissionStatus;
	size?: BadgeProps["size"];
	style?: BadgeProps["style"];
}) {
	const { t } = useTranslation("knowledge");
	return (
		<Badge size={size} variant="light" color={submissionStatusColor(status)} style={style}>
			{t(`submissionStatus_${status}`)}
		</Badge>
	);
}
