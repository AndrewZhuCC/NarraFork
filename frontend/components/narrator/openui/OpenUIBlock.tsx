import "./openui-browser-compat";
import { copyTextToClipboard } from "@frontend/lib/clipboard";
import {
	Box,
	Button,
	Group,
	Modal,
	SegmentedControl,
	Text,
	useComputedColorScheme,
} from "@mantine/core";
import { notifications } from "@mantine/notifications";
import {
	type ActionEvent,
	type ComponentRenderProps,
	type Library,
	type OpenUIError,
	type ParseResult,
	Renderer,
	useFormName,
} from "@openuidev/react-lang";
import { ThemeProvider } from "@openuidev/react-ui";
import { narratorOpenUIChatLibrary as openuiChatLibrary } from "./openui-library";
import "@openuidev/react-ui/layered/styles/index.css";
import { typographyMetrics } from "@shared/pretext-layout/pretext-fonts";
import { onTypographyChange } from "@shared/pretext-layout/typography";
import {
	Component,
	memo,
	type ReactNode,
	type RefObject,
	useCallback,
	useEffect,
	useId,
	useMemo,
	useRef,
	useState,
	useSyncExternalStore,
} from "react";
import { useTranslation } from "react-i18next";
import {
	buildOpenUIActionMessage,
	createOpenUIStateCache,
	MAX_OPENUI_FIELDS,
	MAX_OPENUI_SESSIONS,
	MAX_OPENUI_SOURCE_BYTES,
	OPENUI_FIELD_TYPES,
	type OpenUIControlEvidence,
	openUIFieldKey,
	projectOpenUIFormValues,
	truncateUtf8,
	withinUtf8Limit,
} from "./openui-helpers";
import { OpenUIMarkdownProvider } from "./openui-markdown";
import { openUITypographyTheme } from "./openui-typography";

export interface OpenUIBlockProps {
	source: string;
	isStreaming: boolean;
	stateKey: string;
	onSend?: (message: string) => Promise<unknown>;
}

// Keep every stock component; annotate form controls without replacing their behavior.
const stockComponents: Library["components"] = openuiChatLibrary.components;
const narratorOpenUILibrary: Library = {
	...openuiChatLibrary,
	components: Object.fromEntries(
		Object.entries(stockComponents).map(([name, definition]) => {
			if (!OPENUI_FIELD_TYPES.has(name)) return [name, definition];
			const Original = definition.component;
			return [
				name,
				{
					...definition,
					component: function OpenUIField(args: ComponentRenderProps<Record<string, unknown>>) {
						const formName = useFormName();
						const fieldName = typeof args.props.name === "string" ? args.props.name : undefined;
						return (
							<div
								data-nf-openui-field={fieldName}
								data-nf-openui-form={name === "EditableTable" ? fieldName : formName}
								data-nf-openui-type={name}
							>
								<Original {...args} />
							</div>
						);
					},
				},
			];
		}),
	),
};
const stateCache = createOpenUIStateCache();
type SendStatus = "sending" | "sent" | "sendFailed";
const sendStatuses = new Map<string, SendStatus>();
const sendListeners = new Set<() => void>();
function subscribeSendStatus(listener: () => void) {
	sendListeners.add(listener);
	return () => sendListeners.delete(listener);
}
function setSendStatus(key: string, status: SendStatus) {
	sendStatuses.delete(key);
	sendStatuses.set(key, status);
	for (const listener of sendListeners) listener();
}
function beginSend(key: string): boolean {
	if (sendStatuses.get(key) === "sending") return false;
	if (!key || key.length > 1024) return false;
	if (!sendStatuses.has(key) && sendStatuses.size >= MAX_OPENUI_SESSIONS) {
		const oldestSettled = [...sendStatuses].find(([, status]) => status !== "sending")?.[0];
		if (oldestSettled === undefined) return false;
		sendStatuses.delete(oldestSettled);
	}
	setSendStatus(key, "sending");
	return true;
}

function useCoalescedSource(source: string, isStreaming: boolean) {
	const [coalesced, setCoalesced] = useState(source);
	const latest = useRef(source);
	const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
	useEffect(() => {
		latest.current = source;
		if (!isStreaming) {
			if (timer.current !== null) clearTimeout(timer.current);
			timer.current = null;
			setCoalesced(source);
		} else if (timer.current === null) {
			timer.current = setTimeout(() => {
				timer.current = null;
				setCoalesced(latest.current);
			}, 100);
		}
	}, [source, isStreaming]);
	useEffect(
		() => () => {
			if (timer.current !== null) clearTimeout(timer.current);
			timer.current = null;
			latest.current = "";
		},
		[],
	);
	// Completion uses the final source immediately, never a stale partial program.
	return isStreaming ? coalesced : source;
}

class OpenUIErrorBoundary extends Component<
	{ children: ReactNode; resetKey: string; onError: (message: string) => void },
	{ failed: boolean; resetKey: string }
> {
	state = { failed: false, resetKey: this.props.resetKey };
	static getDerivedStateFromError() {
		return { failed: true };
	}
	static getDerivedStateFromProps(props: { resetKey: string }, state: { resetKey: string }) {
		return props.resetKey === state.resetKey ? null : { failed: false, resetKey: props.resetKey };
	}
	componentDidCatch(error: Error) {
		this.props.onError(truncateUtf8(error.message, 256));
	}
	render() {
		return this.state.failed ? null : this.props.children;
	}
}

function collectVisibleControls(
	root: HTMLElement | null,
):
	| { ok: true; controls: Map<string, OpenUIControlEvidence> }
	| { ok: false; reason: "unsupportedForm" | "messageTooLarge" } {
	if (!root) return { ok: false, reason: "unsupportedForm" };
	const controls = new Map<string, OpenUIControlEvidence>();
	for (const control of root.querySelectorAll<HTMLElement>("[data-nf-openui-field]")) {
		const name = control.dataset.nfOpenuiField;
		if (!name || name.length > 256) return { ok: false, reason: "unsupportedForm" };
		const key = openUIFieldKey(control.dataset.nfOpenuiForm, name);
		if (controls.has(key)) return { ok: false, reason: "unsupportedForm" };
		const style = getComputedStyle(control);
		const rect = control.getBoundingClientRect();
		const evidence: OpenUIControlEvidence = {
			label:
				truncateUtf8(
					control.closest(".openui-form-control")?.querySelector("label")?.textContent ?? name,
					256,
				).trim() || name,
			visible:
				!control.closest("[hidden], [aria-hidden='true']") &&
				rect.width > 0 &&
				rect.height > 0 &&
				style.visibility !== "hidden" &&
				style.opacity !== "0",
		};
		if (control.dataset.nfOpenuiType === "Input" || control.dataset.nfOpenuiType === "TextArea") {
			const input = control.querySelector<HTMLInputElement | HTMLTextAreaElement>(
				"input, textarea",
			);
			if (input?.type === "hidden") evidence.visible = false;
			else if (input) evidence.value = input.value;
		}
		controls.set(key, evidence);
		if (controls.size > MAX_OPENUI_FIELDS) return { ok: false, reason: "messageTooLarge" };
	}
	return { ok: true, controls };
}

const OpenUIPreview = memo(function OpenUIPreview({
	source,
	isStreaming,
	stateKey,
	rootRef,
	onAction,
	onError,
	onParseResult,
	onStateUpdate,
}: {
	source: string;
	isStreaming: boolean;
	stateKey: string;
	rootRef: RefObject<HTMLDivElement | null>;
	onAction: (event: ActionEvent) => void;
	onError: (errors: OpenUIError[]) => void;
	onParseResult: (result: ParseResult | null) => void;
	onStateUpdate: (state: Record<string, unknown>) => void;
}) {
	const [initialState] = useState(() => stateCache.load(stateKey));
	return (
		<OpenUIMarkdownProvider rootRef={rootRef}>
			<Renderer
				response={source}
				isStreaming={isStreaming}
				library={narratorOpenUILibrary}
				initialState={initialState}
				onStateUpdate={onStateUpdate}
				onParseResult={onParseResult}
				onAction={onAction}
				onError={onError}
				toolProvider={null}
				publishObservability={false}
			/>
		</OpenUIMarkdownProvider>
	);
});

function OpenUIBlockContent({ source, isStreaming, stateKey, onSend }: OpenUIBlockProps) {
	const { t } = useTranslation("narrator");
	const colorScheme = useComputedColorScheme("dark");
	const typography = useSyncExternalStore(onTypographyChange, typographyMetrics, typographyMetrics);
	const typographyTheme = useMemo(() => openUITypographyTheme(typography), [typography]);
	const themeId = useId().replace(/[^a-zA-Z0-9_-]/g, "");
	const [view, setView] = useState("preview");
	const [expanded, setExpanded] = useState(false);
	const [actionError, setActionError] = useState<string | null>(null);
	const [renderError, setRenderError] = useState<{ source: string; detail: string } | null>(null);
	const rootRef = useRef<HTMLDivElement | null>(null);
	const inlineRef = useRef<HTMLDivElement | null>(null);
	const [inlineHeight, setInlineHeight] = useState(0);
	const [cachedState] = useState(() => stateCache.load(stateKey));
	const snapshotRef = useRef(cachedState);
	const parseRef = useRef<{ source: string; result: ParseResult | null } | null>(null);
	const stateTooLarge = useRef(false);
	const recordState = useCallback(
		(state: Record<string, unknown>) => {
			if (!stateKey) return;
			const saved = stateCache.save(stateKey, state);
			snapshotRef.current = saved ? state : undefined;
			stateTooLarge.current = !saved;
		},
		[stateKey],
	);
	const bounded = useMemo(() => {
		const tooLarge = !withinUtf8Limit(source, MAX_OPENUI_SOURCE_BYTES);
		return {
			tooLarge,
			source: tooLarge ? truncateUtf8(source, MAX_OPENUI_SOURCE_BYTES) : source,
		};
	}, [source]);
	const renderSource = useCoalescedSource(bounded.tooLarge ? "" : bounded.source, isStreaming);
	const recordParse = useCallback(
		(result: ParseResult | null) => {
			parseRef.current = { source: renderSource, result };
		},
		[renderSource],
	);
	const sendStatus = useSyncExternalStore(
		subscribeSendStatus,
		() => sendStatuses.get(stateKey),
		() => undefined,
	);
	const reportError = useCallback(
		(detail: string) => setRenderError({ source: renderSource, detail }),
		[renderSource],
	);
	const handleErrors = useCallback(
		(errors: OpenUIError[]) => {
			setRenderError(
				errors.length
					? { source: renderSource, detail: truncateUtf8(errors[0].message, 256) }
					: null,
			);
		},
		[renderSource],
	);
	const handleAction = useCallback(
		async (event: ActionEvent) => {
			if (event.type !== "continue_conversation") {
				setActionError("actionBlocked");
				return;
			}
			if (isStreaming) return;
			if (!onSend) {
				setActionError("readOnly");
				return;
			}
			if (sendStatuses.get(stateKey) === "sending") return;
			if (stateTooLarge.current) {
				setActionError("messageTooLarge");
				return;
			}
			const evidence = collectVisibleControls(rootRef.current);
			if (!evidence.ok) {
				setActionError(evidence.reason);
				return;
			}
			const parsed = parseRef.current;
			if (!parsed || parsed.source !== renderSource) {
				setActionError("unsupportedForm");
				return;
			}
			const projection = projectOpenUIFormValues(
				parsed.result,
				snapshotRef.current,
				event,
				evidence.controls,
			);
			if (!projection.ok) {
				setActionError(projection.reason);
				return;
			}
			const result = buildOpenUIActionMessage(event, projection.values);
			if (!result.ok) {
				setActionError(result.reason);
				return;
			}
			if (!beginSend(stateKey)) {
				setActionError("sendFailed");
				return;
			}
			setActionError(null);
			try {
				await onSend(result.message);
				setSendStatus(stateKey, "sent");
			} catch {
				setSendStatus(stateKey, "sendFailed");
			}
		},
		[isStreaming, onSend, renderSource, stateKey],
	);

	const copyText = async (text: string) => {
		try {
			await copyTextToClipboard(text);
			notifications.show({ id: "openui-copy", message: t("openui.copied"), autoClose: 1500 });
		} catch {
			notifications.show({ id: "openui-copy", message: t("openui.copyFailed"), color: "red" });
		}
	};
	const content = (
		<Box
			ref={expanded ? undefined : inlineRef}
			data-nf-openui={themeId}
			onClickCapture={(event) => {
				const target = event.target instanceof Element ? event.target : null;
				const button = target?.closest(".openui-code-block-copy-button");
				const code = button?.closest(".openui-code-block-wrapper")?.querySelector("code");
				if (!code) return;
				// ponytail: the SDK has no copy callback and assumes HTTPS. Keep its
				// renderer, but use NF's fallback until the vendor handles HTTP copy.
				event.preventDefault();
				event.stopPropagation();
				void copyText(code.textContent ?? "");
			}}
			// Leave the transcript row's floating action bar its own hit area.
			pt={expanded ? 0 : 28}
			style={{ minWidth: 0, maxWidth: "100%" }}
		>
			<Group justify="space-between" gap="xs" mb="xs">
				<SegmentedControl
					size="xs"
					styles={{
						root: { padding: 2 },
						label: { padding: "0 6px", fontSize: 11, lineHeight: "20px" },
					}}
					value={view}
					onChange={setView}
					data={[
						{ value: "preview", label: t("openui.preview") },
						{ value: "source", label: t("openui.source") },
					]}
				/>
				<Group gap="xs">
					<Button size="compact-xs" variant="subtle" onClick={() => void copyText(bounded.source)}>
						{t("openui.copySource")}
					</Button>
					{!expanded && (
						<Button
							size="compact-xs"
							variant="subtle"
							onClick={() => {
								setInlineHeight(inlineRef.current?.getBoundingClientRect().height ?? 0);
								setExpanded(true);
							}}
						>
							{t("openui.expand")}
						</Button>
					)}
				</Group>
			</Group>
			{(!onSend || isStreaming) && (
				<Text size="xs" c="dimmed" mb="xs">
					{t(isStreaming ? "openui.streaming" : "openui.readOnly")}
				</Text>
			)}
			{(actionError || sendStatus) && (
				<Text
					size="xs"
					c={actionError || sendStatus === "sendFailed" ? "red" : "dimmed"}
					mb="xs"
					role="status"
				>
					{t(`openui.${actionError ?? sendStatus}`)}
				</Text>
			)}
			{renderError?.source === renderSource && (
				<Text size="xs" c="red" mb="xs" role="alert">
					{t("openui.renderError")} {renderError.detail}
				</Text>
			)}
			{bounded.tooLarge && (
				<Text size="xs" c="red" mb="xs" role="alert">
					{t("openui.sourceTooLarge")} {view === "source" && t("openui.sourceTruncated")}
				</Text>
			)}
			<Box
				style={{
					maxHeight: expanded ? "75vh" : undefined,
					overflow: expanded ? "auto" : "visible",
					contain: "layout",
				}}
			>
				{view === "source" ? (
					<Text
						component="pre"
						size="xs"
						m={0}
						style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere", fontFamily: "monospace" }}
					>
						{bounded.source}
					</Text>
				) : !bounded.tooLarge ? (
					<OpenUIErrorBoundary resetKey={renderSource} onError={reportError}>
						<OpenUIPreview
							source={renderSource}
							isStreaming={isStreaming}
							stateKey={stateKey}
							rootRef={rootRef}
							onAction={handleAction}
							onError={handleErrors}
							onParseResult={recordParse}
							onStateUpdate={recordState}
						/>
					</OpenUIErrorBoundary>
				) : null}
			</Box>
		</Box>
	);
	return (
		<ThemeProvider
			mode={colorScheme}
			lightTheme={typographyTheme}
			cssSelector={`[data-nf-openui="${themeId}"]`}
		>
			<Box data-nf-openui={themeId} style={{ minWidth: 0, maxWidth: "100%" }}>
				{expanded ? <Box h={inlineHeight} /> : content}
				<Modal
					opened={expanded}
					onClose={() => setExpanded(false)}
					title={t("openui.title")}
					size="xl"
				>
					{expanded && content}
				</Modal>
			</Box>
		</ThemeProvider>
	);
}

export default function OpenUIBlock(props: OpenUIBlockProps) {
	return <OpenUIBlockContent key={props.stateKey} {...props} />;
}
