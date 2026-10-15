import { Alert, Box, Button, Group, Text } from "@mantine/core";
import {
	createContext,
	type ReactNode,
	useContext,
	useEffect,
	useMemo,
	useRef,
	useState,
	useSyncExternalStore,
} from "react";
import { useTranslation } from "react-i18next";
import { ApiError } from "../../../lib/api/client";
import { saveBlobAsFile } from "../../../lib/file-download";
import { filePanelBaseName } from "../panels/panel-kind";
import { useEditorAuthority } from "./editor-authority";
import { editorDocumentRegistry } from "./editor-document-registry";
import type { EditorSessionState } from "./editor-session-state";
import { captureEditorSnapshot, encodeEditorSnapshot } from "./editor-worker-client";
import type { EditorWorkingCopy, WorkingCopyOptions } from "./editor-working-copy";

export interface EditorRecoveryScopeProps {
	filePath: string;
	narratorId?: string;
	deviceId?: string;
	referenceOrigin?: boolean;
}
export function editorRecoveryScopeIdentity(props: EditorRecoveryScopeProps) {
	return JSON.stringify([
		props.narratorId ?? "",
		props.deviceId ?? "local",
		props.filePath,
		!!props.referenceOrigin,
	]);
}
function exclusionSlot() {
	let resolve!: (copy: EditorWorkingCopy | null | PromiseLike<EditorWorkingCopy | null>) => void;
	const exclude = new Promise<EditorWorkingCopy | null>((done) => {
		resolve = done;
	});
	return {
		exclude,
		resolve,
		kind: "pending" as "pending" | "view" | "blocked",
		token: null as symbol | null,
	};
}
/** Coordinates only lifecycle metadata. Neither gate status nor this context grants file access. */
class RecoveryHost {
	private slot = exclusionSlot();
	private snapshot = { epoch: 0, exclude: this.slot.exclude };
	private listeners = new Set<() => void>();
	private gateAttempt: number | undefined;
	constructor(readonly identity: string) {}
	subscribe = (listener: () => void) => {
		this.listeners.add(listener);
		return () => {
			this.listeners.delete(listener);
		};
	};
	getSnapshot = () => this.snapshot;
	private replace() {
		this.slot = exclusionSlot();
		this.snapshot = { epoch: this.snapshot.epoch + 1, exclude: this.slot.exclude };
		for (const listener of this.listeners) listener();
	}
	registerViewing(viewing: Promise<EditorWorkingCopy | null>) {
		if (this.slot.kind !== "pending") this.replace();
		const token = Symbol("editor-view");
		this.slot.kind = "view";
		this.slot.token = token;
		this.slot.resolve(viewing);
		return () => {
			if (this.slot.token === token) this.slot.token = null;
		};
	}
	gate(kind: "pending" | "editor" | "blocked", attempt: number) {
		if (
			kind === "pending" &&
			this.gateAttempt !== undefined &&
			this.gateAttempt !== attempt &&
			this.slot.kind === "blocked"
		)
			this.replace();
		this.gateAttempt = attempt;
		if (kind !== "blocked" || this.slot.kind === "blocked") return;
		if (this.slot.kind === "view") this.replace();
		this.slot.kind = "blocked";
		this.slot.resolve(null);
	}
}
const RecoveryHostContext = createContext<RecoveryHost | null>(null);
export function useEditorRecoveryHost(props: EditorRecoveryScopeProps) {
	const host = useContext(RecoveryHostContext);
	return host?.identity === editorRecoveryScopeIdentity(props) ? host : null;
}

/** Stable outside metadata/size gates, including when the editable child never mounts. */
export function EditorRecoveryBoundary(props: EditorRecoveryScopeProps & { children: ReactNode }) {
	const { t } = useTranslation("narrator");
	const principal = useEditorAuthority(props.narratorId, t("fileEditor.identityChanged"));
	// Origin updates retain the host DOM; the independently keyed coordinator and editor
	// binding still forbid migrating an old draft to a different authorization origin.
	return (
		<RecoveryBoundaryIdentity
			key={JSON.stringify([principal, props.narratorId, props.deviceId ?? "local", props.filePath])}
			{...props}
		/>
	);
}
function RecoveryBoundaryIdentity({
	filePath,
	narratorId,
	deviceId = "local",
	referenceOrigin = false,
	children,
}: EditorRecoveryScopeProps & { children: ReactNode }) {
	const { t } = useTranslation("narrator");
	const latest = useRef(t);
	latest.current = t;
	const identity = editorRecoveryScopeIdentity({ filePath, narratorId, deviceId, referenceOrigin });
	const host = useMemo(() => new RecoveryHost(identity), [identity]);
	const request = useSyncExternalStore(host.subscribe, host.getSnapshot, host.getSnapshot);
	const [retained, setRetained] = useState<readonly EditorWorkingCopy[]>([]);
	const [error, setError] = useState<string | null>(null);
	const [publishedFor, setPublishedFor] = useState<typeof request | null>(null);
	useEffect(() => {
		setRetained([]);
		setError(null);
		setPublishedFor(null);
		const controller = new AbortController();
		let release: (() => void) | undefined;
		const options: WorkingCopyOptions = {
			narratorId: narratorId ?? "",
			path: filePath,
			deviceId,
			origin: () => (referenceOrigin ? "reference" : "legacy"),
			translate: (key, defaultValue) => latest.current(`fileEditor.${key}`, { defaultValue }),
		};
		void editorDocumentRegistry
			.discoverRetained(options, controller.signal, request.exclude)
			.then((lease) => {
				if (controller.signal.aborted) {
					lease.release();
					return;
				}
				release = lease.release;
				setRetained(lease.retainedCopies);
				setPublishedFor(request);
			})
			.catch((cause) => {
				if (controller.signal.aborted) return;
				setRetained([]);
				setError(cause instanceof Error ? cause.message : String(cause));
				setPublishedFor(request);
				if (cause instanceof ApiError && [401, 403, 404].includes(cause.status) && narratorId)
					editorDocumentRegistry.invalidateNarrator(
						narratorId,
						latest.current("fileEditor.identityChanged"),
					);
			});
		return () => {
			controller.abort();
			release?.();
		};
	}, [narratorId, filePath, deviceId, referenceOrigin, request]);
	return (
		<RecoveryHostContext.Provider value={host}>
			<Box style={{ height: "100%", display: "flex", flexDirection: "column", minHeight: 0 }}>
				{publishedFor === request &&
					retained.map((copy) => <RetainedEditorCopy key={copy.lifetimeId} copy={copy} />)}
				{publishedFor === request && error && (
					<Text c="red" size="xs" px="xs">
						{error}
					</Text>
				)}
				<Box style={{ flex: 1, minHeight: 0 }}>{children}</Box>
			</Box>
		</RecoveryHostContext.Provider>
	);
}

export function EditorReceiptNotice({ state }: { state: EditorSessionState }) {
	const { t } = useTranslation("narrator");
	const receipt = state.receipt;
	if (
		!receipt ||
		(receipt.status !== "saved" && receipt.status !== "failed" && receipt.status !== "not-applied")
	)
		return null;
	return (
		<Text size="xs" data-editor-retired-receipt={receipt.status}>
			{receipt.status === "saved"
				? t("fileEditor.receiptSaved")
				: receipt.status === "not-applied"
					? t("fileEditor.receiptNotApplied")
					: t("fileEditor.receiptFailed", {
							message: receipt.error?.message ?? receipt.error?.code ?? "",
						})}
		</Text>
	);
}
/** Kept separate from the newly authorized editor: neither querying nor exporting installs its draft. */
function RetainedEditorCopy({ copy }: { copy: EditorWorkingCopy }) {
	const { t } = useTranslation("narrator");
	const state = useSyncExternalStore(copy.subscribe, copy.getSnapshot, copy.getSnapshot);
	const [exporting, setExporting] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const controller = useRef<AbortController | null>(null);
	useEffect(() => () => controller.current?.abort(), []);
	const download = async () => {
		if (!copy.model || exporting) return;
		const pending = new AbortController();
		controller.current = pending;
		setExporting(true);
		setError(null);
		try {
			const blob = await encodeEditorSnapshot(captureEditorSnapshot(copy.model), pending.signal);
			pending.signal.throwIfAborted();
			saveBlobAsFile(blob, `${filePanelBaseName(copy.options.path)}.retained-draft.utf8.txt`);
		} catch (cause) {
			if (!pending.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause));
		} finally {
			if (!pending.signal.aborted) setExporting(false);
		}
	};
	const discard = () => {
		if (!window.confirm(t("fileEditor.confirmDiscardRetained"))) return;
		if (!editorDocumentRegistry.discardRetained(copy)) setError(t("fileEditor.closeRetainedView"));
	};
	if (state.discarded) return null;
	return (
		<Alert
			color="yellow"
			radius={0}
			title={t("fileEditor.retainedDraftTitle")}
			data-editor-retained-draft
		>
			<Text size="xs">{t("fileEditor.retainedDraftBody")}</Text>
			<EditorReceiptNotice state={state} />
			{(state.receiptError || error) && (
				<Text size="xs" c="red">
					{state.receiptError || error}
				</Text>
			)}
			<Group gap="xs" mt="xs">
				{state.phase === "unknown" && (
					<Button
						size="xs"
						loading={!!state.checkingReceipt}
						onClick={() => void copy.session.reconcile()}
					>
						{t("fileEditor.checkSave")}
					</Button>
				)}
				<Button
					size="xs"
					variant="default"
					loading={exporting}
					disabled={!copy.model}
					onClick={() => void download()}
				>
					{t("fileEditor.downloadRetainedDraft")}
				</Button>
				<Button
					size="xs"
					variant="subtle"
					color="red"
					disabled={state.phase !== "idle" || !!state.checkingReceipt || exporting}
					onClick={discard}
				>
					{t("fileEditor.discardRetainedDraft")}
				</Button>
			</Group>
		</Alert>
	);
}
