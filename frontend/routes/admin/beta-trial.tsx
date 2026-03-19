import {
	Alert,
	Anchor,
	Badge,
	Button,
	Code,
	CopyButton,
	Group,
	List,
	Paper,
	PasswordInput,
	Stack,
	Text,
	ThemeIcon,
	Timeline,
	Title,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconArrowLeft, IconCheck, IconKey, IconServer, IconUsers } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { nanoid } from "nanoid";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "../../lib/api";

export const Route = createFileRoute("/admin/beta-trial")({
	component: BetaTrialPage,
});

const BETA_PROVIDER_NAME = "NarraFork 官方";
const BETA_PROVIDER_PREFIX = "dome";
const BETA_BASE_URL = "https://nkp.b.domexie.cn";

function BetaTrialPage() {
	const { t } = useTranslation("settings");
	const navigate = useNavigate();
	const qc = useQueryClient();
	const [apiKey, setApiKey] = useState("");

	const { data: settings } = useQuery({
		queryKey: ["admin", "settings"],
		queryFn: api.getSettings,
	});

	// Check if a provider with prefix "dome" already exists
	const existingProvider = (settings?.nkpProviders ?? []).find(
		// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
		(p: any) => p.prefix === BETA_PROVIDER_PREFIX,
	);

	const createMutation = useMutation({
		mutationFn: async (key: string) => {
			const currentNkpProviders =
				// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
				(settings?.nkpProviders ?? []).map((p: any) => ({
					id: p.id ?? "",
					name: p.name ?? "",
					prefix: p.prefix ?? "nkp",
					apiKey: p.apiKey ?? "",
					baseUrl: p.baseUrl ?? "",
					defaultModel: p.defaultModel ?? "",
				}));

			const newId = nanoid(8);
			const newProvider = {
				id: newId,
				name: BETA_PROVIDER_NAME,
				prefix: BETA_PROVIDER_PREFIX,
				apiKey: key,
				baseUrl: BETA_BASE_URL,
				defaultModel: "",
			};

			// Save settings with the new provider
			await api.updateSettings({
				nkpProviders: [...currentNkpProviders, newProvider],
			});

			// Invalidate to get the saved state
			await qc.invalidateQueries({ queryKey: ["admin", "settings"] });
			await qc.invalidateQueries({ queryKey: ["settings"] });

			// Refresh models for the new provider
			const result = await api.nkpRefreshProviderModels(newId);
			return { providerId: newId, modelCount: result.models?.length ?? 0 };
		},
		onSuccess: (data) => {
			notifications.show({
				color: "green",
				title: "✅",
				message: t("betaTrialSuccess", { count: data.modelCount }),
			});
			qc.invalidateQueries({ queryKey: ["admin", "settings"] });
			qc.invalidateQueries({ queryKey: ["settings"] });
			// Open the setup wizard and jump to the next step (BasicSettings = step 3)
			window.dispatchEvent(new CustomEvent("narrafork:open-wizard", { detail: { step: 3 } }));
		},
		onError: (err: Error) => {
			notifications.show({
				color: "red",
				message: t("betaTrialError", { error: err.message }),
			});
		},
	});

	const handleSubmit = () => {
		const trimmed = apiKey.trim();
		if (!trimmed) return;
		createMutation.mutate(trimmed);
	};

	return (
		<Stack maw={640} mx="auto" py="xl">
			<Group>
				<Button
					variant="subtle"
					leftSection={<IconArrowLeft size={16} />}
					onClick={() => navigate({ to: "/admin/providers" })}
				>
					{t("betaTrialBack")}
				</Button>
			</Group>

			<Group gap="sm">
				<IconServer size={28} color="var(--mantine-color-indigo-5)" />
				<Title order={2}>{t("betaTrialTitle")}</Title>
			</Group>

			<Text size="sm" c="dimmed">
				{t("betaTrialDesc")}
			</Text>

			{existingProvider && !createMutation.isSuccess ? (
				<Alert color="blue" title={t("betaTrialAlreadyExists")}>
					<Button variant="light" mt="sm" onClick={() => navigate({ to: "/admin/providers" })}>
						{t("betaTrialGoToProviders")}
					</Button>
				</Alert>
			) : createMutation.isSuccess ? (
				<Alert color="green" icon={<IconCheck size={18} />}>
					<Stack gap="sm">
						<Text size="sm">
							{t("betaTrialSuccess", { count: createMutation.data?.modelCount ?? 0 })}
						</Text>
						<Button variant="light" onClick={() => navigate({ to: "/admin/providers" })}>
							{t("betaTrialGoToProviders")}
						</Button>
					</Stack>
				</Alert>
			) : (
				<Timeline active={2} bulletSize={32} lineWidth={2}>
					<Timeline.Item
						bullet={
							<ThemeIcon size={28} radius="xl" color="indigo">
								<IconKey size={16} />
							</ThemeIcon>
						}
						title={t("betaTrialStep1Title")}
					>
						<Text size="sm" c="dimmed" mt={4}>
							{t("betaTrialStep1Desc")}
						</Text>
						<Anchor
							href="https://nkp.b.domexie.cn"
							target="_blank"
							rel="noopener noreferrer"
							size="sm"
							mt="xs"
						>
							{t("betaTrialStep1Link")} ↗
						</Anchor>
					</Timeline.Item>

					<Timeline.Item
						bullet={
							<ThemeIcon size={28} radius="xl" color="grape">
								<IconUsers size={16} />
							</ThemeIcon>
						}
						title={t("betaTrialStep2Title")}
					>
						<Text size="sm" c="dimmed" mt={4}>
							{t("betaTrialStep2Desc")}
						</Text>
						<Group gap="xs" mt="xs">
							<Badge variant="light" color="blue" size="lg">
								QQ 群：138118443
							</Badge>
							<CopyButton value="138118443">
								{({ copied, copy }) => (
									<Button variant="subtle" size="xs" onClick={copy}>
										{copied ? "✓" : t("betaTrialCopyGroup")}
									</Button>
								)}
							</CopyButton>
						</Group>
					</Timeline.Item>

					<Timeline.Item
						bullet={
							<ThemeIcon size={28} radius="xl" color="green">
								<IconCheck size={16} />
							</ThemeIcon>
						}
						title={t("betaTrialStep3Title")}
					>
						<Text size="sm" c="dimmed" mt={4}>
							{t("betaTrialStep3Desc")}
						</Text>
						<Stack gap="sm" mt="sm">
							<PasswordInput
								placeholder={t("betaTrialApiKeyPlaceholder")}
								value={apiKey}
								onChange={(e) => setApiKey(e.currentTarget.value)}
								disabled={createMutation.isPending}
							/>
							<Paper withBorder p="xs" bg="var(--mantine-color-dark-7)">
								<List size="xs" c="dimmed" spacing={4}>
									<List.Item>
										供应商名称：<Code>{BETA_PROVIDER_NAME}</Code>
									</List.Item>
									<List.Item>
										前缀：<Code>{BETA_PROVIDER_PREFIX}</Code>（模型格式：
										<Code>dome:模型名</Code>）
									</List.Item>
									<List.Item>
										地址：<Code>{BETA_BASE_URL}</Code>
									</List.Item>
								</List>
							</Paper>
							<Button
								onClick={handleSubmit}
								loading={createMutation.isPending}
								disabled={!apiKey.trim()}
							>
								{createMutation.isPending ? t("betaTrialSubmitting") : t("betaTrialSubmit")}
							</Button>
						</Stack>
					</Timeline.Item>
				</Timeline>
			)}
		</Stack>
	);
}
