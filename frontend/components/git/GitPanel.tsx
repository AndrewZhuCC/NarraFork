import { Alert, Box, Button, Group, Loader, Select, Stack, Switch, Text } from "@mantine/core";
import { IconRefresh } from "@tabler/icons-react";
import {
	QueryClient,
	QueryClientProvider,
	useMutation,
	useQueryClient,
} from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	GitLiveUpdatesContext,
	type GitTarget,
	gitWorkspaceTarget,
	invalidateWorkspaceQueries,
	useGitWorkspace,
	useGitWorkspaceSubscription,
} from "../../hooks/useGit";
import { type ApiError, api } from "../../lib/api";
import { type GitWorkspace, gitTargetKey } from "../../lib/api/git";
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
	const { t } = useTranslation("git");
	const qc = useQueryClient();
	const workspaceQuery = useGitWorkspace(narratorId);
	const workspace = workspaceQuery.data;
	const target = workspaceQuery.target;
	const repositories = workspace?.capabilities.read ? (workspace.repositories ?? []) : [];
	const errorStatus = (workspaceQuery.error as { status?: number } | null)?.status;
	const errorDetail =
		workspaceQuery.accessDenied ||
		errorStatus === 401 ||
		errorStatus === 403 ||
		workspace?.capabilities.read === false
			? undefined
			: workspaceQuery.error?.message || workspace?.reason;

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
				<ManualGitPanel key={workspaceQuery.contextKey} narratorId={narratorId} />
			) : workspaceQuery.isPending && !workspaceQuery.accessDenied ? (
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
									onClick={() => workspaceQuery.refetch()}
								>
									{t("workspace.retry")}
								</Button>
							</Group>
							<Alert
								color="yellow"
								title={t(
									`workspace.${
										workspaceQuery.accessDenied
											? "access_denied"
											: workspaceQuery.isError
												? "error"
												: (workspace?.state ?? "error")
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

/** A user-triggered read; no detection preference or shared live cache is changed. */
function ManualGitPanel({ narratorId }: { narratorId: string }) {
	const { t } = useTranslation("git");
	const controller = useRef<AbortController | null>(null);
	const scanId = useRef(0);
	useEffect(() => () => controller.current?.abort(), []);
	const scan = useMutation({
		mutationFn: async (workspaceKey?: string) => {
			controller.current?.abort();
			const abort = new AbortController();
			controller.current = abort;
			let workspace: GitWorkspace;
			try {
				workspace = await api.getGitWorkspace(narratorId, abort.signal, workspaceKey, true);
			} catch (error) {
				if (!workspaceKey || (error as ApiError)?.status !== 409) throw error;
				workspace = await api.getGitWorkspace(narratorId, abort.signal, undefined, true);
			}
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
	onRefresh,
	onSelect,
}: {
	narratorId: string;
	workspace: GitWorkspace;
	onRefresh: () => void;
	onSelect: (key: string | null) => void;
}) {
	const { t } = useTranslation("git");
	const target = gitWorkspaceTarget(narratorId, workspace);
	const [client] = useState(() => {
		const qc = new QueryClient({
			defaultOptions: {
				queries: {
					staleTime: Infinity,
					refetchOnMount: false,
					refetchOnWindowFocus: false,
					refetchOnReconnect: false,
					refetchInterval: false,
					retry: false,
				},
				mutations: { retry: false },
			},
		});
		if (target) invalidateWorkspaceQueries(qc, target);
		return qc;
	});
	useEffect(() => () => client.clear(), [client]);
	if (!target) {
		return (
			<Alert color="yellow" mx="xs" title={t(`workspace.${workspace.state}`)}>
				{workspace.state === "access_denied" ? undefined : workspace.reason}
			</Alert>
		);
	}
	return (
		<GitLiveUpdatesContext value={false}>
			<QueryClientProvider client={client}>
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
			</QueryClientProvider>
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
	useGitWorkspaceSubscription(target);

	const refresh = () => {
		if (onRefresh) onRefresh();
		else invalidateWorkspaceQueries(qc, target);
	};

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
