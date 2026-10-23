import { Badge, Button, Group, Paper, Stack, Text, UnstyledButton } from "@mantine/core";
import { IconAlertTriangle, IconCheck, IconFolder, IconFolderOpen } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { PROJECT_DIR_STATUS_QUERY_KEY, useProjectDirStatus } from "../../hooks/useProjectDirStatus";
import { api } from "../../lib/api";
import { DirectoryBrowserModal } from "../common/DirectoryPicker";

/**
 * Pick the instance's default project directory from folders that already exist.
 *
 * There is no free-text field on purpose: a typed path was saved on every keystroke and
 * could name a folder that does not exist, which then broke every new session. Home
 * folders are organised differently on every machine (`Projects`, `code`, `dev`,
 * `Documents/GitHub`, ...), so instead of guessing one the server lists existing folders
 * that look like where this user keeps code, and anything else is one browse away.
 */
export function ProjectDirChooser() {
	const { t } = useTranslation("settings");
	const qc = useQueryClient();
	const { status } = useProjectDirStatus();
	const [browseOpen, setBrowseOpen] = useState(false);
	const { data: candidateData, isLoading: candidatesLoading } = useQuery({
		queryKey: ["project-dir-candidates"],
		queryFn: api.fsProjectDirCandidates,
		staleTime: 60_000,
	});
	const candidates = candidateData?.candidates ?? [];

	const choose = useMutation({
		mutationFn: (path: string) => api.updateSettings({ paths: { defaultProjectDir: path } }),
		onSuccess: () => {
			void qc.invalidateQueries({ queryKey: ["settings"] });
			void qc.invalidateQueries({ queryKey: PROJECT_DIR_STATUS_QUERY_KEY });
		},
	});

	const valid = status?.state === "valid";
	const current = valid ? status.path : null;

	return (
		<Stack gap={6}>
			<div>
				<Text size="sm" fw={500}>
					{t("defaultProjectDir")}
				</Text>
				<Text size="xs" c="dimmed">
					{t("projectDirChooserDesc")}
				</Text>
			</div>

			{current ? (
				<Paper withBorder radius="sm" px="sm" py={6}>
					<Group gap="xs" wrap="nowrap">
						<IconCheck size={16} color="var(--mantine-color-green-6)" />
						<Text size="sm" ff="monospace" truncate style={{ flex: 1, minWidth: 0 }}>
							{current}
						</Text>
					</Group>
				</Paper>
			) : (
				status && (
					<Group gap={6} wrap="nowrap">
						<IconAlertTriangle size={14} color="var(--mantine-color-orange-6)" />
						<Text size="xs" c="orange">
							{status.state === "unset"
								? t("projectDirUnset")
								: t("projectDirMissing", { path: status.path ?? status.configured })}
						</Text>
					</Group>
				)
			)}

			{candidates.length > 0 && (
				<Stack gap={4}>
					<Text size="xs" c="dimmed">
						{t("projectDirCandidates")}
					</Text>
					{candidates.map((candidate) => {
						const selected = candidate.path === current;
						return (
							<UnstyledButton
								key={candidate.path}
								onClick={() => choose.mutate(candidate.path)}
								disabled={choose.isPending || selected}
								aria-pressed={selected}
							>
								<Paper
									withBorder
									radius="sm"
									px="sm"
									py={6}
									bg={selected ? "var(--mantine-primary-color-light)" : undefined}
								>
									<Group gap="xs" wrap="nowrap">
										<IconFolder size={16} />
										<Text size="sm" ff="monospace" truncate style={{ flex: 1, minWidth: 0 }}>
											{candidate.path}
										</Text>
										{candidate.repoCount > 0 && (
											<Badge size="sm" variant="light" color="gray">
												{t("projectDirRepoCount", { count: candidate.repoCount })}
											</Badge>
										)}
									</Group>
								</Paper>
							</UnstyledButton>
						);
					})}
				</Stack>
			)}
			{!candidatesLoading && candidates.length === 0 && !current && (
				<Text size="xs" c="dimmed">
					{t("projectDirNoCandidates")}
				</Text>
			)}

			<Group>
				<Button
					variant="default"
					size="xs"
					leftSection={<IconFolderOpen size={14} />}
					loading={choose.isPending}
					onClick={() => setBrowseOpen(true)}
				>
					{current ? t("projectDirChange") : t("projectDirBrowse")}
				</Button>
			</Group>

			<DirectoryBrowserModal
				opened={browseOpen}
				onClose={() => setBrowseOpen(false)}
				initialPath={current ?? undefined}
				onSelect={(path) => {
					setBrowseOpen(false);
					choose.mutate(path);
				}}
			/>
		</Stack>
	);
}
