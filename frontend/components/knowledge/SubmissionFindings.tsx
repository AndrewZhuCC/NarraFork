/**
 * Reviewer findings recorded on a submission.
 *
 * Shown to the author (and anyone else who can already see the submission) on every
 * surface that lists it: the shared entry's Submissions tab, the review panel, and the
 * standalone personal entry. Findings used to render only on the personal-entry page,
 * which redirects away once the draft is linked to a global entry.
 */
import { Stack, Text } from "@mantine/core";
import { useTranslation } from "react-i18next";
import type { KnowledgeFinding } from "../../lib/api";

export function SubmissionFindings({
	findings,
}: {
	findings: KnowledgeFinding[] | null | undefined;
}) {
	const { t } = useTranslation("knowledge");
	if (!findings || findings.length === 0) return null;
	return (
		<Stack gap={2} mt={6}>
			{findings.map((f) => (
				<Text key={`${f.severity}-${f.location ?? ""}-${f.message}`} size="xs">
					[{t(`severity_${f.severity}`)}] {f.message}
					{f.location ? ` (${f.location})` : ""}
				</Text>
			))}
		</Stack>
	);
}
