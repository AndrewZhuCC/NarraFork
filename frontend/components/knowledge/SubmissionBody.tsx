/**
 * The body of a publish request on the request page (§5.7).
 *
 * Linked: the proposal as a diff — against current main by default (what approving would
 * change now), or against the base it was forked from (what the author actually edited).
 * When the viewer cannot read current main, only the base comparison is offered.
 * Standalone: a full preview of the new entry, with the raw text one click away.
 */
import { Group, Paper, SegmentedControl, Stack, Text } from "@mantine/core";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { KnowledgeSubmissionDetail } from "../../lib/api";
import { DiffView } from "../narrator/diff/DiffView";
import { MarkdownContent } from "../narrator/markdown/MarkdownContent";

export type SubmissionBodyView = "main" | "base" | "preview";

/** Views offered for a request. Exported for tests. */
export function submissionBodyViews(
	sub: Pick<KnowledgeSubmissionDetail, "entryId" | "currentMainContent">,
): SubmissionBodyView[] {
	if (!sub.entryId) return ["preview"];
	return sub.currentMainContent !== null ? ["main", "base", "preview"] : ["base", "preview"];
}

export function SubmissionBody({ submission }: { submission: KnowledgeSubmissionDetail }) {
	const { t } = useTranslation("knowledge");
	const views = submissionBodyViews(submission);
	const [view, setView] = useState<SubmissionBodyView>(views[0]);
	const active = views.includes(view) ? view : views[0];

	return (
		<Paper withBorder p="md" radius="md">
			<Stack gap="sm">
				<Group justify="space-between" wrap="wrap">
					<Text size="sm" fw={600}>
						{t("requestBody")}
					</Text>
					{views.length > 1 ? (
						<SegmentedControl
							size="xs"
							value={active}
							onChange={(v) => setView(v as SubmissionBodyView)}
							data={views.map((v) => ({ value: v, label: t(`requestBodyView_${v}`) }))}
						/>
					) : null}
				</Group>
				{active === "preview" ? (
					submission.proposedContent ? (
						<MarkdownContent text={submission.proposedContent} />
					) : (
						<Text size="sm" c="dimmed">
							{t("noContent")}
						</Text>
					)
				) : (
					<DiffView
						oldStr={
							active === "main" ? (submission.currentMainContent ?? "") : submission.baseContent
						}
						newStr={submission.proposedContent}
						language="markdown"
						maxHeight={520}
						wordWrap
					/>
				)}
			</Stack>
		</Paper>
	);
}
