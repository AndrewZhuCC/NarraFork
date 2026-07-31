import { MOBILE_VIEWPORT_MEDIA_QUERY } from "@frontend/lib/responsive";
import {
	ActionIcon,
	Alert,
	Badge,
	Button,
	Divider,
	Group,
	Paper,
	Progress,
	SegmentedControl,
	Stack,
	Switch,
	Table,
	Text,
	Textarea,
	Tooltip,
} from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconRefresh, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import React, { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	useProviderModelRefreshCapability,
	useProviderRouteCapability,
	useProviderRuntimeCapability,
} from "../../hooks/usePlatform";
import { api, type KiroCredentialPatch } from "../../lib/api";
import { type ModelOption, modelValue } from "../../lib/constants";
import type { ProxyOverride } from "../../lib/proxy";
import { relativeTime } from "../../lib/relative-time";
import { useConfirmDialog } from "../common/ConfirmDialogProvider";
import { ProxyOverrideField } from "../common/ProxyOverrideField";
import type { CustomModelEntry } from "./InlineCustomModels";
import { InlineCustomModels } from "./InlineCustomModels";
import {
	KiroCredentialEditModal,
	type KiroCredentialSnapshotView,
} from "./KiroCredentialEditModal";
import { ModelList } from "./ModelList";
import {
	classifyKiroTier,
	isKiroApiKeyAuthMethod,
	type KiroAccountTier,
	TIER_CONFIG,
} from "./types";

/** Whether a credential snapshot entry is a static Kiro API key credential. */
// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
function isApiKeyEntry(entry: any): boolean {
	return entry?.isApiKey === true || isKiroApiKeyAuthMethod(entry?.authMethod);
}

/**
 * Static Kiro API keys in pasted text. Mirrors the server-side pattern in
 * `server/lib/kiro-import-text.ts` so the UI can validate before posting.
 */
const KIRO_API_KEY_PATTERN = /ksk[_-][A-Za-z0-9._-]+/;

/** Whether pasted text carries at least one static Kiro API key. */
function containsKiroApiKey(text: string): boolean {
	return KIRO_API_KEY_PATTERN.test(text);
}

const KIRO_STATUS_GC_TIME_MS = 60_000;

/** Format a number to exactly 2 decimal places. */
function fmtNum(n: number): string {
	return n.toFixed(2);
}

interface KiroSectionProps {
	// biome-ignore lint/suspicious/noExplicitAny: dynamic settings JSON
	settings: any;
	hiddenModels: Set<string>;
	onToggleHidden: (modelVal: string) => void;
	customModels: CustomModelEntry[];
	onCustomModelsChange: (models: CustomModelEntry[]) => void;
	modelContextWindows: Record<string, number>;
	onContextWindowChange: (modelVal: string, size: number | null) => void;
	onTestModel?: (model: string) => void;
}

export const KiroSection = React.memo(function KiroSection({
	settings,
	hiddenModels,
	onToggleHidden,
	customModels,
	onCustomModelsChange,
	modelContextWindows,
	onContextWindowChange,
	onTestModel,
}: KiroSectionProps) {
	const { t } = useTranslation("settings");
	const confirm = useConfirmDialog();
	const qc = useQueryClient();
	const kiroRefreshCapability = useProviderModelRefreshCapability("kiro");
	const kiroRuntimeCapability = useProviderRuntimeCapability("kiro");
	const kiroStatusRoute = useProviderRouteCapability("kiro", "status");
	const kiroLoadBalancingRoute = useProviderRouteCapability("kiro", "loadBalancingMode");
	const kiroCredentialsImportRoute = useProviderRouteCapability("kiro", "credentialsImport");
	const kiroCredentialDisableRoute = useProviderRouteCapability("kiro", "credentialDisable");
	const kiroCredentialEnableRoute = useProviderRouteCapability("kiro", "credentialEnable");
	const kiroCredentialResetRoute = useProviderRouteCapability("kiro", "credentialReset");
	const kiroCredentialPriorityRoute = useProviderRouteCapability("kiro", "credentialPriority");
	const kiroCredentialRefreshRoute = useProviderRouteCapability("kiro", "credentialRefresh");
	const kiroCredentialUsageRoute = useProviderRouteCapability("kiro", "credentialUsage");
	const kiroCredentialUpdateRoute = useProviderRouteCapability("kiro", "credentialUpdate");
	const kiroCredentialDeleteRoute = useProviderRouteCapability("kiro", "credentialDelete");
	const kiroModelsRefreshRoute = useProviderRouteCapability("kiro", "modelsRefresh");
	const providerRouteUnsupportedReason = t("providerRouteUnsupported");
	const kiroPluginBridge = kiroRuntimeCapability?.pluginBridge;
	const showKiroPluginBridgeRequirement =
		kiroPluginBridge?.supported !== false && kiroPluginBridge?.requiresBun === true;
	const kiroPluginBridgeRequirementReason =
		kiroPluginBridge?.reason ?? t("kiroPluginBridgeRequiresBunDesc");
	const kiroRefreshReason = kiroRefreshCapability.reason ?? t("providerRefreshModelsUnsupported");
	const canReadKiroStatus = kiroStatusRoute.supported;
	const canSetKiroLoadBalancingMode = kiroLoadBalancingRoute.supported;
	const canImportKiroCredentials = kiroCredentialsImportRoute.supported;
	const canDisableKiroCredential = kiroCredentialDisableRoute.supported;
	const canEnableKiroCredential = kiroCredentialEnableRoute.supported;
	const canResetKiroCredential = kiroCredentialResetRoute.supported;
	const canSetKiroCredentialPriority = kiroCredentialPriorityRoute.supported;
	const canRefreshKiroCredential = kiroCredentialRefreshRoute.supported;
	const canQueryKiroCredentialUsage = kiroCredentialUsageRoute.supported;
	const canUpdateKiroCredential = kiroCredentialUpdateRoute.supported;
	const canDeleteKiroCredential = kiroCredentialDeleteRoute.supported;
	const canRefreshKiroModels = kiroRefreshCapability.supported && kiroModelsRefreshRoute.supported;
	const kiroRefreshUnsupportedReason = kiroRefreshCapability.supported
		? (kiroModelsRefreshRoute.reason ?? providerRouteUnsupportedReason)
		: kiroRefreshReason;
	const [refreshingKiro, setRefreshingKiro] = useState(false);

	// --- Kiro credential state ---
	const [importJson, setImportJson] = useState("");
	const [importError, setImportError] = useState<string | null>(null);
	const [importResult, setImportResult] = useState<string | null>(null);

	const { data: kiroStatus } = useQuery({
		queryKey: ["kiro", "status"],
		queryFn: api.kiroStatus,
		enabled: canReadKiroStatus,
		refetchInterval: 30_000,
		gcTime: KIRO_STATUS_GC_TIME_MS,
	});

	const snapshot = kiroStatus?.snapshot;
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	const usageCache: Record<string, any> = (kiroStatus as any)?.usageCache ?? {};
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	const entries: any[] = snapshot?.entries ?? [];

	// --- Credential mutations ---
	const disableMut = useMutation({
		mutationFn: (id: string) => api.kiroDisableCredential(id),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
	});
	const enableMut = useMutation({
		mutationFn: (id: string) => api.kiroEnableCredential(id),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
	});
	const resetMut = useMutation({
		mutationFn: (id: string) => api.kiroResetCredential(id),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
	});
	const refreshMut = useMutation({
		mutationFn: (id: string) => api.kiroRefreshCredential(id),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
	});
	const usageMut = useMutation({
		mutationFn: (id: string) => api.kiroGetUsage(id),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
	});
	const deleteMut = useMutation({
		mutationFn: (id: string) => api.kiroDeleteCredential(id),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
	});
	const [editingCredential, setEditingCredential] = useState<KiroCredentialSnapshotView | null>(
		null,
	);
	const [editError, setEditError] = useState<string | null>(null);
	const updateMut = useMutation({
		mutationFn: ({ id, fields }: { id: string; fields: KiroCredentialPatch }) =>
			api.kiroUpdateCredential(id, fields),
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["kiro", "status"] });
			setEditingCredential(null);
			setEditError(null);
		},
		onError: (err: Error) => setEditError(err.message),
	});
	const priorityMut = useMutation({
		mutationFn: ({ id, priority }: { id: string; priority: number }) =>
			api.kiroSetPriority(id, priority),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
		onError: (err: Error) => setEditError(err.message),
	});
	const lbModeMut = useMutation({
		mutationFn: (mode: "priority" | "balanced") => api.kiroSetLoadBalancingMode(mode),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
	});
	const kiroProxy = settings?.kiro?.proxy as ProxyOverride | undefined;
	const proxyMut = useMutation({
		mutationFn: (proxy: ProxyOverride | undefined) => api.updateSettings({ kiro: { proxy } }),
		onSuccess: () => {
			qc.invalidateQueries({ queryKey: ["admin", "settings"] });
			notifications.show({ message: t("proxySaved"), color: "green" });
		},
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

	const handleImport = useCallback(() => {
		if (!canImportKiroCredentials) return;
		setImportError(null);
		setImportResult(null);
		const text = importJson.trim();
		if (!text) return;
		try {
			const parsed = JSON.parse(text);
			importMut.mutate(Array.isArray(parsed) ? parsed : [parsed]);
			return;
		} catch {
			// Not JSON — fall back to plain-text "ksk_..." key lists, which the
			// server parses per line/comma.
		}
		if (!containsKiroApiKey(text)) {
			setImportError(t("kiroImportInvalidJson"));
			return;
		}
		importMut.mutate([text]);
	}, [importJson, importMut, t, canImportKiroCredentials]);

	const handleEditCredential = useCallback((entry: KiroCredentialSnapshotView) => {
		setEditError(null);
		setEditingCredential(entry);
	}, []);

	/**
	 * Apply a credential patch.
	 *
	 * `priority` has its own route (separately gated by runtime capabilities), so
	 * it is split out of the PATCH body and only sent when it actually changed.
	 */
	const handleSubmitEdit = useCallback(
		(patch: KiroCredentialPatch) => {
			const entry = editingCredential;
			if (!entry || !canUpdateKiroCredential) return;
			const { priority, ...fields } = patch;
			if (priority !== undefined && canSetKiroCredentialPriority) {
				priorityMut.mutate({ id: entry.id, priority });
			}
			if (Object.keys(fields).length > 0) {
				updateMut.mutate({ id: entry.id, fields });
				return;
			}
			// Priority-only edits (or a no-op save) close the modal directly since the
			// PATCH mutation that normally closes it never ran.
			setEditingCredential(null);
			setEditError(null);
		},
		[
			editingCredential,
			canUpdateKiroCredential,
			canSetKiroCredentialPriority,
			priorityMut,
			updateMut,
		],
	);

	const handleRefreshKiro = useCallback(async () => {
		if (!canRefreshKiroModels) return;
		setRefreshingKiro(true);
		try {
			await api.kiroRefreshModels();
			qc.invalidateQueries({ queryKey: ["admin", "settings"] });
			qc.invalidateQueries({ queryKey: ["settings"] });
			qc.invalidateQueries({ queryKey: ["kiro", "models"] });
		} catch (err) {
			notifications.show({
				color: "red",
				title: t("openaiRefreshModelsError"),
				message: err instanceof Error ? err.message : String(err),
			});
		} finally {
			setRefreshingKiro(false);
		}
	}, [canRefreshKiroModels, qc, t]);

	// --- Build kiro models list ---
	const kiroModels: ModelOption[] = settings?.kiroModels?.length
		? settings.kiroModels
				// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
				.map((m: any) => {
					const id = String(m.model_id ?? m.modelId ?? "");
					return {
						value: modelValue("kiro", id),
						label: String(
							m.model_short_name ?? m.modelShortName ?? m.model_name ?? m.modelName ?? id,
						),
						provider: "kiro" as const,
					};
				})
				.filter((m: ModelOption) => m.value !== "kiro:")
		: [];

	// --- Group credentials by account tier ---
	const tierGroups: Record<KiroAccountTier, typeof entries> = {
		free: [],
		paid: [],
		enterprise: [],
	};
	for (const entry of entries) {
		const usage = usageCache[String(entry.id)];
		const tier = classifyKiroTier({
			authMethod: entry.authMethod,
			hasProfileArn: entry.hasProfileArn,
			hasStartUrl: entry.hasStartUrl,
			subscriptionTitle: usage?.subscriptionTitle ?? entry.subscriptionTitle,
		});
		tierGroups[tier].push(entry);
	}

	return (
		<Stack gap="md">
			<Group justify="space-between">
				<Text fw={500} size="sm">
					{t("kiroSection")}
				</Text>
				<Badge size="sm" color={snapshot && snapshot.available > 0 ? "green" : "gray"}>
					{snapshot?.available ?? 0} / {snapshot?.total ?? 0}
				</Badge>
			</Group>

			<Text size="xs" c="dimmed">
				{t("kiroSectionDesc")}
			</Text>

			{showKiroPluginBridgeRequirement && (
				<Alert color="blue" variant="light" title={t("kiroPluginBridgeRequiresBunTitle")}>
					{kiroPluginBridgeRequirementReason}
				</Alert>
			)}

			{/* Proxy override */}
			<ProxyOverrideField
				value={kiroProxy}
				onChange={(next) => proxyMut.mutate(next)}
				disabled={proxyMut.isPending}
			/>

			{/* Load balancing mode */}
			{snapshot && (
				<Group gap="xs" align="center">
					<Text size="sm">{t("kiroLoadBalancing")}:</Text>
					<SegmentedControl
						size="xs"
						value={snapshot.loadBalancingMode}
						disabled={!canSetKiroLoadBalancingMode}
						onChange={(v) =>
							canSetKiroLoadBalancingMode && lbModeMut.mutate(v as "priority" | "balanced")
						}
						data={[
							{ value: "priority", label: t("kiroModePriority") },
							{ value: "balanced", label: t("kiroModeBalanced") },
						]}
					/>
				</Group>
			)}

			{/* === Credentials Sub-section === */}
			<Divider />
			<Group gap="xs">
				<Text fw={500} size="sm">
					{t("kiroCredentialsSection")}
				</Text>
				{entries.length > 0 && (
					<Badge size="xs" variant="light">
						{entries.length}
					</Badge>
				)}
			</Group>

			{/* Import area */}
			<Textarea
				size="xs"
				placeholder={t("kiroImportPlaceholder")}
				value={importJson}
				onChange={(e) => setImportJson(e.currentTarget.value)}
				minRows={2}
				autosize
				styles={{ input: { fontFamily: "monospace", fontSize: 12 } }}
			/>
			<Text size="xs" c="dimmed">
				{t("kiroImportApiKeyHint")}
			</Text>
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
				disabled={!importJson.trim() || !canImportKiroCredentials}
				loading={importMut.isPending}
				size="xs"
				variant="light"
				style={{ alignSelf: "flex-start" }}
				title={!canImportKiroCredentials ? providerRouteUnsupportedReason : undefined}
			>
				{t("kiroImportBtn")}
			</Button>

			{entries.length === 0 && (
				<Text c="dimmed" size="sm">
					{t("kiroNoCredentials")}
				</Text>
			)}

			{/* Credentials grouped by tier */}
			{(["paid", "enterprise", "free"] as const).map((tier) => {
				const group = tierGroups[tier];
				if (group.length === 0) return null;
				const cfg = TIER_CONFIG[tier];
				return (
					<Stack key={tier} gap="xs">
						<Group gap="xs">
							<Badge color={cfg.color} size="sm" variant="filled">
								{t(cfg.i18nKey)}
							</Badge>
							<Text size="xs" c="dimmed">
								{t(cfg.descKey)} · {group.length}
							</Text>
						</Group>
						<CredentialList
							entries={group}
							snapshot={snapshot}
							usageCache={usageCache}
							disableMut={disableMut}
							enableMut={enableMut}
							resetMut={resetMut}
							refreshMut={refreshMut}
							usageMut={usageMut}
							deleteMut={deleteMut}
							onEditCredential={handleEditCredential}
							canDisableKiroCredential={canDisableKiroCredential}
							canEnableKiroCredential={canEnableKiroCredential}
							canResetKiroCredential={canResetKiroCredential}
							canRefreshKiroCredential={canRefreshKiroCredential}
							canQueryKiroCredentialUsage={canQueryKiroCredentialUsage}
							canUpdateKiroCredential={canUpdateKiroCredential}
							canDeleteKiroCredential={canDeleteKiroCredential}
							credentialRouteUnsupportedReason={providerRouteUnsupportedReason}
						/>
					</Stack>
				);
			})}

			{/* === Models Sub-section === */}
			<Divider />
			<Group justify="space-between">
				<Text fw={500} size="sm">
					{t("kiroModelsCount", { count: kiroModels.length })}
				</Text>
				<Button
					size="xs"
					variant="light"
					leftSection={<IconRefresh size={14} />}
					loading={refreshingKiro}
					disabled={!canRefreshKiroModels}
					title={!canRefreshKiroModels ? kiroRefreshUnsupportedReason : undefined}
					onClick={async () => {
						if (await confirm({ message: t("kiroRefreshModelsConfirm"), confirmColor: "indigo" })) {
							handleRefreshKiro();
						}
					}}
				>
					{t("kiroRefreshModels")}
				</Button>
			</Group>

			{kiroModels.length > 0 && (
				<ModelList
					models={kiroModels}
					hiddenModels={hiddenModels}
					onToggleHidden={onToggleHidden}
					modelContextWindows={modelContextWindows}
					onContextWindowChange={onContextWindowChange}
					onTestModel={onTestModel}
					showContextWindow={false}
				/>
			)}
			<InlineCustomModels
				prefix="kiro"
				customModels={customModels}
				onCustomModelsChange={onCustomModelsChange}
				hiddenModels={hiddenModels}
				onToggleHidden={onToggleHidden}
				modelContextWindows={modelContextWindows}
				onContextWindowChange={onContextWindowChange}
				onTestModel={onTestModel}
			/>

			<KiroCredentialEditModal
				entry={editingCredential}
				opened={editingCredential != null}
				onClose={() => {
					setEditingCredential(null);
					setEditError(null);
				}}
				onSubmit={handleSubmitEdit}
				loading={updateMut.isPending || priorityMut.isPending}
				error={editError}
			/>
		</Stack>
	);
});

// --- Credential list (responsive: cards on mobile, table on desktop) ---

interface CredentialListProps {
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	entries: any[];
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	snapshot: any;
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	usageCache: Record<string, any>;
	// biome-ignore lint/suspicious/noExplicitAny: mutation types
	disableMut: any;
	// biome-ignore lint/suspicious/noExplicitAny: mutation types
	enableMut: any;
	// biome-ignore lint/suspicious/noExplicitAny: mutation types
	resetMut: any;
	// biome-ignore lint/suspicious/noExplicitAny: mutation types
	refreshMut: any;
	// biome-ignore lint/suspicious/noExplicitAny: mutation types
	usageMut: any;
	// biome-ignore lint/suspicious/noExplicitAny: mutation types
	deleteMut: any;
	onEditCredential: (entry: KiroCredentialSnapshotView) => void;
	canDisableKiroCredential: boolean;
	canEnableKiroCredential: boolean;
	canResetKiroCredential: boolean;
	canRefreshKiroCredential: boolean;
	canQueryKiroCredentialUsage: boolean;
	canUpdateKiroCredential: boolean;
	canDeleteKiroCredential: boolean;
	credentialRouteUnsupportedReason: string;
}

function CredentialList(props: CredentialListProps) {
	const isMobile = useMediaQuery(MOBILE_VIEWPORT_MEDIA_QUERY);
	return isMobile ? <CredentialCards {...props} /> : <CredentialTable {...props} />;
}

/**
 * Compact region summary: base region plus any auth/api overrides.
 *
 * All three are shown because they resolve independently — `authRegion` overrides
 * the token-refresh endpoint while `apiRegion` overrides the data-plane endpoint.
 */
function formatRegions(entry: {
	region?: string;
	authRegion?: string;
	apiRegion?: string;
}): string {
	const parts: string[] = [];
	if (entry.region) parts.push(entry.region);
	if (entry.authRegion) parts.push(`auth ${entry.authRegion}`);
	if (entry.apiRegion) parts.push(`api ${entry.apiRegion}`);
	return parts.length > 0 ? parts.join(" · ") : "-";
}

function CredentialActions({
	entry,
	...muts
}: {
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	entry: any;
} & Pick<
	CredentialListProps,
	| "disableMut"
	| "enableMut"
	| "resetMut"
	| "refreshMut"
	| "usageMut"
	| "deleteMut"
	| "onEditCredential"
	| "canDisableKiroCredential"
	| "canEnableKiroCredential"
	| "canResetKiroCredential"
	| "canRefreshKiroCredential"
	| "canQueryKiroCredentialUsage"
	| "canUpdateKiroCredential"
	| "canDeleteKiroCredential"
	| "credentialRouteUnsupportedReason"
>) {
	const { t } = useTranslation("settings");
	const confirm = useConfirmDialog();
	const canToggleEnabled = entry.disabled
		? muts.canEnableKiroCredential
		: muts.canDisableKiroCredential;
	// Static API keys are long-lived bearer tokens — there is nothing to refresh.
	const isApiKey = isApiKeyEntry(entry);
	const canRefresh = muts.canRefreshKiroCredential && !isApiKey;
	const refreshDisabledReason = isApiKey
		? t("kiroApiKeyNoRefresh")
		: muts.credentialRouteUnsupportedReason;
	return (
		<Group gap="xs" wrap="wrap">
			<Switch
				size="xs"
				checked={!entry.disabled}
				disabled={!canToggleEnabled}
				title={!canToggleEnabled ? muts.credentialRouteUnsupportedReason : undefined}
				onChange={() => {
					if (!canToggleEnabled) return;
					entry.disabled ? muts.enableMut.mutate(entry.id) : muts.disableMut.mutate(entry.id);
				}}
			/>
			<Tooltip label={isApiKey ? refreshDisabledReason : t("kiroRefreshToken")}>
				<ActionIcon
					variant="subtle"
					size="sm"
					onClick={() => canRefresh && muts.refreshMut.mutate(entry.id)}
					loading={muts.refreshMut.isPending}
					disabled={!canRefresh}
					title={!canRefresh ? refreshDisabledReason : undefined}
				>
					<IconRefresh size={14} />
				</ActionIcon>
			</Tooltip>
			<Button
				variant="subtle"
				size="compact-xs"
				onClick={() => muts.canQueryKiroCredentialUsage && muts.usageMut.mutate(entry.id)}
				loading={muts.usageMut.isPending}
				disabled={!muts.canQueryKiroCredentialUsage}
				title={
					!muts.canQueryKiroCredentialUsage ? muts.credentialRouteUnsupportedReason : undefined
				}
			>
				{t("kiroQueryUsage")}
			</Button>
			<Button
				variant="subtle"
				size="compact-xs"
				onClick={() => muts.canUpdateKiroCredential && muts.onEditCredential(entry)}
				disabled={!muts.canUpdateKiroCredential}
				title={!muts.canUpdateKiroCredential ? muts.credentialRouteUnsupportedReason : undefined}
			>
				{t("kiroEdit")}
			</Button>
			<Button
				variant="subtle"
				size="compact-xs"
				color="orange"
				onClick={() => muts.canResetKiroCredential && muts.resetMut.mutate(entry.id)}
				disabled={!muts.canResetKiroCredential}
				title={!muts.canResetKiroCredential ? muts.credentialRouteUnsupportedReason : undefined}
			>
				{t("kiroReset")}
			</Button>
			<ActionIcon
				variant="subtle"
				color="red"
				size="sm"
				onClick={async () => {
					if (!muts.canDeleteKiroCredential) return;
					if (await confirm({ message: t("kiroDeleteConfirm") })) {
						muts.deleteMut.mutate(entry.id);
					}
				}}
				disabled={!muts.canDeleteKiroCredential}
				title={!muts.canDeleteKiroCredential ? muts.credentialRouteUnsupportedReason : undefined}
			>
				<IconTrash size={14} />
			</ActionIcon>
		</Group>
	);
}

// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
function UsageDisplay({ entry, usage }: { entry: any; usage: any }) {
	// meterUsage from credential is the live-updated value
	const currentUsage = entry.meterUsage ?? usage?.currentUsage;
	const usageLimit = usage?.usageLimit;
	const subscriptionTitle = entry.subscriptionTitle ?? usage?.subscriptionTitle;
	const tier = classifyKiroTier({
		authMethod: entry.authMethod,
		hasProfileArn: entry.hasProfileArn,
		hasStartUrl: entry.hasStartUrl,
		subscriptionTitle,
	});
	const tierCfg = TIER_CONFIG[tier];

	const pct = usageLimit && usageLimit > 0 ? (currentUsage / usageLimit) * 100 : null;

	return (
		<Stack gap={2}>
			<Group gap={4}>
				<Badge size="xs" variant="light" color={tierCfg.color}>
					{subscriptionTitle || tier}
				</Badge>
			</Group>
			{currentUsage != null ? (
				<>
					<Text size="xs">
						{fmtNum(currentUsage)}
						{usageLimit != null ? ` / ${fmtNum(usageLimit)}` : ""}
					</Text>
					{pct != null && (
						<Progress
							value={Math.min(pct, 100)}
							size="xs"
							color={pct > 90 ? "red" : pct > 70 ? "yellow" : "blue"}
							style={{ width: 80 }}
						/>
					)}
				</>
			) : (
				<Text size="xs" c="dimmed">
					-
				</Text>
			)}
			{usage?.queriedAt && (
				<Text size="xs" c="dimmed">
					{relativeTime(usage.queriedAt)}
				</Text>
			)}
		</Stack>
	);
}

// --- Mobile: card layout ---

function CredentialCards({ entries, snapshot, usageCache, ...muts }: CredentialListProps) {
	return (
		<Stack gap="xs">
			{/* biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure */}
			{entries.map((entry: any) => (
				<CredentialCard
					key={entry.id}
					entry={entry}
					snapshot={snapshot}
					usage={usageCache[String(entry.id)]}
					{...muts}
				/>
			))}
		</Stack>
	);
}

function CredentialCard({
	entry,
	snapshot,
	usage,
	...muts
}: {
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	entry: any;
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	snapshot: any;
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	usage: any;
} & Pick<
	CredentialListProps,
	| "disableMut"
	| "enableMut"
	| "resetMut"
	| "refreshMut"
	| "usageMut"
	| "deleteMut"
	| "onEditCredential"
	| "canDisableKiroCredential"
	| "canEnableKiroCredential"
	| "canResetKiroCredential"
	| "canRefreshKiroCredential"
	| "canQueryKiroCredentialUsage"
	| "canUpdateKiroCredential"
	| "canDeleteKiroCredential"
	| "credentialRouteUnsupportedReason"
>) {
	const { t } = useTranslation("settings");
	const isCurrent = entry.id === snapshot?.currentId;
	const displayLabel = entry.displayName || entry.email || entry.id;

	return (
		<Paper
			withBorder
			p="sm"
			style={
				isCurrent ? { borderColor: "var(--mantine-color-indigo-5)", borderWidth: 2 } : undefined
			}
		>
			<Stack gap="xs">
				{/* Row 1: ID + Display name + Status */}
				<Group justify="space-between" wrap="wrap">
					<Group gap="xs">
						<Text size="xs" c="dimmed" ff="monospace">
							{entry.id}
						</Text>
						<Text size="sm" fw={isCurrent ? 700 : 500} truncate style={{ maxWidth: 200 }}>
							{displayLabel}
						</Text>
					</Group>
					<Group gap="xs">
						{isApiKeyEntry(entry) && (
							<Badge color="teal" size="sm" variant="light">
								{t("kiroApiKeyBadge")}
							</Badge>
						)}
						{entry.disabled ? (
							<Badge color="red" size="sm">
								{entry.disabledReason ?? "disabled"}
							</Badge>
						) : (
							<Badge color="green" size="sm">
								enabled
							</Badge>
						)}
					</Group>
				</Group>

				{/* Row 2: Details */}
				<Stack gap={4}>
					{entry.email && (
						<Group justify="space-between" wrap="nowrap">
							<Text size="xs" c="dimmed">
								Email
							</Text>
							<Text size="xs">{entry.email}</Text>
						</Group>
					)}
					<Group justify="space-between" wrap="nowrap">
						<Text size="xs" c="dimmed">
							{t("kiroEditRegion")}
						</Text>
						<Text size="xs">{formatRegions(entry)}</Text>
					</Group>
					<Group justify="space-between" wrap="nowrap">
						<Text size="xs" c="dimmed">
							{t("kiroColPriority")}
						</Text>
						<Text size="xs">{entry.priority}</Text>
					</Group>
					<Group justify="space-between" wrap="nowrap">
						<Text size="xs" c="dimmed">
							{t("kiroColExpires")}
						</Text>
						<Text size="xs" c="dimmed">
							{isApiKeyEntry(entry)
								? t("kiroApiKeyNeverExpires")
								: entry.expiresAt
									? relativeTime(entry.expiresAt)
									: "-"}
						</Text>
					</Group>
					<Group justify="space-between" wrap="nowrap">
						<Text size="xs" c="dimmed">
							{t("kiroColStats")}
						</Text>
						<Text size="xs">
							{t("kiroSuccess")}: {entry.successCount} / {t("kiroFail")}: {entry.failureCount}
						</Text>
					</Group>
					<Group justify="space-between" wrap="nowrap">
						<Text size="xs" c="dimmed">
							{t("kiroColLastUsed")}
						</Text>
						<Text size="xs" c="dimmed">
							{entry.lastUsedAt ? relativeTime(entry.lastUsedAt) : "-"}
						</Text>
					</Group>
				</Stack>

				{/* Row 3: Usage */}
				<Stack gap={4}>
					<Text size="xs" c="dimmed">
						{t("kiroColUsage")}
					</Text>
					<UsageDisplay entry={entry} usage={usage} />
				</Stack>

				{/* Row 4: Actions */}
				<CredentialActions entry={entry} {...muts} />
			</Stack>
		</Paper>
	);
}

// --- Desktop: table layout ---

function CredentialTable({ entries, snapshot, usageCache, ...muts }: CredentialListProps) {
	const { t } = useTranslation("settings");

	return (
		<Table striped highlightOnHover>
			<Table.Thead>
				<Table.Tr>
					<Table.Th>ID</Table.Th>
					<Table.Th>{t("kiroEditDisplayName")}</Table.Th>
					<Table.Th>Email</Table.Th>
					<Table.Th>{t("kiroColPriority")}</Table.Th>
					<Table.Th>{t("kiroEditRegion")}</Table.Th>
					<Table.Th>{t("kiroColStatus")}</Table.Th>
					<Table.Th>{t("kiroColExpires")}</Table.Th>
					<Table.Th>{t("kiroColStats")}</Table.Th>
					<Table.Th>{t("kiroColUsage")}</Table.Th>
					<Table.Th>{t("kiroColLastUsed")}</Table.Th>
					<Table.Th>{t("kiroColActions")}</Table.Th>
				</Table.Tr>
			</Table.Thead>
			<Table.Tbody>
				{/* biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure */}
				{entries.map((entry: any) => (
					<CredentialTableRow
						key={entry.id}
						entry={entry}
						snapshot={snapshot}
						usage={usageCache[String(entry.id)]}
						{...muts}
					/>
				))}
			</Table.Tbody>
		</Table>
	);
}

function CredentialTableRow({
	entry,
	snapshot,
	usage,
	...muts
}: {
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	entry: any;
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	snapshot: any;
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	usage: any;
} & Pick<
	CredentialListProps,
	| "disableMut"
	| "enableMut"
	| "resetMut"
	| "refreshMut"
	| "usageMut"
	| "deleteMut"
	| "onEditCredential"
	| "canDisableKiroCredential"
	| "canEnableKiroCredential"
	| "canResetKiroCredential"
	| "canRefreshKiroCredential"
	| "canQueryKiroCredentialUsage"
	| "canUpdateKiroCredential"
	| "canDeleteKiroCredential"
	| "credentialRouteUnsupportedReason"
>) {
	const { t } = useTranslation("settings");

	return (
		<Table.Tr>
			<Table.Td>
				<Text size="xs" ff="monospace" fw={entry.id === snapshot?.currentId ? 700 : 400}>
					{entry.id}
				</Text>
			</Table.Td>
			<Table.Td>
				<Text size="sm">{entry.displayName || "-"}</Text>
			</Table.Td>
			<Table.Td>
				<Text size="sm">{entry.email ?? "-"}</Text>
			</Table.Td>
			<Table.Td>
				<Text size="sm">{entry.priority}</Text>
			</Table.Td>
			<Table.Td>
				<Text size="xs" c="dimmed">
					{formatRegions(entry)}
				</Text>
			</Table.Td>
			<Table.Td>
				<Group gap={4} wrap="wrap">
					{isApiKeyEntry(entry) && (
						<Badge color="teal" size="sm" variant="light">
							{t("kiroApiKeyBadge")}
						</Badge>
					)}
					{entry.disabled ? (
						<Badge color="red" size="sm">
							{entry.disabledReason ?? "disabled"}
						</Badge>
					) : (
						<Badge color="green" size="sm">
							enabled
						</Badge>
					)}
				</Group>
			</Table.Td>
			<Table.Td>
				<Text size="xs" c="dimmed">
					{isApiKeyEntry(entry)
						? t("kiroApiKeyNeverExpires")
						: entry.expiresAt
							? relativeTime(entry.expiresAt)
							: "-"}
				</Text>
			</Table.Td>
			<Table.Td>
				<Text size="xs">
					{t("kiroSuccess")}: {entry.successCount} / {t("kiroFail")}: {entry.failureCount}
				</Text>
			</Table.Td>
			<Table.Td>
				<UsageDisplay entry={entry} usage={usage} />
			</Table.Td>
			<Table.Td>
				<Text size="xs" c="dimmed">
					{entry.lastUsedAt ? relativeTime(entry.lastUsedAt) : "-"}
				</Text>
			</Table.Td>
			<Table.Td>
				<CredentialActions entry={entry} {...muts} />
			</Table.Td>
		</Table.Tr>
	);
}
