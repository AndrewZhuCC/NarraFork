import { useCurrentUser } from "@frontend/hooks/useAuth";
import { usageTokenRepairApi as repairApi } from "@frontend/lib/usage-token-repair-api";
import { Alert, Badge, Button, Group, Paper, Select, Stack, Text, Title } from "@mantine/core";
import {
	TOKEN_REPAIR_MAX_PROTOCOL_OVERRIDES,
	TOKEN_REPAIR_PROTOCOL_CONFIG_MAX_BYTES,
	type TokenRepairJob,
	type TokenRepairProtocolOverride,
	type TokenRepairSummary,
} from "@shared/usage-token-repair";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useState } from "react";
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

// Treat rules as a provider+model keyed set, independent of API ordering.
const rulesKey = (rules: TokenRepairProtocolOverride[] = []) =>
	JSON.stringify(rules.map((entry) => [modelKey(entry), entry.protocol]).sort());

export function UsageRepairPage() {
	const { t } = useTranslation("common");
	const { data: user } = useCurrentUser();
	return user?.role === "admin" ? <RepairPanel /> : <Alert>{t("tokenRepair.adminOnly")}</Alert>;
}

function RepairPanel() {
	const { t } = useTranslation("common");
	const client = useQueryClient();
	const [cursors, setCursors] = useState<string[]>([]);
	const [draftState, setDraftState] = useState<{
		jobId?: string;
		sourceRules: string;
		rules: TokenRepairProtocolOverride[];
		edited: boolean;
		invalid: boolean;
	}>({ sourceRules: rulesKey(), rules: [], edited: false, invalid: false });
	const draft = draftState.rules;
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
	useEffect(() => {
		if (job?.repaired) void client.invalidateQueries({ queryKey: ["usage-history"] });
	}, [client, job?.repaired]);
	const sourceRules = rulesKey(job?.protocolOverrides);
	if (draftState.jobId !== jobId) {
		setDraftState({
			jobId,
			sourceRules,
			rules: job?.protocolOverrides ?? [],
			edited: false,
			invalid: false,
		});
		setCursors([]);
	} else if (draftState.sourceRules !== sourceRules) {
		// Refetches (including late initial details) may replace the same job's rules.
		// Never overwrite unsent edits, even when the new server rules happen to match.
		setDraftState({
			...draftState,
			sourceRules,
			rules: draftState.edited ? draft : (job?.protocolOverrides ?? []),
			invalid: draftState.invalid || draftState.edited,
		});
	}
	const previewInvalid = draftState.invalid || rulesKey(draft) !== sourceRules;
	const v2 = job?.workflowVersion === 2;
	const ready = v2 && job?.status === "ready" && ["models", "preview"].includes(job.phase ?? "");
	const cursor = cursors.at(-1);
	const models = useQuery({
		queryKey: [...key, "models", jobId, cursor],
		queryFn: ({ signal }) => {
			if (!jobId) throw new Error(t("tokenRepair.noJob"));
			return repairApi.models(jobId, cursor, signal);
		},
		enabled: !!jobId && ready,
	});
	const updateJob = ({ job: updated }: { job: TokenRepairJob }) => {
		client.setQueryData([...key, "jobs"], { job: updated });
		client.setQueryData([...key, "job", updated.id], { job: updated });
		void client.invalidateQueries({ queryKey: key });
	};
	const action = useMutation({
		mutationFn: async (kind: "start" | "cancel" | "dryRun" | "apply") => {
			if (kind === "start") return repairApi.start(true);
			if (!jobId) throw new Error(t("tokenRepair.noJob"));
			if (kind === "cancel") return repairApi.cancel(jobId);
			if (!ready) throw new Error(t("tokenRepair.noJob"));
			if (kind === "dryRun") return repairApi.dryRun(jobId, draft);
			if (previewInvalid || job?.phase !== "preview" || !job.summary?.previewVersion)
				throw new Error(t("tokenRepair.noJob"));
			return repairApi.apply(jobId, job.summary.previewVersion);
		},
		onSuccess: (reply, kind) => {
			if (kind === "dryRun") {
				// The asynchronous acceptance reply freezes the submitted rules; the
				// completed preview arrives later through the detail query.
				setDraftState((current) =>
					current.jobId === reply.job.id &&
					rulesKey(current.rules) === rulesKey(reply.job.protocolOverrides)
						? {
								...current,
								sourceRules: rulesKey(reply.job.protocolOverrides),
								edited: false,
								invalid: false,
							}
						: current,
				);
			}
			updateJob(reply);
			if (kind === "apply") void client.invalidateQueries({ queryKey: ["usage-history"] });
		},
	});
	const busy = action.isPending || active(job);
	const tooManyRules =
		draft.length > TOKEN_REPAIR_MAX_PROTOCOL_OVERRIDES ||
		new TextEncoder().encode(JSON.stringify({ protocolOverrides: draft })).byteLength >
			TOKEN_REPAIR_PROTOCOL_CONFIG_MAX_BYTES;
	const error = latest.error ?? detail.error ?? models.error ?? action.error;
	const summary = v2 ? job?.summary : null;
	const showPreview = summary && job?.phase === "preview" && !previewInvalid;
	return (
		<Stack p="md">
			<Title order={2}>{t("tokenRepair.title")}</Title>
			<Alert>{t("tokenRepair.explanation")}</Alert>
			{error && <Alert color="red">{t("tokenRepair.failed")}</Alert>}
			<Group>
				<Button
					disabled={action.isPending || needsEnd(job) || latest.isPending || latest.isError}
					onClick={() => action.mutate("start")}
				>
					{t("tokenRepair.start")}
				</Button>
				{needsEnd(job) && (
					<Button disabled={action.isPending} onClick={() => action.mutate("cancel")}>
						{t("tokenRepair.finish")}
					</Button>
				)}
			</Group>
			{job && !v2 && <Alert color="yellow">{t("tokenRepair.legacy")}</Alert>}
			{job && (
				<Paper withBorder p="sm">
					<Badge>{t(`tokenRepair.status.${job.status}`)}</Badge>
					{v2 && job.phase && <Text>{t(`tokenRepair.phase.${job.phase}`)}</Text>}
					<Text>{t("tokenRepair.progress", { ...job })}</Text>
					{v2 && job.rowsPerSecond !== undefined && (
						<Text>{t("tokenRepair.rate", { count: job.rowsPerSecond })}</Text>
					)}
					{job.error && <Text c="red">{t("tokenRepair.jobError")}</Text>}
				</Paper>
			)}
			{ready && (
				<Paper withBorder p="sm">
					<Stack gap="xs">
						<Title order={3}>{t("tokenRepair.protocol.title")}</Title>
						<Text>{t("tokenRepair.protocol.help")}</Text>
						{models.isPending && <Text>{t("tokenRepair.loadingModels")}</Text>}
						{models.data?.models.length === 0 && <Text>{t("tokenRepair.noModels")}</Text>}
						{models.data?.models.map((model) => {
							const identity = modelKey(model);
							const selected = draft.find((entry) => modelKey(entry) === identity)?.protocol;
							return (
								<Select
									key={identity}
									label={`${model.provider ?? t("tokenRepair.protocol.noProvider")} / ${model.model}`}
									description={t("tokenRepair.modelRequests", { count: model.requestCount })}
									value={selected ?? "auto"}
									allowDeselect={false}
									disabled={busy}
									data={[
										{ value: "auto", label: t("tokenRepair.protocol.automatic") },
										...["openai", "anthropic", "gemini"].map((protocol) => ({
											value: protocol,
											label: t(`tokenRepair.protocol.${protocol}`),
										})),
									]}
									onChange={(value) => {
										if (busy) return;
										const rest = draft.filter((entry) => modelKey(entry) !== identity);
										if (value && ["openai", "anthropic", "gemini"].includes(value))
											rest.push({
												provider: model.provider,
												model: model.model,
												protocol: value as TokenRepairProtocolOverride["protocol"],
											});
										setDraftState({ ...draftState, rules: rest, edited: true, invalid: true });
									}}
								/>
							);
						})}
						<Group>
							<Button
								disabled={busy || !cursors.length}
								onClick={() => setCursors(cursors.slice(0, -1))}
							>
								{t("tokenRepair.previous")}
							</Button>
							<Button
								disabled={busy || !models.data?.nextCursor}
								onClick={() => {
									if (models.data?.nextCursor) setCursors([...cursors, models.data.nextCursor]);
								}}
							>
								{t("tokenRepair.next")}
							</Button>
						</Group>
						{previewInvalid && (
							<Alert color="yellow">{t("tokenRepair.protocol.draftWarning")}</Alert>
						)}
						{tooManyRules && <Alert color="red">{t("tokenRepair.protocol.limit")}</Alert>}
						<Button disabled={busy || tooManyRules} onClick={() => action.mutate("dryRun")}>
							{t("tokenRepair.dryRun")}
						</Button>
					</Stack>
				</Paper>
			)}
			{showPreview && <Summary summary={summary} />}
			{showPreview && <Alert color="yellow">{t("tokenRepair.confirmHelp")}</Alert>}
			{ready && job?.phase === "preview" && (
				<Button
					color="red"
					disabled={busy || previewInvalid || !summary?.previewVersion || !summary.repairable}
					onClick={() => action.mutate("apply")}
				>
					{t("tokenRepair.confirm")}
				</Button>
			)}
			{v2 &&
				summary &&
				(job?.phase === "applying" ||
					job?.phase === "done" ||
					job?.status === "cancelled" ||
					job?.status === "interrupted" ||
					job?.status === "failed") && (
					<>
						<Title order={3}>{t("tokenRepair.actualTitle")}</Title>
						<Text>
							{t("tokenRepair.result", {
								applied: job.repaired,
								conflicts: job.conflicts,
								skipped: summary.skipped,
							})}
						</Text>
						<TotalsTable summary={summary} actual />
					</>
				)}
			<Text size="sm">{t("tokenRepair.unchanged")}</Text>
			<Text size="sm">{t("tokenRepair.cancelHelp")}</Text>
		</Stack>
	);
}

function Summary({ summary }: { summary: TokenRepairSummary }) {
	const { t } = useTranslation("common");
	return (
		<Stack>
			<Title order={3}>{t("tokenRepair.previewTitle")}</Title>
			<Text>{t("tokenRepair.summary", { ...summary })}</Text>
			{Object.entries(summary.reasons).map(([reason, count]) => (
				<Text key={reason}>{t("tokenRepair.reason", { reason, count })}</Text>
			))}
			<TotalsTable summary={summary} />
		</Stack>
	);
}
function TotalsTable({
	summary,
	actual = false,
}: {
	summary: TokenRepairSummary;
	actual?: boolean;
}) {
	const { t } = useTranslation("common");
	const columns = actual
		? (["appliedDelta"] as const)
		: (["before", "after", "delta", "repairBefore", "repairAfter"] as const);
	return (
		<div style={{ overflowX: "auto" }}>
			<table>
				<caption>{t(actual ? "tokenRepair.actualTitle" : "tokenRepair.totalsTitle")}</caption>
				<thead>
					<tr>
						<th>{t("tokenRepair.metric")}</th>
						{columns.map((column) => (
							<th key={column}>{t(`tokenRepair.${column}`)}</th>
						))}
					</tr>
				</thead>
				<tbody>
					{fields.map((field) => (
						<tr key={field}>
							<th scope="row">{t(`tokenRepair.fields.${field}`)}</th>
							{columns.map((column) => (
								<td key={column}>{summary[column][field]}</td>
							))}
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}
