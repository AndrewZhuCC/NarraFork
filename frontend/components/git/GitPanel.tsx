import { Alert, Box, Button, Group, Loader, Select, Stack, Switch, Text } from "@mantine/core";
import { IconRefresh } from "@tabler/icons-react";
import {
	QueryClient,
	QueryClientContext,
	useMutation,
	useQueryClient,
} from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import {
	GitLiveUpdatesContext,
	type GitTarget,
	gitWorkspaceTarget,
	invalidateWorkspaceQueries,
	retainManualGitWorkspace,
	useGitWorkspace,
	useGitWorkspaceSubscription,
} from "../../hooks/useGit";
import { type ApiError, api } from "../../lib/api";
import { type GitWorkspace, gitTargetKey } from "../../lib/api/git";
import { describeApiError } from "../../lib/api-error";
import { narratorWSManager } from "../../lib/narrator-ws-manager";
import { ConfirmDialogProvider } from "../common/ConfirmDialogProvider";
import { GitBranchBar } from "./GitBranchBar";
import { GitChangesTab } from "./GitChangesTab";
import { GitCommitGraph } from "./GitCommitGraph";

/** Every narrator host resolves its own workspace capability. */
export function GitPanel({ narratorId }: { narratorId?: string }) {
	if (narratorId) return <NarratorGitPanel narratorId={narratorId} />;
	return null;
}

function NarratorGitPanel({ narratorId }: { narratorId: string }) {
	const { t } = useTranslation(["git", "errors"]);
	const qc = useQueryClient();
	const workspaceQuery = useGitWorkspace(narratorId);
	const workspace = workspaceQuery.data;
	const target = workspaceQuery.target;
	const repositories = workspace?.capabilities.read ? (workspace.repositories ?? []) : [];
	const state = workspaceQuery.authorizationState;
	const described = describeApiError(workspaceQuery.error, t);
	const denied = workspaceQuery.accessDenied || state === "denied";
	const errorDetail =
		denied || described.status === 401 || described.status === 403
			? undefined
			: workspaceQuery.error
				? described.status === 503 && !described.localized
					? t("workspace.refresh_required")
					: described.message
				: workspace?.reason;
	const getSubject = useCallback(() => manualGitSubject(qc), [qc]);
	const observeSubject = useCallback(
		(listener: () => void) => {
			const unobserveCache = qc.getQueryCache().subscribe(({ query }) => {
				if (query.queryKey[0] === "auth" && query.queryKey[1] === "me") listener();
			});
			const unobserveCredential = narratorWSManager.onCredentialChange(listener);
			return () => {
				unobserveCache();
				unobserveCredential();
			};
		},
		[qc],
	);
	const subject = useSyncExternalStore(observeSubject, getSubject, getSubject);

	return (
		<Stack gap={4} style={{ height: "100%", minHeight: 0 }}>
			<Box px="xs" pt="xs">
				<Switch
					size="sm"
					checked={workspaceQuery.detectionEnabled}
					label={t("workspace.detectionToggle")}
					description={t("workspace.detectionDescription")}
					onChange={(event) => workspaceQuery.setDetectionEnabled(event.currentTarget.checked)}
				/>
			</Box>
			{!workspaceQuery.detectionEnabled ? (
				<ManualGitPanel
					key={JSON.stringify([workspaceQuery.contextKey, subject])}
					narratorId={narratorId}
					subject={subject}
					switching={workspaceQuery.workspaceTransitioning}
				/>
			) : workspaceQuery.isPending && !denied ? (
				<Group justify="center" style={{ flex: 1 }}>
					<Loader size="sm" />
				</Group>
			) : (
				<>
					{repositories.length > 1 && (
						<Select
							mx="xs"
							mt="xs"
							size="xs"
							aria-label={t("workspace.repositorySelect")}
							data={repositories.map((repository) => ({
								value: repository.workspaceKey,
								label: repository.relativePath || ".",
							}))}
							value={workspace?.workspaceKey}
							allowDeselect={false}
							onChange={(workspaceKey) => workspaceQuery.selectWorkspace(workspaceKey)}
						/>
					)}
					{workspace?.capabilities.read && workspace.discoveryTruncated && (
						<Alert color="yellow" mx="xs" mt={repositories.length > 1 ? 0 : "xs"} py={4}>
							<Text size="xs">{t("workspace.discoveryTruncated")}</Text>
						</Alert>
					)}
					{!target ? (
						<Stack gap={4} p="xs">
							<Group justify="flex-end">
								<Button
									size="compact-xs"
									variant="subtle"
									leftSection={<IconRefresh size={13} />}
									loading={workspaceQuery.isFetching}
									onClick={workspaceQuery.refreshAuthorization}
								>
									{t("workspace.retry")}
								</Button>
							</Group>
							<Alert
								color="yellow"
								title={t(
									`workspace.${
										denied
											? "access_denied"
											: (workspace?.state ??
												(state === "refresh_required"
													? "refresh_required"
													: state === "paused" || state === "workspace_changed"
														? "recovering"
														: workspaceQuery.isError
															? "error"
															: "loading"))
									}`,
								)}
							>
								{errorDetail}
							</Alert>
						</Stack>
					) : (
						<ConfirmDialogProvider
							key={`${narratorId}:${gitTargetKey(target)}:${workspace?.cwd}:${workspace?.capabilities.write}`}
						>
							<GitPanelContent
								target={target}
								onRefresh={() => invalidateWorkspaceQueries(qc, target)}
								readOnly={!workspace?.capabilities.write}
							/>
						</ConfirmDialogProvider>
					)}
				</>
			)}
		</Stack>
	);
}

function manualGitSubject(qc: QueryClient) {
	return JSON.stringify([
		qc.getQueryData<{ id?: unknown }>(["auth", "me"])?.id,
		narratorWSManager.getCredentialIdentity(),
	]);
}

/** A user-triggered read; no detection preference or shared live cache is changed. */
function ManualGitPanel({
	narratorId,
	subject,
	switching,
}: {
	narratorId: string;
	subject: string;
	switching: boolean;
}) {
	const { t } = useTranslation("git");
	const qc = useQueryClient();
	const controller = useRef<AbortController | null>(null);
	const scanId = useRef(0);
	useEffect(() => () => controller.current?.abort(), []);
	const scan = useMutation({
		networkMode: "always",
		mutationFn: async (workspaceKey?: string) => {
			if (manualGitSubject(qc) !== subject)
				throw new DOMException("Git principal superseded", "AbortError");
			controller.current?.abort();
			const abort = new AbortController();
			controller.current = abort;
			let workspace: GitWorkspace;
			try {
				workspace = await api.getGitWorkspace(narratorId, abort.signal, workspaceKey, true);
			} catch (error) {
				if (!workspaceKey || (error as ApiError)?.status !== 409) throw error;
				abort.signal.throwIfAborted();
				workspace = await api.getGitWorkspace(narratorId, abort.signal, undefined, true);
			}
			abort.signal.throwIfAborted();
			if (manualGitSubject(qc) !== subject)
				throw new DOMException("Git principal superseded", "AbortError");
			return { workspace, scanId: ++scanId.current };
		},
		retry: false,
	});
	const refresh = () => scan.mutate(scan.data?.workspace.workspaceKey ?? undefined);
	const denied = [401, 403].includes((scan.error as ApiError | null)?.status ?? 0);
	return (
		<>
			<Alert color="gray" mx="xs" mt={4}>
				<Stack gap="xs">
					<Text size="sm">{t("workspace.detectionDisabled")}</Text>
					<Button
						size="compact-xs"
						variant="light"
						leftSection={<IconRefresh size={13} />}
						loading={scan.isPending}
						disabled={switching}
						onClick={refresh}
					>
						{t("workspace.scanOnce")}
					</Button>
				</Stack>
			</Alert>
			{scan.isError && (
				<Alert color="yellow" mx="xs" title={t(`workspace.${denied ? "access_denied" : "error"}`)}>
					{denied ? undefined : scan.error.message}
				</Alert>
			)}
			{scan.data && (
				<ManualGitSnapshot
					key={scan.data.scanId}
					narratorId={narratorId}
					workspace={scan.data.workspace}
					principal={qc.getQueryData<{ id?: string }>(["auth", "me"])?.id}
					onRefresh={refresh}
					onSelect={(key) => {
						if (key) scan.mutate(key);
					}}
				/>
			)}
		</>
	);
}

function ManualGitSnapshot({
	narratorId,
	workspace,
	principal,
	onRefresh,
	onSelect,
}: {
	narratorId: string;
	workspace: GitWorkspace;
	principal?: string;
	onRefresh: () => void;
	onSelect: (key: string | null) => void;
}) {
	const { t } = useTranslation("git");
	const target = useMemo(() => gitWorkspaceTarget(narratorId, workspace), [narratorId, workspace]);
	const [client] = useState(
		() =>
			new QueryClient({
				defaultOptions: {
					queries: {
						networkMode: "always",
						staleTime: Infinity,
						refetchOnMount: false,
						refetchOnWindowFocus: false,
						refetchOnReconnect: false,
						refetchInterval: false,
						retry: false,
					},
					mutations: { networkMode: "always", retry: false },
				},
			}),
	);
	const [ready, setReady] = useState(false);
	useEffect(() => {
		if (!target) return;
		if (principal) client.setQueryData(["auth", "me"], { id: principal });
		const release = retainManualGitWorkspace(client, target);
		setReady(true);
		return () => {
			release();
			client.clear();
		};
	}, [client, principal, target]);
	if (!target) {
		return (
			<Alert color="yellow" mx="xs" title={t(`workspace.${workspace.state}`)}>
				{workspace.state === "access_denied" ? undefined : workspace.reason}
			</Alert>
		);
	}
	if (!ready) return <Loader size="sm" />;
	return (
		<GitLiveUpdatesContext value={false}>
			{/* Unlike QueryClientProvider, this does not register focus/online listeners. */}
			<QueryClientContext value={client}>
				{(workspace.repositories?.length ?? 0) > 1 && (
					<Select
						mx="xs"
						size="xs"
						aria-label={t("workspace.repositorySelect")}
						data={workspace.repositories?.map((repository) => ({
							value: repository.workspaceKey,
							label: repository.relativePath || ".",
						}))}
						value={workspace.workspaceKey}
						allowDeselect={false}
						onChange={onSelect}
					/>
				)}
				{workspace.discoveryTruncated && (
					<Alert color="yellow" mx="xs" py={4}>
						<Text size="xs">{t("workspace.discoveryTruncated")}</Text>
					</Alert>
				)}
				<ConfirmDialogProvider>
					<GitPanelContent
						target={target}
						onRefresh={onRefresh}
						readOnly={!workspace.capabilities.write}
					/>
				</ConfirmDialogProvider>
			</QueryClientContext>
		</GitLiveUpdatesContext>
	);
}

/** The panel for an already-resolved target; exported for tests. */
export function GitPanelContent({
	target,
	onRefresh,
	readOnly = false,
}: {
	target: GitTarget;
	onRefresh?: () => void;
	readOnly?: boolean;
}) {
	const { t } = useTranslation("git");
	const qc = useQueryClient();
	const subscriptions = useGitWorkspaceSubscription(target);
	const state = subscriptions.getState(target);

	const refresh = () => {
		if (onRefresh) onRefresh();
		else invalidateWorkspaceQueries(qc, target);
	};

	if (!subscriptions.canRead(target))
		return (
			<Stack gap={4} p="xs">
				<Group justify="flex-end">
					<Button
						size="compact-xs"
						variant="subtle"
						leftSection={<IconRefresh size={13} />}
						onClick={refresh}
					>
						{t("workspace.retry")}
					</Button>
				</Group>
				<Alert
					color="yellow"
					title={t(
						`workspace.${state === "denied" ? "access_denied" : state === "refresh_required" ? "refresh_required" : state === "waiting" ? "loading" : "recovering"}`,
					)}
				/>
			</Stack>
		);

	return (
		<div
			style={{ height: "100%", flex: 1, display: "flex", flexDirection: "column", minHeight: 0 }}
		>
			{readOnly && (
				<Alert color="yellow" m="xs" mb={0}>
					{t("workspace.readOnly")}
				</Alert>
			)}
			<Box style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
				<GitBranchBar target={target} readOnly={readOnly} />
				<GitChangesTab target={target} onRefresh={refresh} />
				<GitCommitGraph target={target} />
			</Box>
		</div>
	);
}
