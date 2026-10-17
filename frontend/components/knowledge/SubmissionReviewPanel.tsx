/**
 * Reviewer actions on the request page: verdict + findings, and conflict resolution.
 *
 * Shown from the request's server-computed `viewerCaps` only — `canReview` (a verdict is
 * accepted: pending or conflict, reviewer authority, not self-review unless admin),
 * `canApprove` (pending only) and `canResolveConflict`. Linked and standalone requests use the
 * same panel; renders nothing for a viewer with none of those capabilities.
 */
import {
	Alert,
	Button,
	Group,
	Modal,
	Paper,
	SegmentedControl,
	Select,
	Stack,
	Text,
	TextInput,
} from "@mantine/core";
import { IconPlus, IconTrash } from "@tabler/icons-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
	useResolveKnowledgeConflict,
	useReviewKnowledgeSubmission,
} from "../../hooks/useKnowledge";
import type {
	FindingSeverity,
	KnowledgeFinding,
	KnowledgeSubmissionDetail,
	KnowledgeVerdict,
} from "../../lib/api";
import { KnowledgeEditor } from "./editor/KnowledgeEditor";
import { requestActions } from "./request-actions";

const SEVERITIES: FindingSeverity[] = ["critical", "major", "minor", "suggestion"];

export function SubmissionReviewPanel({ submission }: { submission: KnowledgeSubmissionDetail }) {
	const { t } = useTranslation("knowledge");
	const { verdicts, resolveConflict } = requestActions(submission.viewerCaps);
	if (verdicts.length === 0 && !resolveConflict) return null;
	return (
		<Paper withBorder p="md" radius="md">
			<Stack gap="md">
				<Text size="sm" fw={600}>
					{t("requestReviewActions")}
				</Text>
				{resolveConflict ? <ConflictResolver submission={submission} /> : null}
				{verdicts.length > 0 ? (
					<VerdictForm submissionId={submission.id} verdicts={verdicts} />
				) : null}
			</Stack>
		</Paper>
	);
}

function ConflictResolver({ submission }: { submission: KnowledgeSubmissionDetail }) {
	const { t } = useTranslation("knowledge");
	const resolveMut = useResolveKnowledgeConflict();
	const [resolved, setResolved] = useState<string>(submission.proposedContent);
	const submitResolve = () => resolveMut.mutate({ id: submission.id, resolvedContent: resolved });
	return (
		<Stack gap="sm">
			<Alert color="orange" title={t("conflict")} p="xs">
				<Text size="xs">{t("conflictDesc")}</Text>
			</Alert>
			<KnowledgeEditor
				label={t("resolvedContent")}
				value={resolved}
				onChange={setResolved}
				savedValue={submission.proposedContent}
				baseline={submission.currentMainContent}
				baselineLabel={t("resolvedVsMain")}
				onSave={resolved.trim() ? submitResolve : undefined}
				saving={resolveMut.isPending}
				minRows={6}
				maxRows={20}
			/>
			{resolveMut.isError ? (
				<Text size="xs" c="red">
					{(resolveMut.error as Error).message}
				</Text>
			) : null}
			<Group justify="flex-end">
				<Button onClick={submitResolve} loading={resolveMut.isPending} disabled={!resolved.trim()}>
					{t("resolveAndMerge")}
				</Button>
			</Group>
		</Stack>
	);
}

function VerdictForm({
	submissionId,
	verdicts,
}: {
	submissionId: string;
	verdicts: KnowledgeVerdict[];
}) {
	const { t } = useTranslation("knowledge");
	const reviewMut = useReviewKnowledgeSubmission();
	const [verdict, setVerdict] = useState<KnowledgeVerdict>(verdicts[0]);
	const [findings, setFindings] = useState<(KnowledgeFinding & { key: string })[]>([]);
	// Reject closes the request for good (no resubmit path), so it is confirmed separately.
	const [confirmReject, setConfirmReject] = useState(false);
	const current = verdicts.includes(verdict) ? verdict : verdicts[0];

	const addFinding = () =>
		setFindings((f) => [...f, { key: crypto.randomUUID(), severity: "minor", message: "" }]);
	const updateFinding = (key: string, patch: Partial<KnowledgeFinding>) =>
		setFindings((f) => f.map((x) => (x.key === key ? { ...x, ...patch } : x)));
	const removeFinding = (key: string) => setFindings((f) => f.filter((x) => x.key !== key));

	const submitReview = () => {
		reviewMut.mutate(
			{
				id: submissionId,
				verdict: current,
				findings: findings.filter((f) => f.message.trim()).map(({ key: _key, ...rest }) => rest),
			},
			{ onSuccess: () => setFindings([]) },
		);
	};

	return (
		<>
			<Modal
				opened={confirmReject}
				onClose={() => setConfirmReject(false)}
				title={t("rejectConfirmTitle")}
			>
				<Stack gap="md">
					<Alert color="red" p="xs">
						<Text size="xs">{t("rejectConfirmDesc")}</Text>
					</Alert>
					<Group justify="flex-end">
						<Button variant="subtle" size="xs" onClick={() => setConfirmReject(false)}>
							{t("cancel")}
						</Button>
						<Button
							size="xs"
							color="red"
							loading={reviewMut.isPending}
							onClick={() => {
								setConfirmReject(false);
								submitReview();
							}}
						>
							{t("reject")}
						</Button>
					</Group>
				</Stack>
			</Modal>
			<Stack gap="sm">
				<div>
					<Text size="xs" c="dimmed" mb={4}>
						{t("verdict")}
					</Text>
					<SegmentedControl
						value={current}
						onChange={(v) => setVerdict(v as KnowledgeVerdict)}
						data={verdicts.map((v) => ({ value: v, label: t(VERDICT_LABEL[v]) }))}
					/>
					{/* Reject is terminal and the author cannot resubmit, so say so before the
					    click rather than leaving them to discover it. */}
					<Text size="xs" c={current === "reject" ? "orange" : "dimmed"} mt={4}>
						{t(`verdictHint_${current}`)}
					</Text>
				</div>

				<Stack gap="xs">
					<Group justify="space-between">
						<Text size="xs" c="dimmed">
							{t("findings")}
						</Text>
						<Button
							size="compact-xs"
							variant="light"
							leftSection={<IconPlus size={12} />}
							onClick={addFinding}
						>
							{t("addFinding")}
						</Button>
					</Group>
					{findings.map((f) => (
						<Group key={f.key} gap="xs" wrap="nowrap" align="flex-start">
							<Select
								size="xs"
								w={120}
								value={f.severity}
								onChange={(v) => v && updateFinding(f.key, { severity: v as FindingSeverity })}
								data={SEVERITIES.map((s) => ({ value: s, label: t(`severity_${s}`) }))}
							/>
							<TextInput
								size="xs"
								style={{ flex: 1 }}
								placeholder={t("findingMessage")}
								value={f.message}
								onChange={(e) => updateFinding(f.key, { message: e.currentTarget.value })}
							/>
							<Button
								size="compact-xs"
								variant="subtle"
								color="red"
								aria-label={t("delete")}
								onClick={() => removeFinding(f.key)}
							>
								<IconTrash size={12} />
							</Button>
						</Group>
					))}
				</Stack>

				{reviewMut.isError ? (
					<Text size="xs" c="red">
						{(reviewMut.error as Error).message}
					</Text>
				) : null}
				<Group justify="flex-end">
					{/* Rejection is the only verdict the author cannot walk back, so it is the only
					    one that asks twice. */}
					<Button
						onClick={() => (current === "reject" ? setConfirmReject(true) : submitReview())}
						loading={reviewMut.isPending}
						color={current === "reject" ? "red" : undefined}
					>
						{t("submitVerdict")}
					</Button>
				</Group>
			</Stack>
		</>
	);
}

const VERDICT_LABEL: Record<KnowledgeVerdict, string> = {
	approve: "approve",
	request_changes: "requestChanges",
	reject: "reject",
	comment_only: "commentOnly",
};
