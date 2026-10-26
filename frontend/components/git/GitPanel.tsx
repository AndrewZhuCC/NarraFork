import { Alert, Box, Button, Group, Loader, Select, Stack, Switch, Text } from "@mantine/core";
import { IconRefresh } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
	type GitTarget,
	invalidateWorkspaceQueries,
	useGitWorkspace,
	useGitWorkspaceSubscription,
} from "../../hooks/useGit";
import { gitTargetKey } from "../../lib/api/git";
import { ConfirmDialogProvider } from "../common/ConfirmDialogProvider";
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
				<Alert color="gray" mx="xs" mt={4}>
					{t("workspace.detectionDisabled")}
				</Alert>
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
				<GitChangesTab target={target} onRefresh={refresh} />
				<GitCommitGraph target={target} />
			</Box>
		</div>
	);
}
