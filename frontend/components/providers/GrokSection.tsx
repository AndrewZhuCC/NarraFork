import {
	Alert,
	Badge,
	Button,
	Group,
	ScrollArea,
	Stack,
	Switch,
	Text,
	TextInput,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconInfoCircle, IconRefresh } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import React, { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "../../lib/api";
import { normalizeUrlProtocol } from "../../lib/url";

interface GrokSectionProps {
	settings: Record<string, unknown> | undefined;
	hiddenModels: Set<string>;
	onToggleHidden: (modelVal: string) => void;
	modelContextWindows: Record<string, number>;
	onContextWindowChange: (modelVal: string, size: number | null) => void;
	onTestModel?: (model: string) => void;
}

export const GrokSection = React.memo(function GrokSection({
	settings: settingsData,
	hiddenModels,
	onToggleHidden,
	modelContextWindows,
	onContextWindowChange,
	onTestModel,
}: GrokSectionProps) {
	const { t } = useTranslation("settings");
	const qc = useQueryClient();

	const grokProviders: Array<{
		id: string;
		prefix?: string;
		name?: string;
		baseUrl?: string;
		apiKey?: string;
		defaultModel?: string;
		enableWebSearch?: boolean;
		enableXSearch?: boolean;
	}> = (settingsData?.grokProviders as typeof grokProviders) ?? [];
	const provider = grokProviders[0];

	const grokModelsGrouped: Array<{
		providerId: string;
		providerName: string;
		models: Array<{ id: string; name?: string; contextLength?: number }>;
	}> = (settingsData?.grokModelsGrouped as typeof grokModelsGrouped) ?? [];
	const models = grokModelsGrouped[0]?.models ?? [];

	const [baseUrl, setBaseUrl] = useState(provider?.baseUrl ?? "https://api.x.ai/v1");
	const [apiKey, setApiKey] = useState("");
	const [defaultModel, setDefaultModel] = useState(provider?.defaultModel ?? "grok-4-latest");
	const [enableWebSearch, setEnableWebSearch] = useState(provider?.enableWebSearch ?? true);
	const [enableXSearch, setEnableXSearch] = useState(provider?.enableXSearch ?? false);

	const saveMutation = useMutation({
		mutationFn: () => {
			const next = grokProviders.map((p) => ({
				...p,
				baseUrl: normalizeUrlProtocol(baseUrl) ?? "",
				...(apiKey.trim() ? { apiKey: apiKey.trim() } : {}),
				defaultModel,
				enableWebSearch,
				enableXSearch,
			}));
			return api.updateSettings({ grokProviders: next });
		},
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["admin", "settings"] });
			notifications.show({ message: t("settingsSaved"), color: "green" });
		},
		onError: (error: Error) => {
			notifications.show({ message: error.message, color: "red" });
		},
	});

	const refreshMutation = useMutation({
		mutationFn: () => api.grokRefreshModels(provider?.id ?? ""),
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["admin", "settings"] });
			notifications.show({ message: t("modelsRefreshed"), color: "green" });
		},
		onError: (error: Error) => {
			notifications.show({ message: error.message, color: "red" });
		},
	});

	const visibleModels = useMemo(() => {
		return models.filter((m) => !hiddenModels.has(`grok:${m.id}`));
	}, [models, hiddenModels]);

	if (!provider) {
		return (
			<Alert variant="light" color="gray" icon={<IconInfoCircle size={16} />}>
				{t("grokNotConfigured")}
			</Alert>
		);
	}

	return (
		<Stack gap="md">
			<Group gap="sm">
				<Badge size="sm" variant="light" color="indigo">
					Grok
				</Badge>
				<Button
					size="xs"
					variant="light"
					leftSection={<IconRefresh size={14} />}
					onClick={() => refreshMutation.mutate()}
					loading={refreshMutation.isPending}
				>
					{t("refreshModels")}
				</Button>
				<Button
					size="xs"
					variant="filled"
					onClick={() => saveMutation.mutate()}
					loading={saveMutation.isPending}
				>
					{t("save")}
				</Button>
			</Group>

			<Stack gap="xs">
				<TextInput
					label={t("apiBaseUrl")}
					value={baseUrl}
					onChange={(e) => setBaseUrl(e.currentTarget.value)}
					size="xs"
				/>
				<TextInput
					label={t("apiKey")}
					value={apiKey}
					onChange={(e) => setApiKey(e.currentTarget.value)}
					placeholder="xai-..."
					type="password"
					autoComplete="off"
					size="xs"
				/>
				<TextInput
					label={t("defaultModel")}
					value={defaultModel}
					onChange={(e) => setDefaultModel(e.currentTarget.value)}
					size="xs"
				/>
				<Group gap="lg">
					<Switch
						label={t("grokEnableWebSearch")}
						checked={enableWebSearch}
						onChange={(e) => setEnableWebSearch(e.currentTarget.checked)}
						size="xs"
					/>
					<Switch
						label={t("grokEnableXSearch")}
						checked={enableXSearch}
						onChange={(e) => setEnableXSearch(e.currentTarget.checked)}
						size="xs"
					/>
				</Group>
			</Stack>

			{models.length === 0 ? (
				<Alert variant="light" color="blue" icon={<IconInfoCircle size={16} />}>
					{t("grokNoModelsHint")}
				</Alert>
			) : (
				<ScrollArea.Autosize mah={320}>
					<Stack gap={4}>
						{visibleModels.map((m) => {
							const value = `grok:${m.id}`;
							const hidden = hiddenModels.has(value);
							return (
								<Group key={m.id} gap="xs" justify="space-between">
									<Text size="xs" lineClamp={1} style={{ flex: 1 }}>
										{m.name || m.id}
									</Text>
									{m.contextLength ? (
										<Badge size="xs" variant="light">
											{m.contextLength.toLocaleString()}
										</Badge>
									) : null}
									<Button size="compact-xs" variant="subtle" onClick={() => onToggleHidden(value)}>
										{hidden ? t("show") : t("hide")}
									</Button>
									{onTestModel ? (
										<Button size="compact-xs" variant="light" onClick={() => onTestModel(value)}>
											{t("test")}
										</Button>
									) : null}
								</Group>
							);
						})}
					</Stack>
				</ScrollArea.Autosize>
			)}

			{apiKey.trim() ? (
				<Alert variant="light" color="teal" icon={<IconInfoCircle size={16} />}>
					{t("grokApiKeySetHint")}
				</Alert>
			) : (
				<Alert variant="light" color="yellow" icon={<IconAlertTriangle size={16} />}>
					{t("grokApiKeyMissing")}
				</Alert>
			)}
		</Stack>
	);
});
