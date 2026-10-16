/**
 * `/knowledge/mine/new` — create a standalone personal entry (§4.1, §5.3): the unified editor
 * plus title, optional publish target and keywords. On save it opens the new entry's page.
 */
import { Button, Container, Grid, Group, Paper, Stack, Text, Title } from "@mantine/core";
import { IconArrowLeft } from "@tabler/icons-react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { flushSync } from "react-dom";
import { useTranslation } from "react-i18next";
import { KnowledgeEditor } from "../../../components/knowledge/editor/KnowledgeEditor";
import {
	StandaloneMetaFields,
	type StandaloneMetaValue,
} from "../../../components/knowledge/personal/StandaloneMetaFields";
import { useCreatePersonalEntry } from "../../../hooks/useKnowledge";
import { knowledgePersonalEntryDestination } from "../../../lib/knowledge-review-nav";

export const Route = createFileRoute("/knowledge/mine/new")({
	component: NewPersonalEntryPage,
});

const EMPTY_META: StandaloneMetaValue = { title: "", targetCollectionId: null, keywords: [] };

function NewPersonalEntryPage() {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	const create = useCreatePersonalEntry();
	const [meta, setMeta] = useState<StandaloneMetaValue>(EMPTY_META);
	const [content, setContent] = useState("");
	const canSave = !!meta.title.trim() && !create.isPending;

	const save = () => {
		if (!meta.title.trim()) return;
		create.mutate(
			{
				title: meta.title.trim(),
				content: content || undefined,
				targetCollectionId: meta.targetCollectionId ?? undefined,
				keywords: meta.keywords.length > 0 ? meta.keywords : undefined,
			},
			{
				onSuccess: (created) => {
					// Release the editor's unsaved-changes guard before leaving: the body is saved.
					flushSync(() => {
						setContent("");
						setMeta(EMPTY_META);
					});
					navigate({ ...knowledgePersonalEntryDestination(created.id), replace: true });
				},
			},
		);
	};

	return (
		<Container size="lg" py="lg">
			<Group mb="xs">
				<Button
					component={Link}
					to="/knowledge/mine"
					variant="subtle"
					size="compact-sm"
					leftSection={<IconArrowLeft size={14} />}
				>
					{t("mineBack")}
				</Button>
			</Group>
			<Stack gap="xs" mb="md">
				<Title order={3}>{t("newPersonalEntry")}</Title>
				<Text size="sm" c="dimmed">
					{t("mineNewDesc")}
				</Text>
			</Stack>
			<Grid gap="lg">
				<Grid.Col span={{ base: 12, md: 8 }}>
					<Paper withBorder p="md" radius="md">
						<KnowledgeEditor
							label={t("personalEntryContent")}
							placeholder={t("content")}
							value={content}
							onChange={setContent}
							savedValue=""
							onSave={canSave ? save : undefined}
							saving={create.isPending}
							minRows={10}
						/>
					</Paper>
				</Grid.Col>
				<Grid.Col span={{ base: 12, md: 4 }}>
					<Paper withBorder p="md" radius="md">
						<Stack gap="sm">
							<StandaloneMetaFields value={meta} onChange={setMeta} />
							<Button onClick={save} disabled={!canSave} loading={create.isPending} fullWidth>
								{t("mineCreate")}
							</Button>
							{create.isError ? (
								<Text size="xs" c="red">
									{(create.error as Error).message}
								</Text>
							) : null}
						</Stack>
					</Paper>
				</Grid.Col>
			</Grid>
		</Container>
	);
}
