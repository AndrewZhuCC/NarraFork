import { MOBILE_VIEWPORT_MEDIA_QUERY } from "@frontend/lib/responsive";
import {
	ActionIcon,
	Alert,
	Badge,
	Box,
	Button,
	Card,
	Checkbox,
	Collapse,
	Group,
	Loader,
	Paper,
	ScrollArea,
	Stack,
	Table,
	Text,
	Title,
	Tooltip,
} from "@mantine/core";
import { useMediaQuery } from "@mantine/hooks";
import {
	IconPlugConnected,
	IconPlugConnectedX,
	IconRefresh,
	IconTerminal2,
	IconTrash,
	IconX,
} from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { lazy, Suspense, useCallback, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useConfirmDialog } from "../../components/common/confirm-dialog-context";
import { useCurrentUser } from "../../hooks/useAuth";
import { useTerminalCapability } from "../../hooks/usePlatform";
import { api } from "../../lib/api";
import { formatLocaleDateTime } from "../../lib/intl-format";

const TerminalPanel = lazy(() =>
	import("../../components/terminal/TerminalPanel").then((m) => ({ default: m.TerminalPanel })),
);
const ADMIN_TERMINALS_QUERY_GC_TIME_MS = 60_000;

export const Route = createFileRoute("/settings/terminals")({
	component: SettingsTerminalsPage,
});

function SettingsTerminalsPage() {
	const { data: user } = useCurrentUser();
	const { t } = useTranslation("common");
	const { t: tt } = useTranslation("terminal");
	const terminalCapability = useTerminalCapability();
	const terminalSupported = terminalCapability.supported;
	const terminalUnsupportedReason = terminalCapability.reason ?? tt("terminalUnsupported");
	const terminalRuntimeNotes = useMemo(() => {
		const notes: string[] = [];
		if (terminalCapability.multiClientResizeMode) {
			notes.push(
				t("adminTerminalsResizeModeDesc", {
					mode: terminalCapability.multiClientResizeMode,
				}),
			);
		}
		if (terminalCapability.scrollbackReplayMode) {
			notes.push(
				t("adminTerminalsReplayModeDesc", {
					mode: terminalCapability.scrollbackReplayMode,
				}),
			);
		}
		if (terminalCapability.dtachSupported && terminalCapability.dtachAvailable === false) {
			notes.push(t("adminTerminalsDtachUnavailable"));
		}
		if (terminalCapability.detachedReattach === false) {
			notes.push(t("adminTerminalsDetachedReattachUnsupported"));
		}
		if (terminalCapability.xtermSerializedReplay === false) {
			notes.push(
				terminalCapability.xtermSerializedReplayReason ??
					t("adminTerminalsXtermSerializedReplayUnsupported"),
			);
		}
		if (terminalCapability.orphanRecovery === false) {
			notes.push(t("adminTerminalsOrphanRecoveryUnsupported"));
		}
		if (terminalCapability.processTree?.supported === false) {
			notes.push(
				t("adminTerminalsProcessTreeUnsupported", {
					platform: terminalCapability.processTree.platform ?? "unsupported",
				}),
			);
		}
		return notes;
	}, [
		terminalCapability.multiClientResizeMode,
		terminalCapability.scrollbackReplayMode,
		terminalCapability.dtachSupported,
		terminalCapability.dtachAvailable,
		terminalCapability.detachedReattach,
		terminalCapability.xtermSerializedReplay,
		terminalCapability.xtermSerializedReplayReason,
		terminalCapability.orphanRecovery,
		terminalCapability.processTree?.platform,
		terminalCapability.processTree?.supported,
		t,
	]);
	const terminalRuntimeNotesAreWarnings =
		(terminalCapability.dtachSupported && terminalCapability.dtachAvailable === false) ||
		terminalCapability.detachedReattach === false ||
		terminalCapability.xtermSerializedReplay === false ||
		terminalCapability.orphanRecovery === false ||
		terminalCapability.processTree?.supported === false;
	const confirm = useConfirmDialog();
	const qc = useQueryClient();
	const isMobile = useMediaQuery(MOBILE_VIEWPORT_MEDIA_QUERY) ?? false;

	const [openTerminalId, setOpenTerminalId] = useState<string | null>(null);
	const [selected, setSelected] = useState<Set<string>>(new Set());

	const { data, isLoading, refetch } = useQuery({
		queryKey: ["admin", "terminals"],
		queryFn: api.listAdminTerminals,
		enabled: user?.role === "admin" && terminalSupported,
		refetchInterval: terminalSupported ? 10_000 : false,
		gcTime: ADMIN_TERMINALS_QUERY_GC_TIME_MS,
	});

	const killTerminal = useMutation({
		mutationFn: api.killAdminTerminal,
		onSuccess: (_, id) => {
			if (openTerminalId === id) setOpenTerminalId(null);
			qc.invalidateQueries({ queryKey: ["admin", "terminals"] });
		},
	});

	const batchKill = useMutation({
		mutationFn: api.batchKillAdminTerminals,
		onSuccess: (_data, killedIds) => {
			setSelected(new Set());
			if (openTerminalId && killedIds.includes(openTerminalId)) setOpenTerminalId(null);
			qc.invalidateQueries({ queryKey: ["admin", "terminals"] });
		},
	});

	const killOrphan = useMutation({
		mutationFn: api.killOrphanSocket,
		onSuccess: () => qc.invalidateQueries({ queryKey: ["admin", "terminals"] }),
	});

	const reattachTerminal = useMutation({
		mutationFn: api.reattachTerminal,
		onSuccess: (_, id) => {
			qc.invalidateQueries({ queryKey: ["admin", "terminals"] });
			setOpenTerminalId(id);
		},
	});

	const reattachOrphan = useMutation({
		mutationFn: api.reattachOrphan,
		onSuccess: (result) => {
			qc.invalidateQueries({ queryKey: ["admin", "terminals"] });
			// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON
			const id = (result as any)?.id;
			if (id) setOpenTerminalId(id);
		},
	});

	const toggleSelect = useCallback((id: string) => {
		setSelected((prev) => {
			const next = new Set(prev);
			if (next.has(id)) next.delete(id);
			else next.add(id);
			return next;
		});
	}, []);

	const terminals = useMemo(() => data?.terminals ?? [], [data?.terminals]);
	const orphanSockets = useMemo(() => data?.orphanSockets ?? [], [data?.orphanSockets]);
	const runningTerminals = useMemo(
		() => terminals.filter((t: { status: string }) => t.status === "running"),
		[terminals],
	);
	const exitedTerminals = useMemo(
		() => terminals.filter((t: { status: string }) => t.status !== "running"),
		[terminals],
	);
	const runningTerminalIds = useMemo(
		() => runningTerminals.map((t: { id: string }) => t.id),
		[runningTerminals],
	);
	const allRunningSelected = useMemo(
		() => runningTerminalIds.length > 0 && runningTerminalIds.every((id) => selected.has(id)),
		[runningTerminalIds, selected],
	);
	const someRunningSelected = useMemo(
		() => runningTerminalIds.some((id) => selected.has(id)),
		[runningTerminalIds, selected],
	);

	const toggleSelectAll = useCallback(() => {
		if (allRunningSelected) {
			setSelected(new Set());
		} else {
			setSelected(new Set(runningTerminalIds));
		}
	}, [allRunningSelected, runningTerminalIds]);

	if (isLoading) return <Loader />;

	return (
		<Stack>
			<Group justify="space-between" wrap="wrap" gap="xs">
				<Title order={3}>{t("adminTerminalsTitle")}</Title>
				<Tooltip label={terminalSupported ? t("refresh") : terminalUnsupportedReason}>
					<ActionIcon variant="subtle" disabled={!terminalSupported} onClick={() => refetch()}>
						<IconRefresh size={18} />
					</ActionIcon>
				</Tooltip>
			</Group>
			<Text size="sm" c="dimmed">
				{t("adminTerminalsDesc")}
			</Text>
			{!terminalSupported && (
				<Alert color="yellow" variant="light">
					{terminalUnsupportedReason}
				</Alert>
			)}
			{terminalSupported && terminalRuntimeNotes.length > 0 && (
				<Alert
					color={terminalRuntimeNotesAreWarnings ? "yellow" : "blue"}
					variant="light"
					title={t("adminTerminalsRuntimeModeTitle")}
				>
					<Stack gap={4}>
						{terminalRuntimeNotes.map((note) => (
							<Text key={note} size="sm">
								{note}
							</Text>
						))}
					</Stack>
				</Alert>
			)}

			{/* Embedded terminal panel */}
			<Collapse expanded={!!openTerminalId}>
				{openTerminalId && (
					<Paper withBorder p={0} style={{ overflow: "hidden" }}>
						<Group
							px="sm"
							py={4}
							justify="space-between"
							wrap="nowrap"
							gap="xs"
							style={{ backgroundColor: "var(--mantine-color-dark-7)" }}
						>
							<Group gap="xs" wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
								<IconTerminal2 size={14} style={{ flexShrink: 0 }} />
								<Text size="xs" ff="monospace" lineClamp={1} title={openTerminalId}>
									{openTerminalId}
								</Text>
							</Group>
							<ActionIcon size="sm" variant="subtle" onClick={() => setOpenTerminalId(null)}>
								<IconX size={14} />
							</ActionIcon>
						</Group>
						<Box style={{ height: 400 }}>
							<Suspense fallback={null}>
								<TerminalPanel terminalId={openTerminalId} />
							</Suspense>
						</Box>
					</Paper>
				)}
			</Collapse>

			{/* Orphan dtach sockets */}
			{orphanSockets.length > 0 && (
				<Paper withBorder p="md" style={{ borderColor: "var(--mantine-color-orange-7)" }}>
					<Stack>
						<Title order={4} c="orange">
							{t("orphanSockets")} ({orphanSockets.length})
						</Title>
						{isMobile ? (
							<Stack gap="xs">
								{orphanSockets.map((s: { terminalId: string; socketPath: string }) => (
									<Card key={s.terminalId} withBorder radius="sm" padding="xs">
										<Stack gap="xs">
											<Stack gap={2} style={{ minWidth: 0 }}>
												<Text size="xs" c="dimmed">
													{t("terminalId")}
												</Text>
												<Text size="sm" ff="monospace" lineClamp={1} title={s.terminalId}>
													{s.terminalId}
												</Text>
												<Text size="xs" c="dimmed" mt={4}>
													{t("socketPath")}
												</Text>
												<Text size="xs" ff="monospace" style={{ wordBreak: "break-all" }}>
													{s.socketPath}
												</Text>
											</Stack>
											<Group justify="flex-end">
												<OrphanSocketActions
													terminalId={s.terminalId}
													t={t}
													onReattach={(id) => reattachOrphan.mutate(id)}
													reattachPending={reattachOrphan.isPending}
													onKill={async (id) => {
														if (await confirm({ message: t("confirmKillOrphan") })) {
															killOrphan.mutate(id);
														}
													}}
													killPending={killOrphan.isPending}
												/>
											</Group>
										</Stack>
									</Card>
								))}
							</Stack>
						) : (
							<ScrollArea type="auto" offsetScrollbars>
								<Table miw={640}>
									<Table.Thead>
										<Table.Tr>
											<Table.Th>{t("terminalId")}</Table.Th>
											<Table.Th>{t("socketPath")}</Table.Th>
											<Table.Th>{t("actions")}</Table.Th>
										</Table.Tr>
									</Table.Thead>
									<Table.Tbody>
										{orphanSockets.map((s: { terminalId: string; socketPath: string }) => (
											<Table.Tr key={s.terminalId}>
												<Table.Td>
													<Text size="xs" ff="monospace" lineClamp={1} title={s.terminalId}>
														{s.terminalId}
													</Text>
												</Table.Td>
												<Table.Td>
													<Text size="xs" ff="monospace" lineClamp={1} title={s.socketPath}>
														{s.socketPath}
													</Text>
												</Table.Td>
												<Table.Td>
													<OrphanSocketActions
														terminalId={s.terminalId}
														t={t}
														onReattach={(id) => reattachOrphan.mutate(id)}
														reattachPending={reattachOrphan.isPending}
														onKill={async (id) => {
															if (await confirm({ message: t("confirmKillOrphan") })) {
																killOrphan.mutate(id);
															}
														}}
														killPending={killOrphan.isPending}
													/>
												</Table.Td>
											</Table.Tr>
										))}
									</Table.Tbody>
								</Table>
							</ScrollArea>
						)}
					</Stack>
				</Paper>
			)}

			{/* Running terminals */}
			<Paper withBorder p="md">
				<Stack>
					<Group justify="space-between" wrap="wrap" gap="xs">
						<Title order={4}>
							{t("running")} ({runningTerminals.length})
						</Title>
						{selected.size > 0 && (
							<Button
								color="red"
								size="xs"
								variant="light"
								leftSection={<IconTrash size={14} />}
								loading={batchKill.isPending}
								onClick={async () => {
									if (await confirm({ message: t("confirmBatchKill", { count: selected.size }) })) {
										batchKill.mutate([...selected]);
									}
								}}
							>
								{t("batchKill")} ({selected.size})
							</Button>
						)}
					</Group>
					{runningTerminals.length === 0 ? (
						<Text c="dimmed" size="sm">
							{t("noTerminals")}
						</Text>
					) : (
						<TerminalTable
							terminals={runningTerminals}
							t={t}
							openTerminalId={openTerminalId}
							selected={selected}
							onToggleSelect={toggleSelect}
							allSelected={allRunningSelected}
							someSelected={someRunningSelected}
							onToggleSelectAll={toggleSelectAll}
							onConnect={(id, attached) => {
								if (!attached) {
									reattachTerminal.mutate(id);
								} else {
									setOpenTerminalId(openTerminalId === id ? null : id);
								}
							}}
							reattachPending={reattachTerminal.isPending}
							onKill={async (id) => {
								if (await confirm({ message: t("confirmKillTerminal") })) {
									killTerminal.mutate(id);
								}
							}}
							killPending={killTerminal.isPending}
						/>
					)}
				</Stack>
			</Paper>

			{/* Exited terminals */}
			{exitedTerminals.length > 0 && (
				<Paper withBorder p="md">
					<Stack>
						<Title order={4}>
							{t("exited")} ({exitedTerminals.length})
						</Title>
						<TerminalTable terminals={exitedTerminals} t={t} />
					</Stack>
				</Paper>
			)}
		</Stack>
	);
}

function TerminalStatusBadges({
	term,
	t,
}: {
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	term: any;
	t: (key: string, opts?: Record<string, unknown>) => string;
}) {
	return (
		<Group gap={4} wrap="nowrap" style={{ flexShrink: 0 }}>
			<Badge color={term.status === "running" ? "green" : "gray"} size="sm">
				{t(term.status)}
			</Badge>
			{term.status === "running" && (
				<Badge color={term.attached ? "teal" : "yellow"} size="sm" variant="light">
					{term.attached ? t("attached") : t("detached")}
				</Badge>
			)}
		</Group>
	);
}

function OrphanSocketActions({
	terminalId,
	t,
	onReattach,
	reattachPending,
	onKill,
	killPending,
}: {
	terminalId: string;
	t: (key: string, opts?: Record<string, unknown>) => string;
	onReattach: (terminalId: string) => void;
	reattachPending?: boolean;
	onKill: (terminalId: string) => void | Promise<void>;
	killPending?: boolean;
}) {
	const reattachLabel = t("reattachOrphan");
	const killLabel = t("killOrphan");
	return (
		<Group gap="xs" wrap="nowrap">
			<Tooltip label={reattachLabel} events={{ hover: true, focus: true, touch: true }}>
				<ActionIcon
					color="green"
					variant="subtle"
					aria-label={reattachLabel}
					onClick={() => onReattach(terminalId)}
					loading={reattachPending}
				>
					<IconPlugConnected size={16} />
				</ActionIcon>
			</Tooltip>
			<Tooltip label={killLabel} events={{ hover: true, focus: true, touch: true }}>
				<ActionIcon
					color="red"
					variant="subtle"
					aria-label={killLabel}
					onClick={() => onKill(terminalId)}
					loading={killPending}
				>
					<IconTrash size={16} />
				</ActionIcon>
			</Tooltip>
		</Group>
	);
}

function TerminalRowActions({
	term,
	t,
	openTerminalId,
	onConnect,
	reattachPending,
	onKill,
	killPending,
}: {
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	term: any;
	t: (key: string, opts?: Record<string, unknown>) => string;
	openTerminalId?: string | null;
	onConnect?: (id: string, attached: boolean) => void;
	reattachPending?: boolean;
	onKill?: (id: string) => void | Promise<void>;
	killPending?: boolean;
}) {
	if (!onConnect && !onKill) return null;
	const connectLabel = term.attached
		? openTerminalId === term.id
			? t("closePanel")
			: t("attach")
		: t("reattach");
	const killLabel = t("killTerminal");
	return (
		<Group gap="xs" wrap="nowrap">
			{onConnect && (
				<Tooltip label={connectLabel} events={{ hover: true, focus: true, touch: true }}>
					<ActionIcon
						color={term.attached ? "teal" : "yellow"}
						variant={openTerminalId === term.id ? "filled" : "subtle"}
						aria-label={connectLabel}
						onClick={() => onConnect(term.id, !!term.attached)}
						loading={reattachPending}
					>
						{term.attached ? <IconTerminal2 size={16} /> : <IconPlugConnectedX size={16} />}
					</ActionIcon>
				</Tooltip>
			)}
			{onKill && (
				<Tooltip label={killLabel} events={{ hover: true, focus: true, touch: true }}>
					<ActionIcon
						color="red"
						variant="subtle"
						aria-label={killLabel}
						onClick={() => onKill(term.id)}
						loading={killPending}
					>
						<IconTrash size={16} />
					</ActionIcon>
				</Tooltip>
			)}
		</Group>
	);
}

function TerminalTable({
	terminals,
	t,
	openTerminalId,
	selected,
	onToggleSelect,
	allSelected,
	someSelected,
	onToggleSelectAll,
	onConnect,
	reattachPending,
	onKill,
	killPending,
}: {
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure
	terminals: any[];
	t: (key: string, opts?: Record<string, unknown>) => string;
	openTerminalId?: string | null;
	selected?: Set<string>;
	onToggleSelect?: (id: string) => void;
	allSelected?: boolean;
	someSelected?: boolean;
	onToggleSelectAll?: () => void;
	onConnect?: (id: string, attached: boolean) => void;
	reattachPending?: boolean;
	onKill?: (id: string) => void | Promise<void>;
	killPending?: boolean;
}) {
	const isMobile = useMediaQuery(MOBILE_VIEWPORT_MEDIA_QUERY) ?? false;
	const selectable = !!onToggleSelect;

	if (isMobile) {
		return (
			<Stack gap="xs">
				{selectable && (
					<Checkbox
						size="sm"
						label={t("selectAll")}
						checked={allSelected}
						indeterminate={someSelected && !allSelected}
						onChange={onToggleSelectAll}
					/>
				)}
				{/* biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure */}
				{terminals.map((term: any) => (
					<Card
						key={term.id}
						withBorder
						radius="sm"
						padding="xs"
						style={
							openTerminalId === term.id
								? { borderColor: "var(--mantine-color-teal-5)" }
								: undefined
						}
					>
						<Stack gap="xs">
							<Group justify="space-between" align="start" wrap="nowrap" gap="xs">
								{selectable && (
									<Checkbox
										size="sm"
										aria-label={term.name}
										checked={selected?.has(term.id) ?? false}
										onChange={() => onToggleSelect?.(term.id)}
										mt={2}
									/>
								)}
								<Stack gap={2} style={{ minWidth: 0, flex: 1 }}>
									<Text size="sm" fw={500} lineClamp={1}>
										{term.name}
									</Text>
									<Text size="xs" c="dimmed" ff="monospace" lineClamp={1} title={term.id}>
										{term.id}
									</Text>
								</Stack>
								<TerminalStatusBadges term={term} t={t} />
							</Group>

							<Stack gap={2}>
								<Text size="xs" c="dimmed">
									{t("terminalCwd")}
								</Text>
								<Text size="xs" ff="monospace" style={{ wordBreak: "break-all" }}>
									{term.cwd ?? "—"}
								</Text>
							</Stack>

							{(term.processes?.length ?? 0) > 0 && (
								<Stack gap={2}>
									<Text size="xs" c="dimmed">
										{t("terminalProcesses")}
									</Text>
									<ProcessList processes={term.processes} />
								</Stack>
							)}

							<Group justify="space-between" align="center" wrap="nowrap" gap="xs">
								<Text size="xs" c="dimmed" lineClamp={1} style={{ minWidth: 0, flex: 1 }}>
									{formatLocaleDateTime(term.createdAt)}
								</Text>
								<TerminalRowActions
									term={term}
									t={t}
									openTerminalId={openTerminalId}
									onConnect={onConnect}
									reattachPending={reattachPending}
									onKill={onKill}
									killPending={killPending}
								/>
							</Group>
						</Stack>
					</Card>
				))}
			</Stack>
		);
	}

	return (
		<ScrollArea type="auto" offsetScrollbars>
			<Table miw={900}>
				<Table.Thead>
					<Table.Tr>
						{selectable && (
							<Table.Th w={40}>
								<Checkbox
									size="xs"
									aria-label={t("selectAll")}
									checked={allSelected}
									indeterminate={someSelected && !allSelected}
									onChange={onToggleSelectAll}
								/>
							</Table.Th>
						)}
						<Table.Th>{t("terminalName")}</Table.Th>
						<Table.Th>{t("terminalStatus")}</Table.Th>
						<Table.Th>{t("terminalProcesses")}</Table.Th>
						<Table.Th>{t("terminalCwd")}</Table.Th>
						<Table.Th>{t("terminalCreated")}</Table.Th>
						{(onConnect || onKill) && <Table.Th>{t("actions")}</Table.Th>}
					</Table.Tr>
				</Table.Thead>
				<Table.Tbody>
					{/* biome-ignore lint/suspicious/noExplicitAny: dynamic JSON structure */}
					{terminals.map((term: any) => (
						<Table.Tr
							key={term.id}
							style={
								openTerminalId === term.id
									? { backgroundColor: "var(--mantine-color-dark-6)" }
									: undefined
							}
						>
							{selectable && (
								<Table.Td>
									<Checkbox
										size="xs"
										aria-label={term.name}
										checked={selected?.has(term.id) ?? false}
										onChange={() => onToggleSelect?.(term.id)}
									/>
								</Table.Td>
							)}
							<Table.Td>
								<Text size="sm">{term.name}</Text>
								<Text size="xs" c="dimmed" ff="monospace" lineClamp={1} title={term.id}>
									{term.id}
								</Text>
							</Table.Td>
							<Table.Td>
								<TerminalStatusBadges term={term} t={t} />
							</Table.Td>
							<Table.Td>
								<ProcessList processes={term.processes} />
							</Table.Td>
							<Table.Td>
								<Text size="xs" ff="monospace" lineClamp={1} maw={200}>
									{term.cwd ?? "—"}
								</Text>
							</Table.Td>
							<Table.Td>
								<Text size="xs">{formatLocaleDateTime(term.createdAt)}</Text>
							</Table.Td>
							{(onConnect || onKill) && (
								<Table.Td>
									<TerminalRowActions
										term={term}
										t={t}
										openTerminalId={openTerminalId}
										onConnect={onConnect}
										reattachPending={reattachPending}
										onKill={onKill}
										killPending={killPending}
									/>
								</Table.Td>
							)}
						</Table.Tr>
					))}
				</Table.Tbody>
			</Table>
		</ScrollArea>
	);
}

function ProcessList({
	processes,
}: {
	// biome-ignore lint/suspicious/noExplicitAny: dynamic JSON
	processes?: any[];
}) {
	if (!processes || processes.length === 0) {
		return (
			<Text size="xs" c="dimmed">
				—
			</Text>
		);
	}
	return (
		<Stack gap={2}>
			{processes.map(
				(p: {
					pid: number;
					command: string;
					state: string;
					rss: number;
					cpu: number;
					elapsed: string;
				}) => (
					<Group key={p.pid} gap={6} wrap="wrap">
						<Badge
							size="xs"
							variant="dot"
							color={p.state.startsWith("R") ? "green" : p.state.startsWith("S") ? "blue" : "gray"}
							maw="100%"
						>
							<Text size="xs" span lineClamp={1} title={p.command}>
								{p.command}
							</Text>
						</Badge>
						<Text size="xs" c="dimmed" ff="monospace">
							{formatRss(p.rss)}
						</Text>
						<Text size="xs" c="dimmed">
							{p.elapsed}
						</Text>
					</Group>
				),
			)}
		</Stack>
	);
}

function formatRss(kb: number): string {
	if (kb < 1024) return `${kb}K`;
	if (kb < 1024 * 1024) return `${(kb / 1024).toFixed(1)}M`;
	return `${(kb / 1024 / 1024).toFixed(1)}G`;
}
