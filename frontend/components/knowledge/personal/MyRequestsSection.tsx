/**
 * "My requests" on `/knowledge/mine` (§5.4): the caller's open publish requests first, recent
 * review outcomes (approved / rejected / changes requested, with findings) below. Every row
 * opens the request page. The request endpoints carry no titles (they are id-only by design),
 * so rows are labelled from the caller's own personal-version list.
 */
import { Card, Divider, Group, Paper, Stack, Text } from "@mantine/core";
import { useNavigate } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import type { KnowledgePersonalEntrySummary } from "../../../lib/api";
import { formatLocaleDateTime } from "../../../lib/intl-format";
import { knowledgeRequestDestination } from "../../../lib/knowledge-review-nav";
import cardClasses from "../KnowledgeCards.module.css";
import { SubmissionFindings } from "../SubmissionFindings";
import { SubmissionStatusBadge } from "../SubmissionStatusBadge";
import { PersonalEntryKindLabel } from "./PersonalEntryRow";
import { type MyRequestRow, personalEntryDisplayTitle } from "./personal-entry-view";

export function MyRequestsSection({
	open,
	outcomes,
	entriesById,
	loading,
}: {
	open: MyRequestRow[];
	outcomes: MyRequestRow[];
	entriesById: Map<string, KnowledgePersonalEntrySummary>;
	loading: boolean;
}) {
	const { t } = useTranslation("knowledge");
	return (
		<Paper withBorder p="md" radius="md">
			<Stack gap="sm">
				<Text size="sm" fw={600}>
					{t("mineRequestsTitle")}
				</Text>
				<Divider />
				<Text size="xs" fw={700} c="dimmed" tt="uppercase">
					{t("mineRequestsOpen")}
				</Text>
				<RequestRows
					rows={open}
					entriesById={entriesById}
					empty={loading ? t("loading") : t("mineRequestsEmpty")}
				/>
				<Text size="xs" fw={700} c="dimmed" tt="uppercase" mt="xs">
					{t("mineRequestsOutcomes")}
				</Text>
				<RequestRows
					rows={outcomes}
					entriesById={entriesById}
					empty={loading ? t("loading") : t("mineOutcomesEmpty")}
				/>
			</Stack>
		</Paper>
	);
}

function RequestRows({
	rows,
	entriesById,
	empty,
}: {
	rows: MyRequestRow[];
	entriesById: Map<string, KnowledgePersonalEntrySummary>;
	empty: string;
}) {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	if (rows.length === 0) {
		return (
			<Text size="sm" c="dimmed">
				{empty}
			</Text>
		);
	}
	return (
		<Stack gap="xs">
			{rows.map((r) => {
				const entry = entriesById.get(r.draftId);
				const title =
					(entry ? personalEntryDisplayTitle(entry) : null) ??
					t("mineRequestUnknownVersion", { id: r.draftId.slice(0, 8) });
				return (
					<Card
						key={r.id}
						withBorder
						padding="xs"
						radius="sm"
						className={cardClasses.reviewCard}
						data-testid="mine-request"
						onClick={() => navigate(knowledgeRequestDestination(r.id))}
					>
						<Group justify="space-between" wrap="nowrap" align="center">
							<div style={{ minWidth: 0, flex: 1 }}>
								<Text size="sm" truncate="end">
									{title}
								</Text>
								{entry ? <PersonalEntryKindLabel entry={entry} /> : null}
							</div>
							<Group gap={6} wrap="nowrap" style={{ flexShrink: 0 }}>
								<Text size="xs" c="dimmed" visibleFrom="sm">
									{formatLocaleDateTime(r.at)}
								</Text>
								<SubmissionStatusBadge status={r.status} />
							</Group>
						</Group>
						<SubmissionFindings findings={r.findings} />
					</Card>
				);
			})}
		</Stack>
	);
}
