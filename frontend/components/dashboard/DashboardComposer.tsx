import {
	ActionIcon,
	Button,
	Group,
	Menu,
	Modal,
	Paper,
	Select,
	Stack,
	Text,
	Textarea,
	Tooltip,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
	IconAdjustmentsHorizontal,
	IconArrowUp,
	IconCheck,
	IconChevronDown,
	IconClock,
	IconFolder,
	IconFolderOpen,
	IconSparkles,
	IconStarFilled,
} from "@tabler/icons-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useFavoriteDirectories } from "../../hooks/useFavoriteDirectories";
import { useAllModels } from "../../hooks/useModels";
import { useCreateNarrator } from "../../hooks/useNarrator";
import { addRecentTab } from "../../hooks/useRecentTabs";
import { api } from "../../lib/api";
import { FOLLOW_DEFAULT_MODEL } from "../../lib/constants";
import { DirectoryPicker } from "../common/DirectoryPicker";
import type { CreateNarratorResult } from "../narrator/CreateNarratorModal";
import { directoryLabel } from "./dashboard-sessions";

const CreateNarratorModal = lazy(() =>
	import("../narrator/CreateNarratorModal").then((m) => ({ default: m.CreateNarratorModal })),
);

const COMPOSER_PREFS_KEY = "narrafork_dashboard_composer";
const SETTINGS_QUERY_GC_TIME_MS = 60_000;
const MODEL_SELECT_OPTION_LIMIT = 100;
const RECENT_DIRECTORY_LIMIT = 5;

interface ComposerPrefs {
	cwd?: string;
	model?: string;
}

function readPrefs(): ComposerPrefs {
	try {
		const raw = localStorage.getItem(COMPOSER_PREFS_KEY);
		const parsed = raw ? JSON.parse(raw) : null;
		return parsed && typeof parsed === "object" ? parsed : {};
	} catch {
		return {};
	}
}

function writePrefs(prefs: ComposerPrefs) {
	try {
		localStorage.setItem(COMPOSER_PREFS_KEY, JSON.stringify(prefs));
	} catch {
		// Private mode / blocked storage: the selection just isn't remembered.
	}
}

/**
 * Cursor-style entry point: type a task, pick a directory and model, press Enter.
 * Creates a standalone session, sends the text as its first message, then opens it.
 */
export function DashboardComposer({ recentDirectories }: { recentDirectories: string[] }) {
	const { t } = useTranslation("dashboard");
	const { t: tn } = useTranslation("narrators");
	const navigate = useNavigate();
	const createNarrator = useCreateNarrator();
	const { groupedModels } = useAllModels();
	const { data: favorites } = useFavoriteDirectories();
	const { data: settings } = useQuery({
		queryKey: ["settings"],
		queryFn: api.getSettings,
		gcTime: SETTINGS_QUERY_GC_TIME_MS,
	});
	const defaultProjectDir: string = settings?.paths?.defaultProjectDir ?? "";

	const [prefs] = useState(readPrefs);
	const [text, setText] = useState("");
	// `null` = never chosen on this device: follow the instance default directory.
	const [chosenCwd, setChosenCwd] = useState<string | null>(prefs.cwd || null);
	const [model, setModel] = useState(prefs.model || FOLLOW_DEFAULT_MODEL);
	const [submitting, setSubmitting] = useState(false);
	const [browseOpen, setBrowseOpen] = useState(false);
	const [browseValue, setBrowseValue] = useState("");
	const [advancedOpen, setAdvancedOpen] = useState(false);
	const textareaRef = useRef<HTMLTextAreaElement>(null);
	// A session created by a submit whose first message then failed to send. Retrying
	// reuses it instead of leaving an empty session behind per attempt.
	const pendingSessionRef = useRef<CreateNarratorResult | null>(null);

	const cwd = chosenCwd ?? defaultProjectDir;

	useEffect(() => {
		textareaRef.current?.focus();
	}, []);

	const selectCwd = (path: string) => {
		pendingSessionRef.current = null;
		setChosenCwd(path);
		writePrefs({ cwd: path, model });
	};

	const selectModel = (value: string | null) => {
		const next = value || FOLLOW_DEFAULT_MODEL;
		pendingSessionRef.current = null;
		setModel(next);
		writePrefs({ cwd: chosenCwd ?? undefined, model: next });
	};

	const favoritePaths = useMemo<{ path: string; label: string }[]>(
		// biome-ignore lint/suspicious/noExplicitAny: dynamic favorite directory shape
		() => (favorites ?? []).map((f: any) => ({ path: f.path, label: f.label || f.path })),
		[favorites],
	);
	const recentPaths = useMemo(() => {
		const favoriteSet = new Set(favoritePaths.map((f) => f.path));
		return recentDirectories.filter((p) => !favoriteSet.has(p)).slice(0, RECENT_DIRECTORY_LIMIT);
	}, [recentDirectories, favoritePaths]);

	const openSession = (session: CreateNarratorResult) => {
		void addRecentTab({
			type: "narrator",
			id: session.id,
			title: session.title,
			subtitle: session.cwd,
			status: session.status,
		});
		navigate({ to: "/narrators/$narratorId", params: { narratorId: session.id } });
	};

	const submit = async () => {
		const message = text.trim();
		if (!message || submitting) return;
		setSubmitting(true);
		try {
			let session = pendingSessionRef.current;
			if (!session) {
				const data = await createNarrator.mutateAsync({
					...(cwd ? { cwd } : {}),
					model,
				});
				session = {
					id: data.id,
					title: data.title || tn("newNarrator"),
					cwd: data.cwd || cwd,
					status: data.status || "idle",
				};
				pendingSessionRef.current = session;
			}
			try {
				await api.sendNarratorMessage(session.id, message);
			} catch (error) {
				notifications.show({
					color: "red",
					title: t("composer.sendFailed"),
					message: error instanceof Error ? error.message : String(error),
				});
				return;
			}
			pendingSessionRef.current = null;
			setText("");
			openSession(session);
		} catch {
			// Creation failures are already toasted by the global mutation error handler.
		} finally {
			setSubmitting(false);
		}
	};

	const canSubmit = text.trim().length > 0 && !submitting;

	return (
		<Stack gap="xs">
			<Paper
				withBorder
				radius="lg"
				shadow="xs"
				px="md"
				pt="sm"
				pb="xs"
				style={{ cursor: "text" }}
				onClick={() => textareaRef.current?.focus()}
			>
				<Textarea
					ref={textareaRef}
					variant="unstyled"
					autosize
					minRows={3}
					maxRows={12}
					size="md"
					placeholder={t("composer.placeholder")}
					value={text}
					onChange={(e) => setText(e.currentTarget.value)}
					onKeyDown={(e) => {
						if (e.key !== "Enter" || e.shiftKey || e.nativeEvent.isComposing) return;
						e.preventDefault();
						void submit();
					}}
					aria-label={t("composer.placeholder")}
				/>
				<Group justify="space-between" wrap="nowrap" mt={4}>
					<Text size="xs" c="dimmed" visibleFrom="sm">
						{t("composer.hint")}
					</Text>
					<Group gap={6} wrap="nowrap" ml="auto">
						<Tooltip label={t("composer.advanced")}>
							<ActionIcon
								variant="subtle"
								color="gray"
								radius="xl"
								size="lg"
								aria-label={t("composer.advanced")}
								onClick={(e) => {
									e.stopPropagation();
									setAdvancedOpen(true);
								}}
							>
								<IconAdjustmentsHorizontal size={18} />
							</ActionIcon>
						</Tooltip>
						<ActionIcon
							variant="filled"
							radius="xl"
							size="lg"
							aria-label={t("composer.send")}
							disabled={!canSubmit}
							loading={submitting}
							onClick={(e) => {
								e.stopPropagation();
								void submit();
							}}
						>
							<IconArrowUp size={18} />
						</ActionIcon>
					</Group>
				</Group>
			</Paper>

			<Group gap="xs" px={4} wrap="wrap">
				<Menu position="bottom-start" width={320} shadow="md" withinPortal>
					<Menu.Target>
						<Button
							variant="subtle"
							color="gray"
							size="compact-sm"
							leftSection={<IconFolder size={15} />}
							rightSection={<IconChevronDown size={13} />}
							title={cwd || undefined}
							styles={{ label: { maxWidth: 220 } }}
						>
							<Text span size="sm" truncate>
								{cwd ? directoryLabel(cwd) : t("composer.homeDirectory")}
							</Text>
						</Button>
					</Menu.Target>
					<Menu.Dropdown>
						{defaultProjectDir && (
							<DirectoryItem
								path={defaultProjectDir}
								label={t("composer.defaultDirectory")}
								active={cwd === defaultProjectDir}
								icon={<IconFolder size={15} />}
								onSelect={selectCwd}
							/>
						)}
						{favoritePaths.length > 0 && <Menu.Label>{t("composer.favorites")}</Menu.Label>}
						{favoritePaths.map((f) => (
							<DirectoryItem
								key={f.path}
								path={f.path}
								label={f.label}
								active={cwd === f.path}
								icon={<IconStarFilled size={14} color="var(--mantine-color-yellow-5)" />}
								onSelect={selectCwd}
							/>
						))}
						{recentPaths.length > 0 && <Menu.Label>{t("composer.recentDirectories")}</Menu.Label>}
						{recentPaths.map((path) => (
							<DirectoryItem
								key={path}
								path={path}
								label={directoryLabel(path)}
								active={cwd === path}
								icon={<IconClock size={15} />}
								onSelect={selectCwd}
							/>
						))}
						<Menu.Divider />
						<Menu.Item
							leftSection={<IconFolderOpen size={15} />}
							onClick={() => {
								setBrowseValue(cwd);
								setBrowseOpen(true);
							}}
						>
							{t("composer.otherDirectory")}
						</Menu.Item>
					</Menu.Dropdown>
				</Menu>

				<Select
					variant="unstyled"
					size="sm"
					w={210}
					data={groupedModels}
					searchable
					limit={MODEL_SELECT_OPTION_LIMIT}
					value={model}
					onChange={selectModel}
					allowDeselect={false}
					leftSection={<IconSparkles size={15} />}
					rightSection={<IconChevronDown size={13} />}
					rightSectionPointerEvents="none"
					maxDropdownHeight={320}
					comboboxProps={{ width: 300, position: "bottom-start", shadow: "md" }}
					aria-label={tn("model")}
					styles={{ input: { color: "var(--mantine-color-dimmed)", fontSize: 14 } }}
				/>
			</Group>

			<Modal
				opened={browseOpen}
				onClose={() => setBrowseOpen(false)}
				title={tn("workingDirectory")}
				size="lg"
			>
				<Stack>
					<DirectoryPicker
						value={browseValue}
						onChange={setBrowseValue}
						placeholder={defaultProjectDir || undefined}
						leftSection={<IconFolder size={16} />}
					/>
					<Group justify="flex-end">
						<Button
							disabled={!browseValue.trim()}
							onClick={() => {
								selectCwd(browseValue.trim());
								setBrowseOpen(false);
							}}
						>
							{t("composer.useDirectory")}
						</Button>
					</Group>
				</Stack>
			</Modal>

			{advancedOpen && (
				<Suspense fallback={null}>
					<CreateNarratorModal
						opened
						onClose={() => setAdvancedOpen(false)}
						initialCwd={cwd || undefined}
						onCreated={openSession}
					/>
				</Suspense>
			)}
		</Stack>
	);
}

function DirectoryItem({
	path,
	label,
	active,
	icon,
	onSelect,
}: {
	path: string;
	label: string;
	active: boolean;
	icon: React.ReactNode;
	onSelect: (path: string) => void;
}) {
	return (
		<Menu.Item
			leftSection={icon}
			rightSection={active ? <IconCheck size={14} /> : null}
			onClick={() => onSelect(path)}
		>
			<Text size="sm" truncate>
				{label}
			</Text>
			<Text size="xs" c="dimmed" truncate title={path}>
				{path}
			</Text>
		</Menu.Item>
	);
}
