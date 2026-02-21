import {
	ActionIcon,
	Badge,
	Button,
	Group,
	Loader,
	Paper,
	Progress,
	Stack,
	Switch,
	Table,
	Text,
	Textarea,
	Title,
	Tooltip,
} from "@mantine/core";
import { IconRefresh, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCurrentUser } from "../../hooks/useAuth";
import { api } from "../../lib/api";

export const Route = createFileRoute("/admin/kiro-credentials")({
	component: KiroCredentialsPage,
});

function relativeTime(iso: string | undefined): string {
	if (!iso) return "-";
	const diff = Date.now() - new Date(iso).getTime();
	if (diff < 60_000) return "<1m ago";
	if (diff < 3600_000) return `${Math.floor(diff / 60_000)}m ago`;
	if (diff < 86400_000) return `${Math.floor(diff / 3600_000)}h ago`;
	return `${Math.floor(diff / 86400_000)}d ago`;
}

function KiroCredentialsPage() {
	const { data: user } = useCurrentUser();
	const navigate = useNavigate();
	const { t } = useTranslation("common");
	const qc = useQueryClient();
	const [importJson, setImportJson] = useState("");
	const [importError, setImportError] = useState<string | null>(null);
	const [importResult, setImportResult] = useState<string | null>(null);

	const { data: status, isLoading } = useQuery({
		queryKey: ["kiro", "status"],
		queryFn: api.kiroStatus,
		enabled: user?.role === "admin",
		refetchInterval: 30_000,
	});

	const snapshot = status?.snapshot;
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	const usageCache: Record<string, any> = (status as any)?.usageCache ?? {};

	const disableMut = useMutation({
		mutationFn: (id: number) => api.kiroDisableCredential(id),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
	});
	const enableMut = useMutation({
		mutationFn: (id: number) => api.kiroEnableCredential(id),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
	});
	const resetMut = useMutation({
		mutationFn: (id: number) => api.kiroResetCredential(id),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
	});
	const refreshMut = useMutation({
		mutationFn: (id: number) => api.kiroRefreshCredential(id),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
	});
	const usageMut = useMutation({
		mutationFn: (id: number) => api.kiroGetUsage(id),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
	});
	const importMut = useMutation({
		// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
		mutationFn: (creds: any[]) => api.kiroImportCredentials(creds),
		onSuccess: (data) => {
			setImportResult(t("kiroImportSuccess", { added: data.added, duplicates: data.duplicates }));
			setImportError(null);
			setImportJson("");
			qc.invalidateQueries({ queryKey: ["kiro", "status"] });
		},
		onError: (err: Error) => {
			setImportError(err.message);
			setImportResult(null);
		},
	});

	const deleteMut = useMutation({
		mutationFn: (id: number) => api.kiroDeleteCredential(id),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
	});

	const handleImport = () => {
		setImportError(null);
		setImportResult(null);
		try {
			const parsed = JSON.parse(importJson);
			const arr = Array.isArray(parsed) ? parsed : [parsed];
			importMut.mutate(arr);
		} catch {
			setImportError(t("kiroImportInvalidJson"));
		}
	};

	if (user && user.role !== "admin") {
		navigate({ to: "/" });
		return null;
	}

	if (isLoading) return <Loader />;

	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	const entries: any[] = snapshot?.entries ?? [];

	return (
		<Stack>
			<Title order={2}>{t("kiroCredentials")}</Title>

			{snapshot && (
				<Group>
					<Badge color="blue">
						{t("kiroTotal")}: {snapshot.total}
					</Badge>
					<Badge color="green">
						{t("kiroAvailable")}: {snapshot.available}
					</Badge>
					<Badge color="violet">
						{t("kiroCurrent")}: #{snapshot.currentId}
					</Badge>
					<Badge color="gray">{snapshot.loadBalancingMode}</Badge>
				</Group>
			)}

			<Paper withBorder p="md">
				<Stack>
					<Title order={4}>{t("kiroImport")}</Title>
					<Textarea
						placeholder={t("kiroImportPlaceholder")}
						value={importJson}
						onChange={(e) => setImportJson(e.currentTarget.value)}
						minRows={3}
						autosize
						styles={{ input: { fontFamily: "monospace", fontSize: 12 } }}
					/>
					{importError && (
						<Text c="red" size="sm">
							{importError}
						</Text>
					)}
					{importResult && (
						<Text c="green" size="sm">
							{importResult}
						</Text>
					)}
					<Button
						onClick={handleImport}
						disabled={!importJson.trim()}
						loading={importMut.isPending}
						style={{ alignSelf: "flex-start" }}
					>
						{t("kiroImportBtn")}
					</Button>
				</Stack>
			</Paper>

			{entries.length > 0 && (
				<Paper withBorder p="md">
					<Table striped highlightOnHover>
						<Table.Thead>
							<Table.Tr>
								<Table.Th>ID</Table.Th>
								<Table.Th>Email</Table.Th>
								<Table.Th>{t("kiroColPriority")}</Table.Th>
								<Table.Th>{t("kiroColStatus")}</Table.Th>
								<Table.Th>{t("kiroColExpires")}</Table.Th>
								<Table.Th>{t("kiroColStats")}</Table.Th>
								<Table.Th>{t("kiroColUsage")}</Table.Th>
								<Table.Th>{t("kiroColActions")}</Table.Th>
							</Table.Tr>
						</Table.Thead>
						<Table.Tbody>
							{/* biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure */}
							{entries.map((entry: any) => {
								const usage = usageCache[String(entry.id)];
								return (
									<Table.Tr key={entry.id}>
										<Table.Td>
											<Text size="sm" fw={entry.id === snapshot?.currentId ? 700 : 400}>
												#{entry.id}
											</Text>
										</Table.Td>
										<Table.Td>
											<Text size="sm">{entry.email ?? "-"}</Text>
										</Table.Td>
										<Table.Td>
											<Text size="sm">{entry.priority}</Text>
										</Table.Td>
										<Table.Td>
											{entry.disabled ? (
												<Badge color="red" size="sm">
													{entry.disabledReason ?? "disabled"}
												</Badge>
											) : (
												<Badge color="green" size="sm">
													enabled
												</Badge>
											)}
										</Table.Td>
										<Table.Td>
											<Text size="xs" c="dimmed">
												{entry.expiresAt ? relativeTime(entry.expiresAt) : "-"}
											</Text>
										</Table.Td>

										<Table.Td>
											<Text size="xs">
												{t("kiroSuccess")}: {entry.successCount} / {t("kiroFail")}:{" "}
												{entry.failureCount}
											</Text>
											<Text size="xs" c="dimmed">
												{relativeTime(entry.lastUsedAt)}
											</Text>
										</Table.Td>
										<Table.Td>
											{usage ? (
												<Stack gap={2}>
													{usage.subscriptionTitle && (
														<Badge size="xs" variant="light">
															{usage.subscriptionTitle}
														</Badge>
													)}
													<Text size="xs">
														{usage.currentUsage}/{usage.usageLimit}
													</Text>
													<Progress
														value={usage.usagePercentage}
														size="xs"
														color={
															usage.usagePercentage > 90
																? "red"
																: usage.usagePercentage > 70
																	? "yellow"
																	: "blue"
														}
														style={{ width: 80 }}
													/>
													<Text size="xs" c="dimmed">
														{relativeTime(usage.queriedAt)}
													</Text>
												</Stack>
											) : (
												<Text size="xs" c="dimmed">
													-
												</Text>
											)}
										</Table.Td>
										<Table.Td>
											<Group gap="xs">
												<Switch
													size="xs"
													checked={!entry.disabled}
													onChange={() =>
														entry.disabled
															? enableMut.mutate(entry.id)
															: disableMut.mutate(entry.id)
													}
												/>
												<Tooltip label={t("kiroRefreshToken")}>
													<ActionIcon
														variant="subtle"
														size="sm"
														onClick={() => refreshMut.mutate(entry.id)}
														loading={refreshMut.isPending}
													>
														<IconRefresh size={14} />
													</ActionIcon>
												</Tooltip>
												<Button
													variant="subtle"
													size="compact-xs"
													onClick={() => usageMut.mutate(entry.id)}
													loading={usageMut.isPending}
												>
													{t("kiroQueryUsage")}
												</Button>
												<Button
													variant="subtle"
													size="compact-xs"
													color="orange"
													onClick={() => resetMut.mutate(entry.id)}
												>
													{t("kiroReset")}
												</Button>
												<ActionIcon
													variant="subtle"
													color="red"
													size="sm"
													onClick={() => {
														if (confirm(t("kiroDeleteConfirm"))) {
															deleteMut.mutate(entry.id);
														}
													}}
												>
													<IconTrash size={14} />
												</ActionIcon>
											</Group>
										</Table.Td>
									</Table.Tr>
								);
							})}
						</Table.Tbody>
					</Table>
				</Paper>
			)}
		</Stack>
	);
}
