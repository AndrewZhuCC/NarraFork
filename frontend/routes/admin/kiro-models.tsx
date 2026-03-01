import {
	Badge,
	Button,
	Code,
	Group,
	Loader,
	Paper,
	Select,
	Stack,
	Table,
	Text,
	Title,
} from "@mantine/core";
import { IconRefresh } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCurrentUser } from "../../hooks/useAuth";
import { api } from "../../lib/api";

export const Route = createFileRoute("/admin/kiro-models")({
	component: KiroModelsPage,
});

function KiroModelsPage() {
	const { data: user } = useCurrentUser();
	const navigate = useNavigate();
	const { t } = useTranslation("common");
	const qc = useQueryClient();
	const [selectedCredential, setSelectedCredential] = useState<string | null>(null);

	const { data: status } = useQuery({
		queryKey: ["kiro", "status"],
		queryFn: api.kiroStatus,
		enabled: user?.role === "admin",
	});

	const credentialOptions =
		// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
		status?.snapshot?.entries?.map((e: any) => ({
			value: String(e.id),
			label: `#${e.id} ${e.email ?? ""} ${e.disabled ? "(disabled)" : ""}`.trim(),
		})) ?? [];

	const { data, isLoading, error } = useQuery({
		queryKey: ["kiro", "models", selectedCredential],
		queryFn: () => api.kiroListModels(selectedCredential || undefined),
		enabled: user?.role === "admin",
	});

	const refreshMutation = useMutation({
		mutationFn: () => api.kiroRefreshModels(selectedCredential || undefined),
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["kiro", "models"] });
			qc.invalidateQueries({ queryKey: ["settings"] });
		},
	});

	if (user && user.role !== "admin") {
		navigate({ to: "/" });
		return null;
	}

	const models = data?.models ?? [];

	return (
		<Stack>
			<Title order={2}>{t("kiroModels")}</Title>

			<Paper withBorder p="md">
				<Group>
					<Select
						label={t("kiroModelCredential")}
						placeholder={t("kiroModelCredentialDefault")}
						data={credentialOptions}
						value={selectedCredential}
						onChange={setSelectedCredential}
						clearable
						style={{ minWidth: 280 }}
					/>
					{data && (
						<Badge color="blue" mt="auto" size="lg">
							{t("kiroModelCount", { count: models.length })}
						</Badge>
					)}
					{data?.fromCache && (
						<Badge color="gray" mt="auto" size="sm" variant="outline">
							{t("kiroModelFromCache")}
						</Badge>
					)}
					<Button
						mt="auto"
						variant="light"
						leftSection={<IconRefresh size={16} />}
						loading={refreshMutation.isPending}
						onClick={() => refreshMutation.mutate()}
					>
						{t("kiroModelRefresh")}
					</Button>
				</Group>
			</Paper>

			{isLoading && <Loader />}

			{error && (
				<Paper withBorder p="md">
					<Text c="red">{(error as Error).message}</Text>
				</Paper>
			)}

			{refreshMutation.isError && (
				<Paper withBorder p="md">
					<Text c="red">{(refreshMutation.error as Error).message}</Text>
				</Paper>
			)}

			{models.length > 0 && (
				<Paper withBorder p="md">
					<Table striped highlightOnHover>
						<Table.Thead>
							<Table.Tr>
								<Table.Th>{t("kiroModelId")}</Table.Th>
								<Table.Th>{t("kiroModelName")}</Table.Th>
								<Table.Th>{t("kiroModelProvider")}</Table.Th>
								<Table.Th>{t("kiroModelInputTypes")}</Table.Th>
								<Table.Th>{t("kiroModelTokenLimits")}</Table.Th>
								<Table.Th>{t("kiroModelCaching")}</Table.Th>
							</Table.Tr>
						</Table.Thead>
						<Table.Tbody>
							{/* biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure */}
							{models.map((m: any, idx: number) => (
								<Table.Tr key={m.model_id ?? m.modelId ?? m.value ?? idx}>
									<Table.Td>
										<Code>{m.model_id ?? m.modelId ?? m.value ?? "-"}</Code>
									</Table.Td>
									<Table.Td>
										<Text size="sm">
											{m.model_short_name ??
												m.modelShortName ??
												m.model_name ??
												m.modelName ??
												m.label ??
												"-"}
										</Text>
									</Table.Td>
									<Table.Td>
										<Badge variant="light" size="sm">
											{m.model_provider ?? m.modelProvider ?? m.provider ?? "-"}
										</Badge>
									</Table.Td>
									<Table.Td>
										<Group gap={4}>
											{(m.supported_input_types ?? m.supportedInputTypes ?? []).map((t: string) => (
												<Badge key={t} size="xs" variant="outline">
													{t}
												</Badge>
											))}
										</Group>
									</Table.Td>
									<Table.Td>
										{(m.token_limits ?? m.tokenLimits) ? (
											<Code>{JSON.stringify(m.token_limits ?? m.tokenLimits)}</Code>
										) : (
											<Text size="xs" c="dimmed">
												-
											</Text>
										)}
									</Table.Td>
									<Table.Td>
										{(m.prompt_caching ?? m.promptCaching) ? (
											<Code>{JSON.stringify(m.prompt_caching ?? m.promptCaching)}</Code>
										) : (
											<Text size="xs" c="dimmed">
												-
											</Text>
										)}
									</Table.Td>
								</Table.Tr>
							))}
						</Table.Tbody>
					</Table>
				</Paper>
			)}

			{models.length > 0 && (
				<Paper withBorder p="md">
					<Text size="sm" fw={500} mb="xs">
						{t("kiroModelRawJson")}
					</Text>
					<Code block style={{ maxHeight: 400, overflow: "auto" }}>
						{JSON.stringify(models, null, 2)}
					</Code>
				</Paper>
			)}
		</Stack>
	);
}
