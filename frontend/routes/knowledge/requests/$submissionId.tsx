/**
 * Publish request detail — the ONE page for a request (docs/KNOWLEDGE_UI_REDESIGN.md §5.7).
 *
 * Linked and standalone requests, for the author, a reviewer, an admin or anyone else who may
 * view it, all land here (bell, review inbox, my library, personal-entry history, legacy URL
 * redirects). Actions come only from the server's `viewerCaps` on the request:
 *  - reviewer → verdict / findings, conflict resolution (SubmissionReviewPanel);
 *  - author   → withdraw / re-submit (SubmissionAuthorActions) and a link to edit the version;
 *  - others   → read-only.
 */
import {
	Alert,
	Anchor,
	Badge,
	Button,
	Container,
	Group,
	Paper,
	Stack,
	Text,
	Title,
} from "@mantine/core";
import {
	IconAlertTriangle,
	IconArrowLeft,
	IconFileText,
	IconFolder,
	IconGitMerge,
	IconPencil,
} from "@tabler/icons-react";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { requestActions } from "../../../components/knowledge/request-actions";
import { SubmissionAuthorActions } from "../../../components/knowledge/SubmissionAuthorActions";
import { SubmissionBody } from "../../../components/knowledge/SubmissionBody";
import { SubmissionReviewPanel } from "../../../components/knowledge/SubmissionReviewPanel";
import { SubmissionStatusBadge } from "../../../components/knowledge/SubmissionStatusBadge";
import { SubmissionTimeline } from "../../../components/knowledge/SubmissionTimeline";
import { useKnowledgeSubmission } from "../../../hooks/useKnowledge";
import type { KnowledgeSubmissionDetail } from "../../../lib/api";
import { formatLocaleDateTime } from "../../../lib/intl-format";
import {
	knowledgePersonalEntryDestination,
	knowledgeRequestDestination,
} from "../../../lib/knowledge-review-nav";

export const Route = createFileRoute("/knowledge/requests/$submissionId")({
	component: RequestPage,
});

const OPEN_STATUSES = new Set(["pending", "conflict", "changes_requested"]);

function RequestPage() {
	const { t } = useTranslation("knowledge");
	const { submissionId } = Route.useParams();
	const detail = useKnowledgeSubmission(submissionId);

	return (
		<Container size="lg" py="lg">
			<Group mb="xs">
				<Button
					component={Link}
					to="/knowledge/review"
					variant="subtle"
					size="compact-sm"
					leftSection={<IconArrowLeft size={14} />}
				>
					{t("reviewInboxBack")}
				</Button>
			</Group>
			{detail.isLoading ? (
				<Text size="sm" c="dimmed">
					{t("loading")}
				</Text>
			) : detail.data ? (
				<RequestDetail submission={detail.data} />
			) : (
				<Alert color="red" title={t("requestNotFound")} icon={<IconAlertTriangle size={16} />}>
					{t("requestNotFoundDesc")}
				</Alert>
			)}
		</Container>
	);
}

function RequestDetail({ submission: s }: { submission: KnowledgeSubmissionDetail }) {
	const { t } = useTranslation("knowledge");
	const navigate = useNavigate();
	const caps = s.viewerCaps;
	const isOpen = OPEN_STATUSES.has(s.status);
	const title = s.entryId
		? (s.entryTitle ?? t("requestUnreadableEntry"))
		: s.title || t("reviewCenterUntitledEntry");
	const actions = requestActions(caps);

	return (
		<Stack gap="md">
			<div>
				<Group gap="xs" align="center" wrap="nowrap">
					{s.entryId ? (
						<IconFileText
							size={20}
							style={{ color: "var(--mantine-color-blue-5)", flexShrink: 0 }}
						/>
					) : (
						<IconFolder size={20} style={{ color: "var(--mantine-color-teal-5)", flexShrink: 0 }} />
					)}
					<Title order={3} style={{ minWidth: 0, overflowWrap: "anywhere" }}>
						{title}
					</Title>
				</Group>
				<Group gap="xs" mt={6} wrap="wrap">
					<SubmissionStatusBadge status={s.status} size="sm" />
					<Badge size="sm" variant="light" color={s.entryId ? "blue" : "teal"}>
						{s.entryId ? t("requestKindLinked") : t("reviewCenterNewEntry")}
					</Badge>
					<Badge size="sm" variant="outline" color="grape">
						{t("subFlowRound", { round: s.round ?? 1 })}
					</Badge>
					{s.previousSubmissionId ? (
						<Anchor
							component="button"
							type="button"
							size="xs"
							onClick={() =>
								s.previousSubmissionId &&
								navigate(knowledgeRequestDestination(s.previousSubmissionId))
							}
						>
							{t("requestPreviousRound")}
						</Anchor>
					) : null}
				</Group>
			</div>

			<Paper withBorder p="md" radius="md">
				<Stack gap={6}>
					<MetaRow label={t("requestSubmitter")}>
						{s.submitterUsername ?? t("requestUnknownUser")}
						{caps.isSubmitter ? ` (${t("requestYou")})` : ""}
						{" · "}
						{formatLocaleDateTime(s.createdAt)}
					</MetaRow>
					<MetaRow label={t("requestTarget")}>
						{s.entryId && s.entryTitle ? (
							<Anchor
								component="button"
								type="button"
								size="sm"
								onClick={() =>
									s.entryId &&
									navigate({ to: "/knowledge/$entryId", params: { entryId: s.entryId } })
								}
							>
								{s.entryTitle}
							</Anchor>
						) : (
							(s.collectionName ?? "—")
						)}
						{s.entryId && s.entryTitle && s.collectionName ? ` · ${s.collectionName}` : ""}
					</MetaRow>
					{s.changeNote ? (
						<MetaRow label={t("changeNote")}>
							<span style={{ whiteSpace: "pre-wrap" }}>{s.changeNote}</span>
						</MetaRow>
					) : null}
				</Stack>
			</Paper>

			{isOpen && s.drift?.drifted ? (
				<Alert
					color="yellow"
					icon={<IconGitMerge size={16} />}
					title={t("requestDriftTitle")}
					p="xs"
				>
					<Text size="xs">{t("requestDriftDesc", { count: s.drift.versionsBehind })}</Text>
				</Alert>
			) : null}

			<SubmissionBody submission={s} />

			<SubmissionReviewPanel key={`${s.id}:${s.status}`} submission={s} />

			{actions.editVersion || actions.withdraw || actions.resubmit ? (
				<Paper withBorder p="md" radius="md">
					<Group justify="space-between" wrap="wrap" gap="xs">
						<Text size="sm" fw={600}>
							{t("requestAuthorActions")}
						</Text>
						<Group gap="xs">
							{actions.editVersion ? (
								<Button
									size="xs"
									variant="light"
									leftSection={<IconPencil size={12} />}
									onClick={() => navigate(knowledgePersonalEntryDestination(s.draftId))}
								>
									{t("requestEditVersion")}
								</Button>
							) : null}
							<SubmissionAuthorActions
								size="xs"
								submissionId={s.id}
								caps={caps}
								personalEntryId={s.draftId}
								onResubmitted={(id) => navigate(knowledgeRequestDestination(id))}
							/>
						</Group>
					</Group>
				</Paper>
			) : null}

			{actions.readOnly ? (
				<Text size="xs" c="dimmed">
					{isOpen ? t("requestReadOnly") : t("requestClosed")}
				</Text>
			) : null}

			<SubmissionTimeline currentId={s.id} history={s.history} />
		</Stack>
	);
}

function MetaRow({ label, children }: { label: string; children: ReactNode }) {
	return (
		<Group gap="xs" wrap="nowrap" align="flex-start">
			<Text size="xs" c="dimmed" w={88} style={{ flexShrink: 0 }}>
				{label}
			</Text>
			<Text size="sm" style={{ minWidth: 0, overflowWrap: "anywhere" }}>
				{children}
			</Text>
		</Group>
	);
}
