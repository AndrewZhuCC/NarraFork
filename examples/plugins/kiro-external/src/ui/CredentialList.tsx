/** @jsxImportSource ./shim */

/**
 * Credential list: a table on wide panels, cards on narrow ones.
 *
 * Column set, ordering and action affordances follow the host's `KiroSection` so the two pages
 * read identically. The breakpoint is measured against the panel's own width rather than the
 * viewport: the panel is an iframe of fixed size, so a viewport media query would describe the
 * wrong box entirely.
 */

import {
	formatRegions,
	formatUsageNumber,
	getTierMeta,
	isApiKeyEntry,
	relativeTime,
} from "./format";
import { MantineCore, useEffect, useState } from "./host-runtime";
import type { CredentialEntry, UsageEntry } from "./panel-types";
import { t } from "./strings";

const {
	ActionIcon,
	Badge,
	Button,
	Checkbox,
	Group,
	Pagination,
	Paper,
	Progress,
	Stack,
	Switch,
	Table,
	Text,
	Tooltip,
} = MantineCore;

/** Below this panel width the table stops fitting and cards take over. */
const NARROW_PANEL_WIDTH = 900;

export interface CredentialActionHandlers {
	onToggleEnabled: (entry: CredentialEntry) => void;
	onRefreshToken: (entry: CredentialEntry) => void;
	onQueryUsage: (entry: CredentialEntry) => void;
	onEdit: (entry: CredentialEntry) => void;
	onReset: (entry: CredentialEntry) => void;
	onDelete: (entry: CredentialEntry) => void;
	onShowModels: (entry: CredentialEntry) => void;
}

export interface CredentialListProps extends CredentialActionHandlers {
	entries: CredentialEntry[];
	total: number;
	page: number;
	pageSize: number;
	onPageChange: (page: number) => void;
	currentId?: string;
	usageCache: Record<string, UsageEntry>;
	selectedIds: Set<string>;
	onToggleSelect: (id: string) => void;
	onToggleSelectPage: (ids: string[]) => void;
	busy: boolean;
}

/** Track the panel's own width so the layout switch reflects the iframe, not the window. */
function usePanelWidth(): number {
	const [width, setWidth] = useState<number>(() =>
		typeof document === "undefined" ? NARROW_PANEL_WIDTH : document.body.clientWidth,
	);
	useEffect(() => {
		if (typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver((entries: ResizeObserverEntry[]) => {
			const next = entries[0]?.contentRect.width;
			if (typeof next === "number") setWidth(next);
		});
		observer.observe(document.body);
		return () => observer.disconnect();
	}, []);
	return width;
}

function UsageDisplay({ entry, usage }: { entry: CredentialEntry; usage?: UsageEntry }) {
	// `meterUsage` on the credential is the live value; the cache fills in the plan limits.
	const current = entry.meterUsage ?? usage?.currentUsage;
	const limit = usage?.usageLimit;
	const title = entry.subscriptionTitle ?? usage?.subscriptionTitle;
	const tier = entry.tier ?? "free";
	const meta = getTierMeta(tier);
	const percent = limit && limit > 0 && current != null ? (current / limit) * 100 : null;

	return (
		<Stack gap={2}>
			<Group gap={4}>
				<Badge size="xs" variant="light" color={meta.color}>
					{title || meta.label}
				</Badge>
			</Group>
			{current != null ? (
				<>
					<Text size="xs">
						{formatUsageNumber(current)}
						{limit != null ? ` / ${formatUsageNumber(limit)}` : ""}
					</Text>
					{percent != null ? (
						<Progress
							value={Math.min(percent, 100)}
							size="xs"
							color={percent > 90 ? "red" : percent > 70 ? "yellow" : "blue"}
							style={{ width: 80 }}
						/>
					) : null}
				</>
			) : (
				<Text size="xs" c="dimmed">
					-
				</Text>
			)}
			{usage?.queriedAt ? (
				<Text size="xs" c="dimmed">
					{relativeTime(usage.queriedAt)}
				</Text>
			) : null}
		</Stack>
	);
}

function StatusBadges({ entry }: { entry: CredentialEntry }) {
	return (
		<Group gap={4} wrap="wrap">
			{isApiKeyEntry(entry) ? (
				<Badge color="teal" size="sm" variant="light">
					{t("apiKeyBadge")}
				</Badge>
			) : null}
			{entry.disabled ? (
				<Badge color="red" size="sm">
					{entry.disabledReason ?? t("disabled")}
				</Badge>
			) : (
				<Badge color="green" size="sm">
					{t("enabled")}
				</Badge>
			)}
		</Group>
	);
}

function ExpiryText({ entry }: { entry: CredentialEntry }) {
	return (
		<Text size="xs" c="dimmed">
			{isApiKeyEntry(entry)
				? t("apiKeyNeverExpires")
				: entry.expiresAt
					? relativeTime(entry.expiresAt)
					: "-"}
		</Text>
	);
}

function CredentialActions({
	entry,
	busy,
	handlers,
}: {
	entry: CredentialEntry;
	busy: boolean;
	handlers: CredentialActionHandlers;
}) {
	// A static API key is a long-lived bearer token; there is nothing to refresh.
	const isApiKey = isApiKeyEntry(entry);
	return (
		<Group gap="xs" wrap="wrap">
			<Switch
				size="xs"
				checked={!entry.disabled}
				disabled={busy}
				onChange={() => handlers.onToggleEnabled(entry)}
			/>
			<Tooltip label={isApiKey ? t("apiKeyNoRefresh") : t("refreshToken")} withinPortal={false}>
				<ActionIcon
					variant="subtle"
					size="sm"
					disabled={busy || isApiKey}
					onClick={() => handlers.onRefreshToken(entry)}
					aria-label={t("refreshToken")}
				>
					↻
				</ActionIcon>
			</Tooltip>
			<Button
				variant="subtle"
				size="compact-xs"
				disabled={busy}
				onClick={() => handlers.onQueryUsage(entry)}
			>
				{t("queryUsage")}
			</Button>
			<Button
				variant="subtle"
				size="compact-xs"
				disabled={busy}
				onClick={() => handlers.onShowModels(entry)}
			>
				{t("models")}
			</Button>
			<Button
				variant="subtle"
				size="compact-xs"
				disabled={busy}
				onClick={() => handlers.onEdit(entry)}
			>
				{t("edit")}
			</Button>
			<Button
				variant="subtle"
				size="compact-xs"
				color="orange"
				disabled={busy}
				onClick={() => handlers.onReset(entry)}
			>
				{t("reset")}
			</Button>
			<ActionIcon
				variant="subtle"
				color="red"
				size="sm"
				disabled={busy}
				onClick={() => handlers.onDelete(entry)}
				aria-label={t("actionRemove")}
			>
				✕
			</ActionIcon>
		</Group>
	);
}

function CredentialTable(props: CredentialListProps) {
	const ids = props.entries.map((entry) => entry.id);
	const selectedCount = ids.filter((id) => props.selectedIds.has(id)).length;

	return (
		<Table striped highlightOnHover>
			<Table.Thead>
				<Table.Tr>
					<Table.Th w={40}>
						<Checkbox
							size="xs"
							checked={ids.length > 0 && selectedCount === ids.length}
							indeterminate={selectedCount > 0 && selectedCount < ids.length}
							onChange={() => props.onToggleSelectPage(ids)}
							aria-label={t("selectAll")}
						/>
					</Table.Th>
					<Table.Th>ID</Table.Th>
					<Table.Th>{t("colDisplayName")}</Table.Th>
					<Table.Th>Email</Table.Th>
					<Table.Th>{t("colPriority")}</Table.Th>
					<Table.Th>{t("colRegion")}</Table.Th>
					<Table.Th>{t("colStatus")}</Table.Th>
					<Table.Th>{t("colExpires")}</Table.Th>
					<Table.Th>{t("colStats")}</Table.Th>
					<Table.Th>{t("colUsage")}</Table.Th>
					<Table.Th>{t("colLastUsed")}</Table.Th>
					<Table.Th>{t("colActions")}</Table.Th>
				</Table.Tr>
			</Table.Thead>
			<Table.Tbody>
				{props.entries.map((entry) => (
					<Table.Tr key={entry.id}>
						<Table.Td>
							<Checkbox
								size="xs"
								checked={props.selectedIds.has(entry.id)}
								onChange={() => props.onToggleSelect(entry.id)}
							/>
						</Table.Td>
						<Table.Td>
							<Text size="xs" ff="monospace" fw={entry.id === props.currentId ? 700 : 400}>
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
							<StatusBadges entry={entry} />
						</Table.Td>
						<Table.Td>
							<ExpiryText entry={entry} />
						</Table.Td>
						<Table.Td>
							<Text size="xs">
								{t("statSuccess")}: {entry.successCount ?? 0} / {t("statFail")}:{" "}
								{entry.failureCount ?? 0}
							</Text>
						</Table.Td>
						<Table.Td>
							<UsageDisplay entry={entry} usage={props.usageCache[entry.id]} />
						</Table.Td>
						<Table.Td>
							<Text size="xs" c="dimmed">
								{entry.lastUsedAt ? relativeTime(entry.lastUsedAt) : "-"}
							</Text>
						</Table.Td>
						<Table.Td>
							<CredentialActions entry={entry} busy={props.busy} handlers={props} />
						</Table.Td>
					</Table.Tr>
				))}
			</Table.Tbody>
		</Table>
	);
}

function CredentialCards(props: CredentialListProps) {
	return (
		<Stack gap="xs">
			{props.entries.map((entry) => {
				const isCurrent = entry.id === props.currentId;
				return (
					<Paper
						key={entry.id}
						withBorder
						p="sm"
						style={
							isCurrent
								? { borderColor: "var(--mantine-color-indigo-5)", borderWidth: 2 }
								: undefined
						}
					>
						<Stack gap="xs">
							<Group justify="space-between" wrap="wrap">
								<Group gap="xs">
									<Checkbox
										size="xs"
										checked={props.selectedIds.has(entry.id)}
										onChange={() => props.onToggleSelect(entry.id)}
									/>
									<Text size="xs" c="dimmed" ff="monospace">
										{entry.id}
									</Text>
									<Text size="sm" fw={isCurrent ? 700 : 500} truncate style={{ maxWidth: 200 }}>
										{entry.displayName || entry.email || entry.id}
									</Text>
								</Group>
								<StatusBadges entry={entry} />
							</Group>

							<Stack gap={4}>
								{entry.email ? (
									<Group justify="space-between" wrap="nowrap">
										<Text size="xs" c="dimmed">
											Email
										</Text>
										<Text size="xs">{entry.email}</Text>
									</Group>
								) : null}
								<Group justify="space-between" wrap="nowrap">
									<Text size="xs" c="dimmed">
										{t("colRegion")}
									</Text>
									<Text size="xs">{formatRegions(entry)}</Text>
								</Group>
								<Group justify="space-between" wrap="nowrap">
									<Text size="xs" c="dimmed">
										{t("colPriority")}
									</Text>
									<Text size="xs">{entry.priority}</Text>
								</Group>
								<Group justify="space-between" wrap="nowrap">
									<Text size="xs" c="dimmed">
										{t("colExpires")}
									</Text>
									<ExpiryText entry={entry} />
								</Group>
								<Group justify="space-between" wrap="nowrap">
									<Text size="xs" c="dimmed">
										{t("colStats")}
									</Text>
									<Text size="xs">
										{t("statSuccess")}: {entry.successCount ?? 0} / {t("statFail")}:{" "}
										{entry.failureCount ?? 0}
									</Text>
								</Group>
								<Group justify="space-between" wrap="nowrap">
									<Text size="xs" c="dimmed">
										{t("colLastUsed")}
									</Text>
									<Text size="xs" c="dimmed">
										{entry.lastUsedAt ? relativeTime(entry.lastUsedAt) : "-"}
									</Text>
								</Group>
							</Stack>

							<Stack gap={4}>
								<Text size="xs" c="dimmed">
									{t("colUsage")}
								</Text>
								<UsageDisplay entry={entry} usage={props.usageCache[entry.id]} />
							</Stack>

							<CredentialActions entry={entry} busy={props.busy} handlers={props} />
						</Stack>
					</Paper>
				);
			})}
		</Stack>
	);
}

export function CredentialList(props: CredentialListProps) {
	const width = usePanelWidth();
	const narrow = width < NARROW_PANEL_WIDTH;
	const totalPages = Math.max(1, Math.ceil(props.total / props.pageSize));
	const ids = props.entries.map((entry) => entry.id);
	const selectedCount = ids.filter((id) => props.selectedIds.has(id)).length;

	return (
		<Stack gap="xs">
			{narrow ? (
				<Checkbox
					size="xs"
					checked={ids.length > 0 && selectedCount === ids.length}
					indeterminate={selectedCount > 0 && selectedCount < ids.length}
					onChange={() => props.onToggleSelectPage(ids)}
					label={`${t("selectAll")} (${ids.length})`}
				/>
			) : null}
			{narrow ? <CredentialCards {...props} /> : <CredentialTable {...props} />}
			{totalPages > 1 ? (
				<Group justify="center" gap="sm">
					<Pagination
						size="sm"
						total={totalPages}
						value={props.page}
						onChange={props.onPageChange}
					/>
					<Text size="xs" c="dimmed">
						{t("pageInfo", { current: props.page, total: totalPages, count: props.total })}
					</Text>
				</Group>
			) : null}
		</Stack>
	);
}
