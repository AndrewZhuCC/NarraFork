import "@mantine/core/styles.css";
import { ImageViewerContext } from "@frontend/components/common/image-viewer-context";
import common from "@frontend/locales/en/common.json";
import narrator from "@frontend/locales/en/narrator.json";
import { MantineProvider } from "@mantine/core";
import { type ComposerDocument, composerDocumentText } from "@shared/composer-document";
import type { FileReferenceEditorSelection } from "@shared/file-reference";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Editor } from "@tiptap/react";
import i18next from "i18next";
import { createRef, useCallback, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { createRoot } from "react-dom/client";
import { I18nextProvider, initReactI18next } from "react-i18next";
import { useComposerFileIngest } from "../interaction/use-composer-file-ingest";
import { getComposerAssetId } from "./draft-image-attachments";
import { FileReferenceScopeProvider } from "./FileReferenceScope";
import type { NarratorComposerHandle } from "./NarratorComposer";
import { NarratorComposerRow, type NarratorComposerRowProps } from "./NarratorComposerRow";

const i18n = i18next.createInstance();
await i18n
	.use(initReactI18next)
	.init({ lng: "en", resources: { en: { narrator, common } }, react: { useSuspense: false } });
const query = new QueryClient({
	defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
});
query.setQueryData(["auth", "me"], { id: "fixture-user" });
query.setQueryData(["named-narrators"], []);
const composer = createRef<NarratorComposerHandle>();
const fileInput = createRef<HTMLInputElement>();
const sending = { current: false };
const noop = () => {};
let parentRenders = 0;
let sends = 0;
let updateFiles: (images: File[], textFiles: File[]) => void;
let updateSending: (value: boolean) => void;
let updateTakeover: (value: boolean) => void;
let ingestFiles: ReturnType<typeof useComposerFileIngest>["ingestFiles"];
let pendingFiles: Promise<File[]> | undefined;
let currentFiles: File[] = [];
let updateSourceSelection: (selection: FileReferenceEditorSelection | null) => void;
const defaults = {
	fileInputRef: fileInput,
	composerRef: composer,
	sendingRef: sending,
	appendInputRef: undefined,
	narratorId: "fixture-narrator",
	isActive: false,
	effectiveFocusIndex: null,
	enterQueueMode: "turn",
	ctrlEnterQueueMode: "interrupt",
	onFileInputChange: noop,
	onComposerPasteImages: noop,
	onSendWithMode: () => {
		sends++;
	},
	onSend: () => {
		sends++;
	},
	onRetry: noop,
	onContinue: noop,
	onTakeover: noop,
	onStopTakeover: noop,
	queuedMessagesCount: 0,
	showCompactQueueChoice: false,
	canCutInLine: false,
	hasCutInMessage: false,
	canTakeover: false,
	isTakenOver: false,
	canRetryLastUserMessage: false,
	canContinueNarrator: false,
	retryRecoveryAllowsInterrupt: false,
	takeoverMutationPending: false,
	stopTakeoverMutationPending: false,
	interruptMutationPending: false,
	interruptProgress: 0,
	queueHoldProgress: 0,
	startInterruptPress: noop,
	handleInterruptMouseUp: noop,
	clearInterruptTimer: noop,
	startQueueHold: noop,
	handleQueuePointerUp: noop,
	cancelQueueHold: noop,
	handleQueueClick: noop,
	onUpdateEnterQueueMode: noop,
	onUpdateCtrlEnterQueueMode: noop,
} satisfies Omit<
	NarratorComposerRowProps,
	"composerHasText" | "composerHasAttachments" | "setComposerHasText" | "isSending"
>;
function PanelBoundary({ showFileSource = false }: { showFileSource?: boolean }) {
	parentRenders++;
	const [hasText, setHasText] = useState(false);
	const [images, setImages] = useState<File[]>([]);
	const [textFiles, setTextFiles] = useState<File[]>([]);
	const [isSending, setIsSending] = useState(false);
	const [takenOver, setTakenOver] = useState(false);
	updateTakeover = setTakenOver;
	const changeFiles = useCallback((nextImages: File[], nextTextFiles: File[]) => {
		setImages(nextImages);
		setTextFiles(nextTextFiles);
	}, []);
	updateFiles = changeFiles;
	updateSending = setIsSending;
	currentFiles = [...images, ...textFiles];
	const dragCounterRef = useRef(0);
	const ingest = useComposerFileIngest({
		narratorId: "fixture-narrator",
		sendingRef: sending,
		updateAttachedImages: setImages,
		updateAttachedTextFiles: setTextFiles,
		setIsDragging: noop,
		dragCounterRef,
		t: (key) => key,
		captureInsertion: (origin) => composer.current?.captureAttachmentInsertion(origin),
		onAcceptedFiles: (files, bookmark) =>
			composer.current?.insertAcceptedFiles(files, bookmark) ?? false,
	});
	ingestFiles = ingest.ingestFiles;
	return (
		<div
			data-narrator-panel
			id="panel"
			style={{
				height: "100dvh",
				maxWidth: 880,
				margin: "auto",
				padding: 16,
				display: "flex",
				flexDirection: "column",
				boxSizing: "border-box",
			}}
		>
			<div id="messages" style={{ flex: 1, minHeight: 0, overflow: "auto", padding: 12 }}>
				<div data-content-block data-message-id="message-source" data-quote-role="assistant">
					<p id="quote-source">
						Select this source text to quote without changing the message layout.
					</p>
				</div>
				{showFileSource && (
					<div className="monaco-editor">
						<textarea
							id="file-selection-source"
							aria-label="Selected file source"
							defaultValue="source text"
						/>
					</div>
				)}
				<div style={{ height: 600 }}>Scrollable message history</div>
			</div>
			<div id="row">
				<NarratorComposerRow
					{...defaults}
					canTakeover={takenOver}
					isTakenOver={takenOver}
					onFileInputChange={ingest.handleFileInputChange}
					onComposerPasteImages={ingest.handleComposerPasteImages}
					composerHasText={hasText}
					composerHasAttachments={images.length + textFiles.length > 0}
					setComposerHasText={setHasText}
					attachedImages={images}
					attachedTextFiles={textFiles}
					onAttachmentsChange={changeFiles}
					isSending={isSending}
				/>
			</div>
		</div>
	);
}
function ScopedFixture() {
	const [selection, setSelection] = useState<FileReferenceEditorSelection | null>(null);
	updateSourceSelection = setSelection;
	return (
		<FileReferenceScopeProvider
			value={{
				narratorId: "fixture-narrator",
				context: { deviceId: "local", cwd: "/work" },
				selection,
			}}
		>
			<PanelBoundary showFileSource={!!selection} />
		</FileReferenceScopeProvider>
	);
}
createRoot(document.getElementById("root") as HTMLElement).render(
	<QueryClientProvider client={query}>
		<MantineProvider defaultColorScheme="dark">
			<I18nextProvider i18n={i18n}>
				<ImageViewerContext.Provider value={{ open: noop }}>
					<ScopedFixture />
				</ImageViewerContext.Provider>
			</I18nextProvider>
		</MantineProvider>
	</QueryClientProvider>,
);
export function ready() {
	return !!composer.current && !!document.querySelector('[role="textbox"]');
}
export function snapshot() {
	return {
		text: composer.current?.getText(),
		document: composer.current?.getDocument(),
		missing: composer.current?.hasMissingAssets(),
		parentRenders,
		sends,
		files: currentFiles.map((file) => file.name),
	};
}
export function selectSource() {
	const source = document.getElementById("quote-source");
	if (!source?.firstChild) throw new Error("Missing quote source");
	const range = document.createRange();
	range.selectNodeContents(source);
	window.getSelection()?.removeAllRanges();
	window.getSelection()?.addRange(range);
	document.dispatchEvent(new Event("selectionchange"));
}
export function clearSelection() {
	window.getSelection()?.removeAllRanges();
	document.dispatchEvent(new Event("selectionchange"));
}
export function stageMixed() {
	const pixel = Uint8Array.from(
		atob(
			"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j1ioAAAAASUVORK5CYII=",
		),
		(char) => char.charCodeAt(0),
	);
	const image = new File([pixel], "screenshot.png", { type: "image/png" });
	const textFile = new File(["console output"], "console.log", { type: "text/plain" });
	flushSync(() => updateFiles([image], [textFile]));
	const imageId = getComposerAssetId(image);
	const textId = getComposerAssetId(textFile);
	const doc: ComposerDocument = {
		version: 1,
		assets: [
			{ id: imageId, kind: "image", name: image.name, size: image.size, mimeType: image.type },
			{
				id: textId,
				kind: "textFile",
				name: textFile.name,
				size: textFile.size,
				mimeType: textFile.type,
			},
		],
		paragraphs: [
			{
				children: [
					{ type: "text", text: "Analyze " },
					{
						type: "quote",
						id: "quote",
						label: "Selected explanation",
						text: "This is the quoted context.",
					},
					{ type: "text", text: " with " },
					{ type: "attachment", id: "image", assetId: imageId },
					{ type: "text", text: " and " },
					{
						type: "fileReference",
						id: "file",
						reference: { id: "ref", deviceId: "local", path: "/work/App.tsx", label: "App.tsx" },
					},
					{ type: "attachment", id: "log", assetId: textId },
					{ type: "text", text: " for the answer." },
				],
			},
		],
	};
	load(doc);
	return doc;
}
export function load(doc: ComposerDocument) {
	const projection = composerDocumentText(doc);
	composer.current?.restoreInput(projection.text, projection.fileReferences, doc);
}
export function hide() {
	sending.current = true;
	composer.current?.hideTextForSend();
	flushSync(() => {
		updateFiles([], []);
		updateSending(true);
	});
}
export function restore(doc: ComposerDocument) {
	sending.current = false;
	flushSync(() => updateSending(false));
	load(doc);
}
export function commit() {
	composer.current?.commitDraftAfterSend();
}

export function ingestImage() {
	const pixel = Uint8Array.from(
		atob(
			"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j1ioAAAAASUVORK5CYII=",
		),
		(char) => char.charCodeAt(0),
	);
	pendingFiles = ingestFiles([new File([pixel], "async.png", { type: "image/png" })], "paste");
}
export function ingestText(name: string) {
	pendingFiles = ingestFiles([new File(["text"], name, { type: "text/plain" })], "direct");
}
export function awaitFiles() {
	return pendingFiles;
}
export function remote(doc: ComposerDocument, revision = 100) {
	const projection = composerDocumentText(doc);
	composer.current?.handleDraftChanged({
		hasDraft: true,
		...projection,
		composerDocument: doc,
		revision,
		updatedAt: new Date().toISOString(),
		updatedBy: "another-user",
		sourceId: "another-tab",
	});
}
export function remember(doc: ComposerDocument) {
	const projection = composerDocumentText(doc);
	composer.current?.noteSent(projection.text, projection.fileReferences, doc);
}
export function clear() {
	composer.current?.clearTextAndDraft();
}
export function namedCandidates(candidates: { id: string; handle: string; title?: string }[]) {
	query.setQueryData(["named-narrators"], candidates);
}

export function nativeSelection(from: number, to = from) {
	const element = document.querySelector<HTMLElement>("[role=textbox]") as HTMLElement & {
		editor: Editor;
	};
	element.editor.commands.setTextSelection({ from, to });
	element.editor.commands.focus();
}

export function publishFileSelection(dirty = false) {
	flushSync(() =>
		updateSourceSelection({
			target: {
				deviceId: "local",
				path: "/work/src.ts",
				selection: { startLineNumber: 1, startColumn: 1, endLineNumber: 1, endColumn: 7 },
			},
			label: "src.ts",
			expectedHash: "a".repeat(64),
			dirty,
		}),
	);
	const source = document.getElementById("file-selection-source") as HTMLTextAreaElement;
	source.focus();
	source.setSelectionRange(0, 6);
}

export function takeover(value: boolean) {
	updateTakeover(value);
}
