import { useRenderInteractive } from "@frontend/components/narrator/lod/RenderLodCtx";
import { useMobileViewport } from "@frontend/hooks/useMobileViewport";
import { onTokenChange } from "@frontend/lib/api/client";
import { Box, Button, Drawer, Group, Loader, Modal, Text } from "@mantine/core";
import type { GenUiFileReference } from "@shared/gen-ui-files";
import type { GenUiKitNotice } from "@shared/gen-ui-kit";
import { lazy, Suspense, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

const FilePreview = lazy(() =>
	import("./FilePreview").then((module) => ({ default: module.FilePreview })),
);
const InlineRender = lazy(() =>
	import("./InlineRender").then((module) => ({ default: module.InlineRender })),
);
const AppFrame = lazy(() =>
	import("./FilePreview").then((module) => ({ default: module.AppFrame })),
);
const KitNoticeBar = lazy(() =>
	import("./KitNoticeBar").then((module) => ({ default: module.KitNoticeBar })),
);

/** Header row of an inline card; the frame gets the rest of the reserved height. */
const INLINE_HEADER = 28;
/** Reserved for the kit confirmation row, so it never moves the frame when it appears. */
const NOTICE_ROW = 26;

export interface GenUICardProps {
	narratorId?: string;
	file: GenUiFileReference | null;
	publicSummary?: boolean;
	historyUnavailable?: boolean;
	height: number;
	/** `render` markup, drawn in the row itself and re-drawn as it streams. */
	inline?: {
		source: string;
		live: boolean;
		title?: string;
		/** What the finished call was drawn with; old UI keeps that runtime. */
		runtime?: "builtin" | "project";
		/** Set while a `wait: true` call is holding open for this form. */
		formToolUseId?: string;
		/** The tool call: frames of this card share what the reader entered. */
		stateKey?: string;
	};
	/** The kit this result used, and any build the user is being asked to confirm. */
	kit?: GenUiKitNotice;
	/** A `show` call: the app runs in the card, and when it was shown decides if it starts. */
	appShownAt?: string;
}

/** List rows and public shares never mount a preview, fetch source or execute an iframe. */
export function GenUICard(props: GenUICardProps) {
	const interactive = useRenderInteractive();
	const { t } = useTranslation("narrator");
	const usable = !props.publicSummary && interactive && !!props.narratorId;
	if (usable && props.narratorId && props.inline) {
		return (
			<InlineCard
				key={props.narratorId}
				narratorId={props.narratorId}
				inline={props.inline}
				kit={props.kit}
				height={props.height}
			/>
		);
	}
	if (usable && props.narratorId && props.file && props.appShownAt) {
		return (
			<AppCard
				key={`${props.narratorId}:${props.file.toolCallId}`}
				narratorId={props.narratorId}
				file={props.file}
				shownAt={props.appShownAt}
				historyUnavailable={props.historyUnavailable}
				kit={props.kit}
				height={props.height}
			/>
		);
	}
	if (usable && props.narratorId && props.file) {
		return (
			<PrivateCard
				key={`${props.narratorId}:${props.file.toolCallId}`}
				narratorId={props.narratorId}
				file={props.file}
				historyUnavailable={props.historyUnavailable}
				kit={props.kit}
				height={props.height}
			/>
		);
	}
	if (usable && props.narratorId && props.kit?.request) {
		return (
			<Box h={props.height} p="xs" style={{ overflow: "hidden" }}>
				<Text fw={600} size="sm">
					{t("genUi.kit.title")}
				</Text>
				<Text size="xs" c="dimmed" truncate>
					{t(`genUi.kit.state.${props.kit.state}`)}
				</Text>
				<Suspense fallback={null}>
					<KitNoticeBar narratorId={props.narratorId} notice={props.kit} />
				</Suspense>
			</Box>
		);
	}
	return (
		<Box h={props.height} p="xs">
			<Text fw={600} size="sm">
				{props.inline?.title || t("genUi.title")}
			</Text>
			<Text size="xs" c="dimmed">
				{t(
					props.publicSummary || !interactive || !props.narratorId
						? "genUi.staticPreview"
						: "genUi.unavailable",
				)}
			</Text>
		</Box>
	);
}

function InlineCard({
	narratorId,
	inline,
	kit,
	height,
}: {
	narratorId: string;
	inline: NonNullable<GenUICardProps["inline"]>;
	kit?: GenUiKitNotice;
	height: number;
}) {
	const { t } = useTranslation("narrator");
	const mobile = useMobileViewport();
	const [expanded, setExpanded] = useState(false);
	const [closing, setClosing] = useState(false);
	const title = inline.title || t("genUi.title");
	const form = inline.live ? undefined : inline.formToolUseId;
	// Kept out of the chat bundle: only a card that is waiting on its reader needs it.
	const closeForm = () => {
		if (!form) return;
		setClosing(true);
		void import("./kit-client")
			.then((client) => client.answerGenUiForm(narratorId, form, { status: "closed" }))
			.catch(() => undefined)
			.finally(() => setClosing(false));
	};
	const notice = kit?.request ? NOTICE_ROW : 0;
	const expandedFrame = expanded ? (
		<Suspense fallback={<Loader size="sm" />}>
			<InlineRender
				narratorId={narratorId}
				source={inline.source}
				live={inline.live}
				height="70dvh"
				title={title}
				runtime={inline.runtime}
				formToolUseId={form}
				stateKey={inline.stateKey}
			/>
		</Suspense>
	) : null;
	return (
		<Box h={height} style={{ overflow: "hidden" }}>
			<Group h={INLINE_HEADER} px="xs" gap="xs" wrap="nowrap" justify="space-between">
				<Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
					{inline.live && <Loader size={12} />}
					<Text fw={600} size="xs" truncate title={title}>
						{inline.live
							? t("genUi.streaming", { title })
							: form
								? t("genUi.formWaiting", { title })
								: title}
					</Text>
				</Group>
				<Group gap={4} wrap="nowrap" style={{ flexShrink: 0 }}>
					{form && (
						<Button
							size="compact-xs"
							variant="subtle"
							color="gray"
							loading={closing}
							onClick={closeForm}
						>
							{t("genUi.formClose")}
						</Button>
					)}
					<Button size="compact-xs" variant="subtle" onClick={() => setExpanded(true)}>
						{t("genUi.expand")}
					</Button>
				</Group>
			</Group>
			{kit?.request && (
				<Box px="xs">
					<Suspense fallback={<Box h={NOTICE_ROW} />}>
						<KitNoticeBar narratorId={narratorId} notice={kit} />
					</Suspense>
				</Box>
			)}
			<Suspense fallback={null}>
				<InlineRender
					narratorId={narratorId}
					source={inline.source}
					live={inline.live}
					height={Math.max(60, height - INLINE_HEADER - notice)}
					title={title}
					runtime={inline.runtime}
					formToolUseId={form}
					stateKey={inline.stateKey}
				/>
			</Suspense>
			{expanded &&
				(mobile ? (
					<Drawer
						opened
						onClose={() => setExpanded(false)}
						title={title}
						position="bottom"
						size="90%"
					>
						{expandedFrame}
					</Drawer>
				) : (
					<Modal opened onClose={() => setExpanded(false)} title={title} size="90%">
						{expandedFrame}
					</Modal>
				))}
		</Box>
	);
}

function PrivateCard({
	narratorId,
	file,
	height,
	historyUnavailable,
	kit,
}: {
	narratorId: string;
	file: GenUiFileReference;
	height: number;
	historyUnavailable?: boolean;
	kit?: GenUiKitNotice;
}) {
	const { t } = useTranslation("narrator");
	const mobile = useMobileViewport();
	const [opened, setOpened] = useState(false);
	useEffect(() => {
		if (!opened) return;
		const unsubscribe = onTokenChange(() => setOpened(false));
		const onStorage = (event: StorageEvent) => {
			if (event.key === "narrafork_token" || event.key === null) setOpened(false);
		};
		window.addEventListener("storage", onStorage);
		return () => {
			unsubscribe();
			window.removeEventListener("storage", onStorage);
		};
	}, [opened]);
	const title = file.title || t("genUi.title");
	const path = `${file.directory.replace(/[\\/]$/, "")}/${file.entry}`;
	const preview = opened ? (
		<Suspense fallback={<Loader size="sm" />}>
			<FilePreview narratorId={narratorId} file={file} />
		</Suspense>
	) : null;
	return (
		<>
			<Box h={height} p="xs" style={{ overflow: "hidden" }}>
				<Group justify="space-between" gap="xs" wrap="nowrap">
					<Text fw={600} size="sm" truncate style={{ minWidth: 0 }} title={title}>
						{title}
					</Text>
					<Button
						size="compact-xs"
						variant="light"
						onClick={() => setOpened(true)}
						style={{ flexShrink: 0 }}
					>
						{t("genUi.open")}
					</Button>
				</Group>
				<Text size="xs" c="dimmed" truncate title={path}>
					{path}
				</Text>
				{kit?.request ? (
					// Takes the status line's place: the card's height is fixed.
					<Suspense fallback={null}>
						<KitNoticeBar narratorId={narratorId} notice={kit} />
					</Suspense>
				) : (
					<Text size="xs" c={historyUnavailable ? "orange" : "dimmed"} truncate>
						{t(historyUnavailable ? "genUi.historyUnavailable" : "genUi.currentFiles")}
					</Text>
				)}
			</Box>
			{opened &&
				(mobile ? (
					<Drawer
						opened
						onClose={() => setOpened(false)}
						title={title}
						position="bottom"
						size="90%"
					>
						{preview}
					</Drawer>
				) : (
					<Modal opened onClose={() => setOpened(false)} title={title} size="90%">
						{preview}
					</Modal>
				))}
		</>
	);
}

/** An app shown longer ago than this waits for a click, even as the newest. */
const AUTO_RUN_WINDOW_MS = 15 * 60_000;
const newestShown = new Map<string, number>();

/**
 * Only the newest app in a conversation starts by itself, and only while it is recent:
 * scrolling back through history must not build and run every app it passes, nor run
 * code the agent has long since moved on from.
 */
function startsByItself(narratorId: string, shownAt: string): boolean {
	const at = Date.parse(shownAt);
	const newest = Math.max(newestShown.get(narratorId) ?? 0, at);
	newestShown.set(narratorId, newest);
	if (newestShown.size > 50) newestShown.delete(newestShown.keys().next().value as string);
	return at === newest && Date.now() - at < AUTO_RUN_WINDOW_MS;
}

function AppCard({
	narratorId,
	file,
	shownAt,
	height,
	historyUnavailable,
	kit,
}: {
	narratorId: string;
	file: GenUiFileReference;
	shownAt: string;
	height: number;
	historyUnavailable?: boolean;
	kit?: GenUiKitNotice;
}) {
	const { t } = useTranslation("narrator");
	const mobile = useMobileViewport();
	const [running, setRunning] = useState(() => startsByItself(narratorId, shownAt));
	const [run, setRun] = useState(0);
	const [expanded, setExpanded] = useState(false);
	useEffect(() => {
		if (!running && !expanded) return;
		const stop = () => {
			setRunning(false);
			setExpanded(false);
		};
		const unsubscribe = onTokenChange(stop);
		const onStorage = (event: StorageEvent) => {
			if (event.key === "narrafork_token" || event.key === null) stop();
		};
		window.addEventListener("storage", onStorage);
		return () => {
			unsubscribe();
			window.removeEventListener("storage", onStorage);
		};
	}, [running, expanded]);
	const title = file.title || t("genUi.title");
	const path = `${file.directory.replace(/[\\/]$/, "")}/${file.entry}`;
	const notice = kit?.request ? NOTICE_ROW : 0;
	const bodyHeight = Math.max(60, height - INLINE_HEADER - notice);
	const preview = expanded ? (
		<Suspense fallback={<Loader size="sm" />}>
			<FilePreview narratorId={narratorId} file={file} />
		</Suspense>
	) : null;
	return (
		<Box h={height} style={{ overflow: "hidden" }}>
			<Group h={INLINE_HEADER} px="xs" gap="xs" wrap="nowrap" justify="space-between">
				<Text fw={600} size="xs" truncate title={title} style={{ minWidth: 0 }}>
					{title}
				</Text>
				<Group gap={4} wrap="nowrap" style={{ flexShrink: 0 }}>
					<Button
						size="compact-xs"
						variant="subtle"
						onClick={() => {
							// Run again from the files as they are now.
							setRunning(true);
							setRun((value) => value + 1);
						}}
					>
						{t(running ? "genUi.reload" : "genUi.run")}
					</Button>
					<Button size="compact-xs" variant="subtle" onClick={() => setExpanded(true)}>
						{t("genUi.expand")}
					</Button>
				</Group>
			</Group>
			{kit?.request && (
				<Box px="xs">
					<Suspense fallback={<Box h={NOTICE_ROW} />}>
						<KitNoticeBar narratorId={narratorId} notice={kit} />
					</Suspense>
				</Box>
			)}
			{running ? (
				<Suspense fallback={null}>
					<AppFrame key={run} narratorId={narratorId} file={file} height={bodyHeight} />
				</Suspense>
			) : (
				<Box px="xs" h={bodyHeight}>
					<Text size="xs" c="dimmed" truncate title={path}>
						{path}
					</Text>
					<Text size="xs" c={historyUnavailable ? "orange" : "dimmed"}>
						{t(historyUnavailable ? "genUi.historyUnavailable" : "genUi.notRunning")}
					</Text>
				</Box>
			)}
			{expanded &&
				(mobile ? (
					<Drawer
						opened
						onClose={() => setExpanded(false)}
						title={title}
						position="bottom"
						size="90%"
					>
						{preview}
					</Drawer>
				) : (
					<Modal opened onClose={() => setExpanded(false)} title={title} size="90%">
						{preview}
					</Modal>
				))}
		</Box>
	);
}
