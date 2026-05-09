import {
	ActionIcon,
	Badge,
	Button,
	Divider,
	Group,
	NumberInput,
	Paper,
	Progress,
	SegmentedControl,
	Stack,
	Switch,
	Table,
	Text,
	Textarea,
	TextInput,
	Tooltip,
} from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import { notifications } from "@mantine/notifications";
import { IconDeviceFloppy, IconRefresh, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import React, { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "../../lib/api";
import { type ModelOption, modelValue } from "../../lib/constants";
import { relativeTime } from "../../lib/relative-time";
import { useConfirmDialog } from "../common/ConfirmDialogProvider";
import type { CustomModelEntry } from "./InlineCustomModels";
import { InlineCustomModels } from "./InlineCustomModels";
import { ModelList } from "./ModelList";
import { classifyKiroTier, type KiroAccountTier, TIER_CONFIG } from "./types";

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
	const [refreshingKiro, setRefreshingKiro] = useState(false);

	// --- Kiro credential state ---
	const [importJson, setImportJson] = useState("");
	const [importError, setImportError] = useState<string | null>(null);
	const [importResult, setImportResult] = useState<string | null>(null);

	const { data: kiroStatus } = useQuery({
		queryKey: ["kiro", "status"],
		queryFn: api.kiroStatus,
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
	const updateMut = useMutation({
		mutationFn: ({
			id,
			fields,
		}: {
			id: string;
			fields: { email?: string; displayName?: string; region?: string };
		}) => api.kiroUpdateCredential(id, fields),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
	});
	const lbModeMut = useMutation({
		mutationFn: (mode: "priority" | "balanced") => api.kiroSetLoadBalancingMode(mode),
		onSuccess: () => qc.invalidateQueries({ queryKey: ["kiro", "status"] }),
	});
	const priorityMut = useMutation({
		mutationFn: ({ id, priority }: { id: string; priority: number }) =>
			api.kiroSetPriority(id, priority),
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

	const handleImport = useCallback(() => {
		setImportError(null);
		setImportResult(null);
		try {
			const parsed = JSON.parse(importJson);
			const arr = Array.isArray(parsed) ? parsed : [parsed];
			importMut.mutate(arr);
		} catch {
			setImportError(t("kiroImportInvalidJson"));
		}
	}, [importJson, importMut, t]);

	const handleRefreshKiro = useCallback(async () => {
		setRefreshingKiro(true);
		try {
			await api.kiroRefreshModels();
			qc.invalidateQueries({ queryKey: ["admin", "settings"] });
			qc.invalidateQueries({ queryKey: ["settings"] });
			qc.invalidateQueries({ queryKey: ["kiro", "models"] });
		} catch {
			notifications.show({ color: "red", title: t("openaiRefreshModelsError"), message: "" });
		} finally {
			setRefreshingKiro(false);
		}
	}, [qc, t]);

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

			{/* Load balancing mode */}
			{snapshot && (
				<Group gap="xs" align="center">
					<Text size="sm">{t("kiroLoadBalancing")}:</Text>
					<SegmentedControl
						size="xs"
						value={snapshot.loadBalancingMode}
						onChange={(v) => lbModeMut.mutate(v as "priority" | "balanced")}
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
				size="xs"
				variant="light"
				style={{ alignSelf: "flex-start" }}
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
							updateMut={updateMut}
							priorityMut={priorityMut}
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
	// biome-ignore lint/suspicious/noExplicitAny: mutation types
	updateMut: any;
	// biome-ignore lint/suspicious/noExplicitAny: mutation types
	priorityMut: any;
}

function CredentialList(props: CredentialListProps) {
	const isMobile = useMediaQuery("(max-width: 768px)");
	return isMobile ? <CredentialCards {...props} /> : <CredentialTable {...props} />;
}

function CredentialActions({
	entry,
	editing,
	onEdit,
	...muts
}: {
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	entry: any;
	editing?: boolean;
	onEdit?: () => void;
} & Pick<
	CredentialListProps,
	"disableMut" | "enableMut" | "resetMut" | "refreshMut" | "usageMut" | "deleteMut" | "updateMut"
>) {
	const { t } = useTranslation("settings");
	const confirm = useConfirmDialog();
	return (
		<Group gap="xs" wrap="wrap">
			<Switch
				size="xs"
				checked={!entry.disabled}
				onChange={() =>
					entry.disabled ? muts.enableMut.mutate(entry.id) : muts.disableMut.mutate(entry.id)
				}
			/>
			<Tooltip label={t("kiroRefreshToken")}>
				<ActionIcon
					variant="subtle"
					size="sm"
					onClick={() => muts.refreshMut.mutate(entry.id)}
					loading={muts.refreshMut.isPending}
				>
					<IconRefresh size={14} />
				</ActionIcon>
			</Tooltip>
			<Button
				variant="subtle"
				size="compact-xs"
				onClick={() => muts.usageMut.mutate(entry.id)}
				loading={muts.usageMut.isPending}
			>
				{t("kiroQueryUsage")}
			</Button>
			{onEdit && !editing && (
				<Button variant="subtle" size="compact-xs" onClick={onEdit}>
					{t("kiroEdit")}
				</Button>
			)}
			<Button
				variant="subtle"
				size="compact-xs"
				color="orange"
				onClick={() => muts.resetMut.mutate(entry.id)}
			>
				{t("kiroReset")}
			</Button>
			<ActionIcon
				variant="subtle"
				color="red"
				size="sm"
				onClick={async () => {
					if (await confirm({ message: t("kiroDeleteConfirm") })) {
						muts.deleteMut.mutate(entry.id);
					}
				}}
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
	| "updateMut"
	| "priorityMut"
>) {
	const { t } = useTranslation("settings");
	const [editing, setEditing] = useState(false);
	const [editEmail, setEditEmail] = useState(entry.email ?? "");
	const [editDisplayName, setEditDisplayName] = useState(entry.displayName ?? "");
	const [editRegion, setEditRegion] = useState(entry.region ?? "");
	const [editPriority, setEditPriority] = useState<number>(entry.priority ?? 0);
	const isCurrent = entry.id === snapshot?.currentId;

	const handleSave = () => {
		muts.updateMut.mutate({
			id: entry.id,
			fields: { email: editEmail, displayName: editDisplayName, region: editRegion },
		});
		if (editPriority !== (entry.priority ?? 0)) {
			muts.priorityMut.mutate({ id: entry.id, priority: editPriority });
		}
		setEditing(false);
	};

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

				{/* Inline edit form */}
				{editing ? (
					<Stack gap="xs">
						<TextInput
							size="xs"
							label={t("kiroEditDisplayName")}
							value={editDisplayName}
							onChange={(e) => setEditDisplayName(e.currentTarget.value)}
						/>
						<TextInput
							size="xs"
							label="Email"
							value={editEmail}
							onChange={(e) => setEditEmail(e.currentTarget.value)}
						/>
						<TextInput
							size="xs"
							label={t("kiroEditRegion")}
							value={editRegion}
							onChange={(e) => setEditRegion(e.currentTarget.value)}
						/>
						<NumberInput
							size="xs"
							label={t("kiroColPriority")}
							value={editPriority}
							onChange={(v) => setEditPriority(Number(v) || 0)}
							min={0}
							step={1}
						/>
						<Group gap="xs">
							<Button
								size="compact-xs"
								onClick={handleSave}
								leftSection={<IconDeviceFloppy size={12} />}
							>
								{t("kiroEditSave")}
							</Button>
							<Button size="compact-xs" variant="subtle" onClick={() => setEditing(false)}>
								{t("kiroEditCancel")}
							</Button>
						</Group>
					</Stack>
				) : (
					<>
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
							{entry.region && (
								<Group justify="space-between" wrap="nowrap">
									<Text size="xs" c="dimmed">
										{t("kiroEditRegion")}
									</Text>
									<Text size="xs">{entry.region}</Text>
								</Group>
							)}
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
									{entry.expiresAt ? relativeTime(entry.expiresAt) : "-"}
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
					</>
				)}

				{/* Row 4: Actions */}
				<CredentialActions
					entry={entry}
					editing={editing}
					onEdit={() => setEditing(true)}
					{...muts}
				/>
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
	| "updateMut"
	| "priorityMut"
>) {
	const { t } = useTranslation("settings");
	const [editing, setEditing] = useState(false);
	const [editDisplayName, setEditDisplayName] = useState(entry.displayName ?? "");
	const [editEmail, setEditEmail] = useState(entry.email ?? "");
	const [editRegion, setEditRegion] = useState(entry.region ?? "");
	const [editPriority, setEditPriority] = useState<number>(entry.priority ?? 0);

	const handleSave = () => {
		muts.updateMut.mutate({
			id: entry.id,
			fields: { email: editEmail, displayName: editDisplayName, region: editRegion },
		});
		if (editPriority !== (entry.priority ?? 0)) {
			muts.priorityMut.mutate({ id: entry.id, priority: editPriority });
		}
		setEditing(false);
	};

	if (editing) {
		return (
			<Table.Tr>
				<Table.Td>
					<Text size="xs" ff="monospace">
						{entry.id}
					</Text>
				</Table.Td>
				<Table.Td>
					<TextInput
						size="xs"
						value={editDisplayName}
						onChange={(e) => setEditDisplayName(e.currentTarget.value)}
					/>
				</Table.Td>
				<Table.Td>
					<TextInput
						size="xs"
						value={editEmail}
						onChange={(e) => setEditEmail(e.currentTarget.value)}
					/>
				</Table.Td>
				<Table.Td>
					<NumberInput
						size="xs"
						value={editPriority}
						onChange={(v) => setEditPriority(Number(v) || 0)}
						min={0}
						step={1}
						style={{ width: 70 }}
					/>
				</Table.Td>
				<Table.Td colSpan={3}>
					<TextInput
						size="xs"
						label={t("kiroEditRegion")}
						value={editRegion}
						onChange={(e) => setEditRegion(e.currentTarget.value)}
					/>
				</Table.Td>
				<Table.Td colSpan={2}>
					<Group gap="xs">
						<Button
							size="compact-xs"
							onClick={handleSave}
							leftSection={<IconDeviceFloppy size={12} />}
						>
							{t("kiroEditSave")}
						</Button>
						<Button size="compact-xs" variant="subtle" onClick={() => setEditing(false)}>
							{t("kiroEditCancel")}
						</Button>
					</Group>
				</Table.Td>
			</Table.Tr>
		);
	}

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
				<CredentialActions
					entry={entry}
					editing={editing}
					onEdit={() => setEditing(true)}
					{...muts}
				/>
			</Table.Td>
		</Table.Tr>
	);
}
