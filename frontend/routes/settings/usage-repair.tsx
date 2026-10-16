import { useCurrentUser } from "@frontend/hooks/useAuth";
import { usageTokenRepairApi as repairApi } from "@frontend/lib/usage-token-repair-api";
import {
	Alert,
	Badge,
	Button,
	Checkbox,
	Group,
	Modal,
	Paper,
	Select,
	Stack,
	Text,
	Title,
} from "@mantine/core";
import type {
	TokenRepairApplyResult,
	TokenRepairCandidate,
	TokenRepairJob,
	TokenRepairProtocolOverride,
} from "@shared/usage-token-repair";
import { TOKEN_REPAIR_MAX_PROTOCOL_OVERRIDES } from "@shared/usage-token-repair";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

export const Route = createFileRoute("/settings/usage-repair")({ component: UsageRepairPage });
const key = ["usage-token-repair"];
const fields = [
	"inputTokens",
	"cachedInputTokens",
	"cacheCreationInputTokens",
	"cacheCreation5mTokens",
	"cacheCreation1hTokens",
] as const;
const active = (job?: TokenRepairJob | null) =>
	job?.status === "analyzing" || job?.status === "applying";

const needsEnd = (job?: TokenRepairJob | null) =>
	!!job && !["completed", "cancelled"].includes(job.status);

const modelKey = (entry: { provider: string | null; model: string }) =>
	JSON.stringify([entry.provider, entry.model]);
const configKey = (entries: TokenRepairProtocolOverride[]) =>
	JSON.stringify(
		entries
			.map((entry) => [modelKey(entry), entry.protocol])
			.sort(([a], [b]) => String(a).localeCompare(String(b))),
	);

export function UsageRepairPage() {
	const { t } = useTranslation("common");
	const { data: user } = useCurrentUser();
	return user?.role === "admin" ? <RepairPanel /> : <Alert>{t("tokenRepair.adminOnly")}</Alert>;
}

function RepairPanel() {
	const { t } = useTranslation("common");
	const client = useQueryClient();
	const [includeReviewed, setIncludeReviewed] = useState(false);
	const [cursors, setCursors] = useState<string[]>([]);
	const [selected, setSelected] = useState<string[]>([]);
	const [preview, setPreview] = useState<TokenRepairCandidate[] | null>(null);
	const [result, setResult] = useState<TokenRepairApplyResult | null>(null);
	const latest = useQuery({
		queryKey: [...key, "jobs"],
		queryFn: ({ signal }) => repairApi.jobs(signal),
	});
	const jobId = latest.data?.job?.id;
	const detail = useQuery({
		queryKey: [...key, "job", jobId],
		queryFn: ({ signal }) => {
			if (!jobId) throw new Error(t("tokenRepair.noJob"));
			return repairApi.job(jobId, signal);
		},
		enabled: !!jobId,
		refetchInterval: (query) => (active(query.state.data?.job) ? 1500 : false),
	});
	const job = detail.data?.job ?? latest.data?.job;
	const [draftJobId, setDraftJobId] = useState(jobId);
	const [draft, setDraft] = useState<TokenRepairProtocolOverride[]>(job?.protocolOverrides ?? []);
	// Reset all previews before rendering a different immutable analysis job.
	if (draftJobId !== jobId) {
		setDraftJobId(jobId);
		setDraft(job?.protocolOverrides ?? []);
		setSelected([]);
		setPreview(null);
		setResult(null);
		setCursors([]);
	}
	const draftDirty = configKey(draft) !== configKey(job?.protocolOverrides ?? []);
	const cursor = cursors.at(-1);
	const candidates = useQuery({
		queryKey: [...key, "candidates", jobId, cursor],
		queryFn: ({ signal }) => {
			if (!jobId) throw new Error(t("tokenRepair.noJob"));
			return repairApi.candidates(jobId, cursor, signal);
		},
		enabled: !!jobId,
		refetchInterval: active(job) ? 1500 : false,
	});
	const isActive = active(job);
	const previousJob = useRef({ id: jobId, active: isActive });
	useEffect(() => {
		const previous = previousJob.current;
		previousJob.current = { id: jobId, active: isActive };
		if (jobId && previous.id === jobId && previous.active && !isActive) {
			void client.invalidateQueries({ queryKey: [...key, "candidates", jobId] });
		}
	}, [client, jobId, isActive]);
	const refresh = () => client.invalidateQueries({ queryKey: key });
	const action = useMutation({
		mutationFn: async (kind: "start" | "cancel" | "reanalyze") => {
			if (kind === "start")
				return draft.length
					? repairApi.start(includeReviewed, draft)
					: repairApi.start(includeReviewed);
			if (!jobId) throw new Error(t("tokenRepair.noJob"));
			if (kind === "reanalyze") {
				if (active(job) || !draftDirty || draft.length > TOKEN_REPAIR_MAX_PROTOCOL_OVERRIDES)
					throw new Error(t("tokenRepair.protocol.stopFirst"));
				const overrides = draft.map((entry) => ({ ...entry }));
				if (needsEnd(job)) {
					const cancelled = await repairApi.cancel(jobId);
					client.setQueryData([...key, "jobs"], cancelled);
					client.setQueryData([...key, "job", jobId], cancelled);
				}
				return repairApi.start(true, overrides, jobId);
			}
			return repairApi.cancel(jobId);
		},
		onSuccess: ({ job: updated }) => {
			client.setQueryData([...key, "jobs"], { job: updated });
			client.setQueryData([...key, "job", updated.id], { job: updated });
			setSelected([]);
			setPreview(null);
			setCursors([]);
			setResult(null);
			void refresh();
		},
	});
	const apply = useMutation({
		mutationFn: () => {
			if (
				!jobId ||
				!preview?.length ||
				preview.length > 50 ||
				draftDirty ||
				action.isPending ||
				job?.status !== "ready"
			)
				throw new Error(t("tokenRepair.noJob"));
			return repairApi.apply(
				jobId,
				preview.map((record) => record.id),
			);
		},
		onSuccess: (value) => setResult(value),
		onSettled: (_value, error) => {
			if (error) setResult(null);
			setPreview(null);
			setSelected([]);
			return Promise.all([client.invalidateQueries({ queryKey: ["usage-history"] }), refresh()]);
		},
	});
	const rows = candidates.data?.records ?? [];
	const chosen = rows.filter(
		(row) => selected.includes(row.id) && row.classification === "repairable" && !row.appliedAt,
	);
	const busy = action.isPending || apply.isPending;
	const error = latest.error ?? detail.error ?? candidates.error ?? action.error ?? apply.error;
	const groups = new Map<string, { provider: string | null; model: string }>();
	for (const row of rows) {
		if (
			row.model &&
			row.model.trim().length > 0 &&
			(row.protocol === "unknown" ||
				row.classification === "ambiguous" ||
				row.classification === "insufficient")
		) {
			const group = { provider: row.provider, model: row.model };
			groups.set(modelKey(group), group);
		}
	}
	for (const entry of [...(job?.protocolOverrides ?? []), ...draft])
		groups.set(modelKey(entry), entry);
	const changeProtocol = (
		group: { provider: string | null; model: string },
		value: string | null,
	) => {
		if (busy || active(job)) return;
		const remaining = draft.filter((entry) => modelKey(entry) !== modelKey(group));
		if (value && value !== "auto") {
			if (
				!["openai", "anthropic", "gemini"].includes(value) ||
				remaining.length >= TOKEN_REPAIR_MAX_PROTOCOL_OVERRIDES
			)
				return;
			remaining.push({ ...group, protocol: value as TokenRepairProtocolOverride["protocol"] });
		}
		setDraft(remaining);
		setSelected([]);
		setPreview(null);
		setResult(null);
	};
	const page = (next: string[]) => {
		setCursors(next);
		setSelected([]);
	};

	return (
		<Stack p="md">
			<Title order={2}>{t("tokenRepair.title")}</Title>
			<Alert>{t("tokenRepair.explanation")}</Alert>
			{error && <Alert color="red">{t("tokenRepair.failed")}</Alert>}
			<Checkbox
				label={t("tokenRepair.includeReviewed")}
				checked={includeReviewed}
				onChange={(event) => setIncludeReviewed(event.currentTarget.checked)}
			/>
			<Group>
				<Button
					disabled={busy || draftDirty || needsEnd(job) || latest.isPending || latest.isError}
					onClick={() => action.mutate("start")}
				>
					{t("tokenRepair.start")}
				</Button>
				{needsEnd(job) && (
					<Button disabled={busy} onClick={() => action.mutate("cancel")}>
						{t("tokenRepair.finish")}
					</Button>
				)}
			</Group>
			{needsEnd(job) && <Text size="sm">{t("tokenRepair.finishHelp")}</Text>}
			{job && (
				<Paper withBorder p="sm">
					<Stack gap="xs">
						<Badge>{t(`tokenRepair.status.${job.status}`)}</Badge>
						<Text>
							{t("tokenRepair.progress", {
								scanned: job.scanned,
								repaired: job.repaired,
								conflicts: job.conflicts,
							})}
						</Text>
						{job.sourceJobId && (
							<Text size="sm">
								{t("tokenRepair.protocol.sourceBatch", { jobId: job.sourceJobId })}
							</Text>
						)}
						{job.error && <Text c="red">{t("tokenRepair.jobError")}</Text>}
					</Stack>
				</Paper>
			)}
			{job && (
				<Paper withBorder p="sm">
					<Stack gap="xs">
						<Title order={3}>{t("tokenRepair.protocol.title")}</Title>
						<Text size="sm">{t("tokenRepair.protocol.help")}</Text>
						<Text size="sm">{t("tokenRepair.protocol.frozen")}</Text>
						{(job.protocolOverrides ?? []).length === 0 && (
							<Text size="sm">{t("tokenRepair.protocol.automatic")}</Text>
						)}
						{job.protocolOverrides?.map((entry) => (
							<Text size="sm" key={modelKey(entry)}>
								{entry.provider ?? t("tokenRepair.protocol.noProvider")} / {entry.model}:{" "}
								{t(`tokenRepair.protocol.${entry.protocol}`)}
							</Text>
						))}
						{Array.from(groups, ([identity, group]) => {
							const selectedProtocol = draft.find(
								(entry) => modelKey(entry) === identity,
							)?.protocol;
							return (
								<Select
									key={identity}
									label={`${group.provider ?? t("tokenRepair.protocol.noProvider")} / ${group.model}`}
									value={selectedProtocol ?? "auto"}
									allowDeselect={false}
									disabled={busy || active(job)}
									data={[
										{ value: "auto", label: t("tokenRepair.protocol.automatic") },
										...["openai", "anthropic", "gemini"].map((protocol) => ({
											value: protocol,
											label: t(`tokenRepair.protocol.${protocol}`),
											disabled:
												!selectedProtocol && draft.length >= TOKEN_REPAIR_MAX_PROTOCOL_OVERRIDES,
										})),
									]}
									onChange={(value) => changeProtocol(group, value)}
								/>
							);
						})}
						{active(job) && <Text size="sm">{t("tokenRepair.protocol.stopFirst")}</Text>}
						{draftDirty && <Alert color="yellow">{t("tokenRepair.protocol.draftWarning")}</Alert>}
						<Text size="sm">
							{t("tokenRepair.protocol.limit", {
								count: draft.length,
								max: TOKEN_REPAIR_MAX_PROTOCOL_OVERRIDES,
							})}
						</Text>
						<Button
							disabled={busy || active(job) || !draftDirty}
							onClick={() => action.mutate("reanalyze")}
						>
							{t("tokenRepair.protocol.reanalyze")}
						</Button>
						{draftDirty && (
							<Button
								variant="subtle"
								disabled={busy}
								onClick={() => {
									setDraft(job.protocolOverrides ?? []);
									setPreview(null);
									setSelected([]);
								}}
							>
								{t("tokenRepair.protocol.discard")}
							</Button>
						)}
					</Stack>
				</Paper>
			)}
			{result && <Alert>{t("tokenRepair.result", { ...result })}</Alert>}
			{job && (
				<>
					<Text size="sm">{t("tokenRepair.partial")}</Text>
					{candidates.isPending && <Text>{t("tokenRepair.loading")}</Text>}
					{!candidates.isPending && rows.length === 0 && <Text>{t("tokenRepair.empty")}</Text>}
					{rows.map((row) => (
						<Paper withBorder p="sm" key={row.id}>
							<Stack gap="xs">
								<Checkbox
									aria-label={row.requestId}
									disabled={
										busy ||
										draftDirty ||
										job.status !== "ready" ||
										row.classification !== "repairable" ||
										!!row.appliedAt
									}
									checked={selected.includes(row.id)}
									onChange={(event) =>
										setSelected(
											event.currentTarget.checked
												? [...selected, row.id]
												: selected.filter((id) => id !== row.id),
										)
									}
									label={`${row.requestId} · ${row.createdAt}`}
								/>
								<Text size="sm">
									{row.provider ?? "—"} / {row.model ?? "—"} · {row.protocol}
								</Text>
								{row.manualProtocol && (
									<Text size="sm">
										{t("tokenRepair.protocol.audit", {
											protocol: t(`tokenRepair.protocol.${row.manualProtocol}`),
										})}
									</Text>
								)}
								<Badge>{t(`tokenRepair.classification.${row.classification}`)}</Badge>
								<Text size="sm">
									{row.reason} · {row.evidence}
								</Text>
								{fields.map((field) => (
									<Text size="sm" key={field}>
										{t(`tokenRepair.fields.${field}`)}: {row.before[field]} →{" "}
										{row.after?.[field] ?? "—"}
									</Text>
								))}
								{row.appliedAt && <Text size="sm">{t("tokenRepair.applied")}</Text>}
							</Stack>
						</Paper>
					))}
					<Group>
						<Button
							disabled={!cursors.length || busy || candidates.isFetching}
							onClick={() => page(cursors.slice(0, -1))}
						>
							{t("tokenRepair.previous")}
						</Button>
						<Button
							disabled={!candidates.data?.nextCursor || busy || candidates.isFetching}
							onClick={() => {
								if (candidates.data?.nextCursor) page([...cursors, candidates.data.nextCursor]);
							}}
						>
							{t("tokenRepair.next")}
						</Button>
						<Button
							disabled={
								!chosen.length || chosen.length > 50 || busy || draftDirty || job.status !== "ready"
							}
							onClick={() => {
								apply.reset();
								setPreview(chosen);
							}}
						>
							{t("tokenRepair.preview", { count: chosen.length })}
						</Button>
					</Group>
				</>
			)}
			<Modal
				opened={!!preview}
				onClose={() => {
					if (!apply.isPending) setPreview(null);
				}}
				title={t("tokenRepair.confirmTitle")}
			>
				<Stack>
					<Text>{t("tokenRepair.confirm", { count: preview?.length ?? 0 })}</Text>
					{fields.map((field) => (
						<Text key={field}>
							{t(`tokenRepair.fields.${field}`)}:{" "}
							{(preview ?? []).reduce(
								(sum, row) => sum + (row.after?.[field] ?? row.before[field]) - row.before[field],
								0,
							)}
						</Text>
					))}
					{apply.isError && <Alert color="red">{t("tokenRepair.failed")}</Alert>}
					<Button
						disabled={!preview?.length || busy || draftDirty || job?.status !== "ready"}
						onClick={() => apply.mutate()}
					>
						{t("tokenRepair.confirmApply")}
					</Button>
				</Stack>
			</Modal>
		</Stack>
	);
}
