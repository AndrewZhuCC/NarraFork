import {
	ActionIcon,
	Badge,
	Button,
	Divider,
	Group,
	PasswordInput,
	Stack,
	Switch,
	Text,
	TextInput,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconRefresh, IconTrash } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import React, { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	useProviderModelRefreshCapability,
	useProviderQuotaCapability,
	useProviderRuntimeCapability,
} from "../../hooks/usePlatform";
import { api } from "../../lib/api";
import type { ModelOption } from "../../lib/constants";
import type { CustomModelEntry } from "./InlineCustomModels";
import { InlineCustomModels } from "./InlineCustomModels";
import { ModelList } from "./ModelList";
import type { NKPProviderState } from "./types";

type ProvidersUpdater = NKPProviderState[] | ((prev: NKPProviderState[]) => NKPProviderState[]);

interface NKPProvidersSectionProps {
	providers: NKPProviderState[];
	onProvidersChange: (updater: ProvidersUpdater) => void;
	providerModelsMap: Record<string, ModelOption[]>;
	hiddenModels: Set<string>;
	onToggleHidden: (modelVal: string) => void;
	modelContextWindows: Record<string, number>;
	onContextWindowChange: (modelVal: string, size: number | null) => void;
	isProviderDirty?: (providerId: string) => boolean;
	onSaveBeforeRefresh?: () => Promise<boolean>;
	quotas?: Record<string, { quotaBalance: number | null; quotaTotalGranted: number | null }>;
	customModels: CustomModelEntry[];
	onCustomModelsChange: (models: CustomModelEntry[]) => void;
	getPrefixError?: (prefix: string, providerId: string) => string | undefined;
	getUniquePrefix?: (base: string, providerId: string) => string;
	onTestModel?: (model: string) => void;
}

/** Sanitize a prefix value: any text except an ASCII colon is allowed. */
function sanitizePrefix(value: string): string {
	return value.replace(/:/g, "");
}

export const NKPProvidersSection = React.memo(function NKPProvidersSection({
	providers,
	onProvidersChange,
	providerModelsMap,
	hiddenModels,
	onToggleHidden,
	modelContextWindows,
	onContextWindowChange,
	isProviderDirty,
	onSaveBeforeRefresh,
	quotas,
	customModels,
	onCustomModelsChange,
	getPrefixError,
	getUniquePrefix,
	onTestModel,
}: NKPProvidersSectionProps) {
	const { t } = useTranslation("settings");
	const qc = useQueryClient();
	const nkpRuntimeCapability = useProviderRuntimeCapability("nkp");
	const nkpRefreshCapability = useProviderModelRefreshCapability("nkp");
	const providerRouteUnsupportedReason = t("providerRouteUnsupported");
	const nkpRefreshReason = nkpRefreshCapability.reason ?? t("providerRefreshModelsUnsupported");
	const nkpRoutesSupported = nkpRuntimeCapability?.routes?.supported !== false;
	const canRefreshNkpProviderModels =
		nkpRefreshCapability.supported &&
		nkpRoutesSupported &&
		nkpRuntimeCapability?.routes?.perProviderModelsRefresh !== false;
	const nkpRefreshUnsupportedReason = nkpRefreshCapability.supported
		? providerRouteUnsupportedReason
		: nkpRefreshReason;
	const nkpQuotaCapability = useProviderQuotaCapability("nkp");
	const [refreshingProvider, setRefreshingProvider] = useState<string | null>(null);

	const handleRemoveProvider = useCallback(
		(id: string) => {
			onProvidersChange((prev) => prev.filter((p) => p.id !== id));
		},
		[onProvidersChange],
	);

	const updateProvider = useCallback(
		(id: string, field: keyof NKPProviderState, value: string) => {
			onProvidersChange((prev) => prev.map((p) => (p.id === id ? { ...p, [field]: value } : p)));
		},
		[onProvidersChange],
	);

	// Track which providers have a manually-edited prefix (keyed by id). A provider
	// that already has a prefix is treated as locked; new ones start name-driven.
	const [manualPrefixIds, setManualPrefixIds] = useState<Set<string>>(
		() => new Set(providers.filter((p) => p.prefix).map((p) => p.id)),
	);

	const handleNameChange = useCallback(
		(id: string, name: string) => {
			const manual = manualPrefixIds.has(id);
			onProvidersChange((prev) =>
				prev.map((p) => {
					if (p.id !== id) return p;
					if (manual) return { ...p, name };
					const base = sanitizePrefix(name.trim());
					const nextPrefix = base ? (getUniquePrefix?.(base, p.id) ?? base) : "";
					return { ...p, name, prefix: nextPrefix };
				}),
			);
		},
		[onProvidersChange, manualPrefixIds, getUniquePrefix],
	);

	const handlePrefixChange = useCallback(
		(id: string, value: string) => {
			const next = sanitizePrefix(value);
			setManualPrefixIds((prev) => {
				const updated = new Set(prev);
				if (next.length > 0) updated.add(id);
				else updated.delete(id);
				return updated;
			});
			updateProvider(id, "prefix", next);
		},
		[updateProvider],
	);

	const toggleProviderDisabled = useCallback(
		(id: string) => {
			onProvidersChange((prev) =>
				prev.map((p) => (p.id === id ? { ...p, disabled: !p.disabled } : p)),
			);
		},
		[onProvidersChange],
	);

	const handleRefreshProviderModels = useCallback(
		async (providerId: string) => {
			if (!canRefreshNkpProviderModels) return;
			if (isProviderDirty?.(providerId)) {
				const saved = await onSaveBeforeRefresh?.();
				if (!saved) return;
			}
			setRefreshingProvider(providerId);
			try {
				await api.nkpRefreshProviderModels(providerId);
				qc.invalidateQueries({ queryKey: ["admin", "settings"] });
				qc.invalidateQueries({ queryKey: ["settings"] });
			} catch {
				notifications.show({ color: "red", title: t("nkpRefreshModelsError"), message: "" });
			} finally {
				setRefreshingProvider(null);
			}
		},
		[canRefreshNkpProviderModels, qc, t, isProviderDirty, onSaveBeforeRefresh],
	);

	return (
		<Stack>
			<Text size="sm" c="dimmed">
				{t("nkpProvidersSectionDesc")}
			</Text>

			{providers.map((p, idx) => {
				const pModels = providerModelsMap[p.id] ?? [];
				const quota = quotas?.[p.id];
				return (
					<React.Fragment key={p.id}>
						{idx > 0 && <Divider />}
						<Stack gap="xs" style={p.disabled ? { opacity: 0.6 } : undefined}>
							<Group justify="space-between">
								<Group gap="xs">
									<Text fw={500} size="sm">
										{p.name || `Provider #${idx + 1}`}
									</Text>
									{p.disabled && (
										<Badge size="xs" variant="light" color="gray">
											{t("providerDisabled")}
										</Badge>
									)}
									{!p.disabled && pModels.length > 0 && (
										<Badge size="xs" variant="light">
											{t("nkpModelsCount", { count: pModels.length })}
										</Badge>
									)}
									{!p.disabled && nkpQuotaCapability.supported && quota?.quotaBalance != null && (
										<Badge size="xs" variant="light" color="teal">
											${quota.quotaBalance.toFixed(2)}
										</Badge>
									)}
								</Group>
								<Group gap="xs">
									<Switch
										size="xs"
										checked={!p.disabled}
										onChange={() => toggleProviderDisabled(p.id)}
									/>
									<ActionIcon
										color="red"
										variant="subtle"
										size="sm"
										onClick={() => handleRemoveProvider(p.id)}
									>
										<IconTrash size={14} />
									</ActionIcon>
								</Group>
							</Group>

							<TextInput
								label={t("nkpProviderName")}
								placeholder={t("nkpProviderNamePlaceholder")}
								value={p.name}
								size="xs"
								onChange={(e) => handleNameChange(p.id, e.currentTarget.value)}
							/>
							<TextInput
								label={t("nkpProviderPrefix")}
								description={t("nkpProviderPrefixDesc")}
								placeholder={t("nkpProviderPrefixPlaceholder")}
								value={p.prefix}
								size="xs"
								error={getPrefixError?.(p.prefix, p.id)}
								onChange={(e) => handlePrefixChange(p.id, e.currentTarget.value)}
							/>
							<PasswordInput
								label={t("nkpApiKey")}
								placeholder={t("nkpApiKeyPlaceholder")}
								value={p.apiKey}
								size="xs"
								autoComplete="off"
								onChange={(e) => updateProvider(p.id, "apiKey", e.currentTarget.value)}
							/>
							<TextInput
								label={t("nkpBaseUrl")}
								placeholder={t("nkpBaseUrlPlaceholder")}
								value={p.baseUrl}
								size="xs"
								onChange={(e) => updateProvider(p.id, "baseUrl", e.currentTarget.value)}
							/>
							<Group gap="xs">
								<Button
									size="xs"
									variant="light"
									leftSection={<IconRefresh size={14} />}
									loading={refreshingProvider === p.id}
									disabled={!p.apiKey || !p.baseUrl || !canRefreshNkpProviderModels}
									title={!canRefreshNkpProviderModels ? nkpRefreshUnsupportedReason : undefined}
									onClick={() => handleRefreshProviderModels(p.id)}
								>
									{refreshingProvider === p.id
										? t("nkpRefreshModelsLoading")
										: t("nkpRefreshModels")}
								</Button>
								{pModels.length > 0 && (
									<Text size="xs" c="dimmed">
										{t("nkpModelsCount", { count: pModels.length })}
									</Text>
								)}
							</Group>
							{pModels.length > 0 && (
								<ModelList
									models={pModels}
									hiddenModels={hiddenModels}
									onToggleHidden={onToggleHidden}
									modelContextWindows={modelContextWindows}
									onContextWindowChange={onContextWindowChange}
									onTestModel={onTestModel}
								/>
							)}
							<InlineCustomModels
								prefix={p.prefix || "nkp"}
								customModels={customModels}
								onCustomModelsChange={onCustomModelsChange}
								hiddenModels={hiddenModels}
								onToggleHidden={onToggleHidden}
								modelContextWindows={modelContextWindows}
								onContextWindowChange={onContextWindowChange}
								onTestModel={onTestModel}
							/>
						</Stack>
					</React.Fragment>
				);
			})}
		</Stack>
	);
});
