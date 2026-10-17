/**
 * `/knowledge/mine/$personalEntryId` — the ONE page of a personal version (§5.5), linked and
 * standalone alike: status bar, the unified editor (linked: diff against current main;
 * standalone: no baseline), drift handling, publish, the version's request history, delete.
 *
 * Everything the page may do comes from the detail payload's `viewerCaps` (same gates as the
 * endpoints); display names (linked entry title, target collection) are ACL-checked server-side.
 */
import { Alert, Button, Container, Grid, Group, Paper, Stack, Text, Title } from "@mantine/core";
import { IconAlertTriangle, IconArrowLeft, IconBook2, IconTrash } from "@tabler/icons-react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { flushSync } from "react-dom";
import { useTranslation } from "react-i18next";
import { DriftBanner } from "../../../components/knowledge/DriftBanner";
import { KnowledgeEditor } from "../../../components/knowledge/editor/KnowledgeEditor";
import { PersonalEntryRequests } from "../../../components/knowledge/PersonalEntryRequests";
import { PersonalEntryDeleteModal } from "../../../components/knowledge/personal/PersonalEntryDeleteModal";
import { PersonalEntryStatusBar } from "../../../components/knowledge/personal/PersonalEntryStatusBar";
import { PersonalPublishPanel } from "../../../components/knowledge/personal/PersonalPublishPanel";
import { personalEntryDisplayTitle } from "../../../components/knowledge/personal/personal-entry-view";
import {
	StandaloneMetaFields,
	type StandaloneMetaValue,
} from "../../../components/knowledge/personal/StandaloneMetaFields";
import { AnchorLink } from "../../../components/knowledge/router-links";
import {
	useDeletePersonalEntry,
	usePersonalEntry,
	useUpdatePersonalEntryContent,
	useUpdatePersonalEntryMeta,
} from "../../../hooks/useKnowledge";
import type { KnowledgePersonalEntryDetail } from "../../../lib/api";

export const Route = createFileRoute("/knowledge/mine/$personalEntryId")({
	component: PersonalEntryPage,
});

function PersonalEntryPage() {
	const { personalEntryId } = Route.useParams();
	const { t } = useTranslation("knowledge");
	const entry = usePersonalEntry(personalEntryId);

	if (entry.isLoading) {
		return (
			<Container size="lg" py="lg">
				<Text size="sm" c="dimmed">
					{t("loading")}
				</Text>
			</Container>
		);
	}
	if (entry.isError || !entry.data) {
		return (
			<Container size="lg" py="lg">
				<Stack gap="md" align="flex-start">
					<Alert
						color="red"
						title={t("personalEntryNotFound")}
						icon={<IconAlertTriangle size={16} />}
					>
						{t("personalEntryNotFoundDesc")}
					</Alert>
					<BackToMine />
				</Stack>
			</Container>
		);
	}
	// Keyed so local edit state never carries over to another personal version.
	return <PersonalEntryEditorPage key={entry.data.id} entry={entry.data} />;
}

function BackToMine() {
	const { t } = useTranslation("knowledge");
	return (
		<Button
			component={Link}
			to="/knowledge/mine"
			variant="subtle"
			size="compact-sm"
			leftSection={<IconArrowLeft size={14} />}
		>
			{t("mineBack")}
		</Button>
	);
}

function PersonalEntryEditorPage({ entry }: { entry: KnowledgePersonalEntryDetail }) {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	const caps = entry.viewerCaps;
	const linked = !!entry.entryId;
	const saveContent = useUpdatePersonalEntryContent();
	const remove = useDeletePersonalEntry();

	// Body: local until saved; `dirty` keeps refetches from clobbering edits.
	const [content, setContent] = useState(entry.content);
	const [dirty, setDirty] = useState(false);
	useEffect(() => {
		if (!dirty) setContent(entry.content);
	}, [entry.content, dirty]);
	const [metaDirty, setMetaDirty] = useState(false);
	const [confirmDelete, setConfirmDelete] = useState(false);

	const name = personalEntryDisplayTitle(entry) ?? t("mineUntitled");
	const onSaveContent = () =>
		saveContent.mutate({ id: entry.id, content }, { onSuccess: () => setDirty(false) });
	const beforeSubmit = async () => {
		if (!dirty) return;
		await saveContent.mutateAsync({ id: entry.id, content });
		setDirty(false);
	};
	const onDelete = () =>
		remove.mutate(entry.id, {
			onSuccess: () => {
				// The version is gone: drop unsaved edits synchronously so the editor's
				// unsaved-changes guard does not stop the navigation away.
				flushSync(() => {
					setContent(entry.content);
					setDirty(false);
					setConfirmDelete(false);
				});
				navigate({ to: "/knowledge/mine" });
			},
		});

	return (
		<Container size="lg" py="lg">
			<Group mb="xs">
				<BackToMine />
			</Group>
			<Group justify="space-between" align="flex-start" mb="xs" wrap="nowrap">
				<Group gap="xs" align="center" wrap="nowrap" style={{ minWidth: 0 }}>
					<IconBook2 size={20} style={{ color: "var(--mantine-color-grape-5)", flexShrink: 0 }} />
					<Title order={3}>{name}</Title>
				</Group>
				{caps.canDelete ? (
					<Button
						variant="subtle"
						color="red"
						size="compact-sm"
						leftSection={<IconTrash size={14} />}
						onClick={() => setConfirmDelete(true)}
					>
						{t("delete")}
					</Button>
				) : null}
			</Group>
			<Stack gap="md">
				<PersonalEntryStatusBar entry={entry} />

				{entry.status === "archived" ? (
					<Alert
						color="gray"
						title={t("personalEntryArchived")}
						icon={<IconAlertTriangle size={16} />}
					>
						{t("personalEntryArchivedHint")}
					</Alert>
				) : null}
				{linked && !entry.entryTitle ? (
					<Alert color="gray" icon={<IconAlertTriangle size={16} />}>
						{t("personalLinkedUnreadable")}
					</Alert>
				) : null}
				{linked && entry.entryId && caps.canRebase && entry.drift ? (
					<DriftBanner
						draftId={entry.id}
						entryId={entry.entryId}
						drifted={entry.drift.drifted}
						versionsBehind={entry.drift.versionsBehind}
						onResolved={() => setDirty(false)}
					/>
				) : null}

				<Grid gap="lg">
					<Grid.Col span={{ base: 12, md: 8 }}>
						<Stack gap="md">
							<Paper withBorder p="md" radius="md">
								<Stack gap="sm">
									<KnowledgeEditor
										label={t("personalEntryContent")}
										value={content}
										onChange={(v) => {
											setContent(v);
											setDirty(true);
										}}
										savedValue={entry.content}
										baseline={linked ? entry.currentMainContent : undefined}
										baselineLabel={t("personalVsMain")}
										placeholder={t("content")}
										onSave={onSaveContent}
										saving={saveContent.isPending}
										readOnly={!caps.canEdit}
										toolbar={
											<Button
												size="xs"
												variant="light"
												loading={saveContent.isPending}
												disabled={!caps.canEdit || !dirty}
												onClick={onSaveContent}
											>
												{t("save")}
											</Button>
										}
									/>
									{saveContent.isError ? (
										<Text size="xs" c="red">
											{(saveContent.error as Error).message}
										</Text>
									) : null}
									{entry.openSubmissions.some((s) => s.status === "pending") ? (
										<Text size="xs" c="dimmed">
											{t("editInvalidatesOpenSubmission")}
										</Text>
									) : null}
								</Stack>
							</Paper>
							<PersonalPublishPanel
								entry={entry}
								beforeSubmit={beforeSubmit}
								pendingLocalChanges={metaDirty}
							/>
							<PersonalEntryRequests personalEntryId={entry.id} />
						</Stack>
					</Grid.Col>
					<Grid.Col span={{ base: 12, md: 4 }}>
						{linked ? (
							<LinkedMetaCard entry={entry} />
						) : (
							<StandaloneMetaCard entry={entry} onDirtyChange={setMetaDirty} />
						)}
					</Grid.Col>
				</Grid>
			</Stack>

			<PersonalEntryDeleteModal
				entry={entry}
				name={name}
				opened={confirmDelete}
				onClose={() => setConfirmDelete(false)}
				onConfirm={onDelete}
				loading={remove.isPending}
				error={remove.isError ? (remove.error as Error).message : null}
			/>
		</Container>
	);
}

/** Linked versions carry no metadata of their own: it is the shared entry's. */
function LinkedMetaCard({ entry }: { entry: KnowledgePersonalEntryDetail }) {
	const { t } = useTranslation("knowledge");
	return (
		<Paper withBorder p="md" radius="md">
			<Stack gap="xs">
				<Text size="xs" fw={700} c="dimmed" tt="uppercase">
					{t("metaSettings")}
				</Text>
				<Text size="sm">{t("personalLinkedMeta")}</Text>
				{entry.entryId && entry.entryTitle ? (
					<AnchorLink to="/knowledge/$entryId" params={{ entryId: entry.entryId }} size="sm">
						{t("personalOpenSharedEntry")}
					</AnchorLink>
				) : null}
			</Stack>
		</Paper>
	);
}

function metaOf(entry: KnowledgePersonalEntryDetail): StandaloneMetaValue {
	return {
		title: entry.title ?? "",
		targetCollectionId: entry.targetCollectionId,
		keywords: entry.keywordsJson ?? [],
	};
}

function StandaloneMetaCard({
	entry,
	onDirtyChange,
}: {
	entry: KnowledgePersonalEntryDetail;
	onDirtyChange: (dirty: boolean) => void;
}) {
	const { t } = useTranslation("knowledge");
	const saveMeta = useUpdatePersonalEntryMeta();
	const [meta, setMeta] = useState<StandaloneMetaValue>(() => metaOf(entry));
	const [dirty, setDirty] = useState(false);
	useEffect(() => {
		if (!dirty) setMeta(metaOf(entry));
	}, [entry, dirty]);
	useEffect(() => onDirtyChange(dirty), [dirty, onDirtyChange]);

	const onSave = () =>
		saveMeta.mutate(
			{
				id: entry.id,
				title: meta.title.trim() || undefined,
				targetCollectionId: meta.targetCollectionId,
				keywords: meta.keywords,
			},
			{ onSuccess: () => setDirty(false) },
		);

	return (
		<Paper withBorder p="md" radius="md" style={{ position: "sticky", top: 80 }}>
			<Stack gap="sm">
				<Text size="xs" fw={700} c="dimmed" tt="uppercase">
					{t("metaSettings")}
				</Text>
				<StandaloneMetaFields
					value={meta}
					onChange={(next) => {
						setMeta(next);
						setDirty(true);
					}}
					disabled={!entry.viewerCaps.canEditMeta}
					currentTargetName={entry.targetCollectionName}
				/>
				<Button
					size="xs"
					variant="light"
					loading={saveMeta.isPending}
					disabled={!entry.viewerCaps.canEditMeta || !dirty || !meta.title.trim()}
					onClick={onSave}
					fullWidth
				>
					{t("save")}
				</Button>
				{saveMeta.isError ? (
					<Text size="xs" c="red">
						{(saveMeta.error as Error).message}
					</Text>
				) : null}
			</Stack>
		</Paper>
	);
}
