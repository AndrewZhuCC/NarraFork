/**
 * Revision list + any-two diff of one entry (`/knowledge/$entryId/history`). The list is
 * metadata only; the two sides of a diff are fetched on demand and cached per revision id.
 */
import { Badge, Group, Paper, Select, Stack, Text } from "@mantine/core";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useKnowledgeRevision, useKnowledgeRevisions } from "../../../hooks/useKnowledge";
import { formatLocaleDateTime } from "../../../lib/intl-format";
import { DiffView } from "../../narrator/diff/DiffView";

export function EntryHistory({ entryId }: { entryId: string }) {
	const { t } = useTranslation("knowledge");
	const revisions = useKnowledgeRevisions(entryId);
	const [leftId, setLeftId] = useState<string | null>(null);
	const [rightId, setRightId] = useState<string | null>(null);
	const leftRev = useKnowledgeRevision(leftId ?? undefined);
	const rightRev = useKnowledgeRevision(rightId ?? undefined);

	const revs = revisions.data ?? [];
	const options = useMemo(() => revs.map((r) => ({ value: r.id, label: `v${r.version}` })), [revs]);
	const bothSelected = !!leftId && !!rightId;
	const diffLoading = bothSelected && (leftRev.isLoading || rightRev.isLoading);
	const diffReady = bothSelected && !!leftRev.data && !!rightRev.data;

	if (revisions.isLoading) {
		return (
			<Text size="sm" c="dimmed">
				{t("loading")}
			</Text>
		);
	}
	if (revs.length === 0) {
		return (
			<Text size="sm" c="dimmed">
				{t("noRevisions")}
			</Text>
		);
	}

	return (
		<Stack>
			<Group gap="xs" align="flex-end" wrap="wrap">
				<Select
					label={t("version")}
					data={options}
					value={leftId}
					onChange={setLeftId}
					w={140}
					clearable
				/>
				<Text mt="lg">→</Text>
				<Select
					label={t("compareWith")}
					data={options}
					value={rightId}
					onChange={setRightId}
					w={140}
					clearable
				/>
			</Group>

			{diffLoading ? (
				<Text size="sm" c="dimmed">
					{t("loading")}
				</Text>
			) : diffReady ? (
				<DiffView
					oldStr={leftRev.data?.content ?? ""}
					newStr={rightRev.data?.content ?? ""}
					language="markdown"
					maxHeight={600}
					wordWrap
				/>
			) : (
				<Text size="xs" c="dimmed">
					{t("compareSelectHint")}
				</Text>
			)}

			<Stack gap="xs">
				{revs.map((r) => (
					<Paper key={r.id} withBorder p="xs">
						<Group justify="space-between" wrap="wrap">
							<Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
								<Badge variant="light">v{r.version}</Badge>
								{r.changeNote ? (
									<Text size="sm" truncate="end">
										{r.changeNote}
									</Text>
								) : null}
							</Group>
							<Group gap="xs">
								{/* Size comes from the SQL projection: no bodies are shipped. */}
								<Text size="xs" c="dimmed">
									{t("revisionSize", { count: r.contentLength })}
								</Text>
								<Text size="xs" c="dimmed">
									{formatLocaleDateTime(r.createdAt)}
								</Text>
							</Group>
						</Group>
					</Paper>
				))}
			</Stack>
		</Stack>
	);
}
