import "@mantine/core/styles.css";
import { MantineProvider } from "@mantine/core";
import i18next from "i18next";
import { flushSync } from "react-dom";
import { createRoot } from "react-dom/client";
import { I18nextProvider } from "react-i18next";
import { readEditorRawPage } from "../frontend/components/narrator/file-editor/EditorPagedView";
import {
	type EditorDocumentLease,
	editorDocumentRegistry,
} from "../frontend/components/narrator/file-editor/editor-document-registry";
import { editorCodecDiagnostics } from "../frontend/components/narrator/file-editor/editor-stream-decoder";
import {
	captureEditorSnapshot,
	getEditorWorkerDiagnostics,
	hashEditorSnapshot,
} from "../frontend/components/narrator/file-editor/editor-worker-client";
import { FileEditorContent } from "../frontend/components/narrator/file-editor/FileEditorContent";
import { getEditorViewDiagnostics } from "../frontend/components/narrator/file-editor/MonacoEditor";
import { loadMonaco } from "../frontend/components/narrator/file-editor/monaco-loader";
import { FilePanelContent } from "../frontend/components/narrator/file-panel/FilePanelContent";
import { NarratorPanelVisibilityProvider } from "../frontend/components/narrator/narrator-panel-visibility";
import en from "../frontend/locales/en/narrator.json";
import type { EditorRawEncoding } from "../shared/editor-document";

const config = (await (await fetch("/fixture-config")).json()) as { narratorId: string };
const api = await loadMonaco();
const i18n = i18next.createInstance();
await i18n.init({
	lng: "en",
	resources: { en: { narrator: en } },
	interpolation: { escapeValue: false },
});
const host = document.getElementById("root");
if (!host) throw new Error("Missing fixture host");
const root = createRoot(host);
let lease: EditorDocumentLease | undefined;
let extraLease: EditorDocumentLease | undefined;
let path = "";
let encodingHint: EditorRawEncoding | undefined;
let controller: AbortController | undefined;
const disposal: boolean[] = [];
const languages: string[] = [];
const longTasks: number[] = [];
const observer = new PerformanceObserver((list) => {
	for (const entry of list.getEntries())
		if (longTasks.length < 1000) longTasks.push(entry.duration);
});
observer.observe({ type: "longtask", buffered: true });
api.editor.onDidCreateModel((model) => {
	languages.push(model.getLanguageId());
	model.onWillDispose(() =>
		disposal.push(api.editor.getEditors().every((view) => view.getModel() !== model)),
	);
});
export function state() {
	return {
		session: lease?.copy.session.state,
		docId: lease?.copy.session.documentId,
		pageDocId: lease?.copy.pagedDocument?.docId,
		eol: lease?.copy.model?.getEOL(),
		modelLoad: lease?.copy.rawLoadStats,
		length: lease?.copy.model?.getValueLength() ?? 0,
		language: lease?.copy.model?.getLanguageId(),
		models: api.editor.getModels().length,
		registry: editorDocumentRegistry.diagnostics(),
		codecs: editorCodecDiagnostics(),
		searchMirrors: getEditorWorkerDiagnostics().pool.slots.filter((slot) => slot.docId).length,
		views: getEditorViewDiagnostics(),
		disposal: [...disposal],
		languages: [...languages],
		maxLongTaskMs: Math.max(0, ...longTasks),
		heap: (performance as unknown as { memory?: { usedJSHeapSize: number } }).memory
			?.usedJSHeapSize,
	};
}
export async function open(filePath: string, hint?: EditorRawEncoding) {
	await close();
	path = filePath;
	encodingHint = hint;
	controller = new AbortController();
	lease = await editorDocumentRegistry.acquire(
		{
			narratorId: config.narratorId,
			path,
			deviceId: "local",
			origin: () => "legacy",
			protocol: "raw-v2",
			encodingHint,
		},
		controller.signal,
	);
	await lease.copy.ensureLoaded();
	return state();
}
export function dualViews(visible = true) {
	lease?.setVisible(visible);
	flushSync(() =>
		root.render(
			<MantineProvider>
				<I18nextProvider i18n={i18n}>
					<NarratorPanelVisibilityProvider value={visible}>
						<div style={{ display: "flex", height: "100vh" }}>
							<div style={{ width: "50%" }}>
								<FileEditorContent
									filePath={path}
									narratorId={config.narratorId}
									rawProtocol
									rawEncoding={encodingHint}
								/>
							</div>
							<div style={{ width: "50%" }}>
								<FileEditorContent
									filePath={path}
									narratorId={config.narratorId}
									rawProtocol
									rawEncoding={encodingHint}
								/>
							</div>
						</div>
					</NarratorPanelVisibilityProvider>
				</I18nextProvider>
			</MantineProvider>,
		),
	);
}
export async function fingerprint() {
	const model = lease?.copy.model;
	return model
		? hashEditorSnapshot(captureEditorSnapshot(model), new AbortController().signal)
		: null;
}
export async function reload() {
	await lease?.copy.session.load(true);
	return state();
}
export function edit(text: string) {
	const model = lease?.copy.model;
	if (!model) throw new Error("No model");
	model.pushEditOperations(
		[],
		[{ range: { startLineNumber: 1, startColumn: 1, endLineNumber: 1, endColumn: 1 }, text }],
		() => null,
	);
}
export function undo() {
	lease?.copy.model?.undo();
}
export async function page() {
	const document = lease?.copy.pagedDocument;
	if (!document) throw new Error("No paged descriptor");
	return readEditorRawPage(config.narratorId, document, 0, new AbortController().signal);
}
export function windowsControl() {
	const model = api.editor.createModel("", "plaintext");
	const eol = model.getEOL();
	model.applyEdits(
		[
			{
				range: { startLineNumber: 1, startColumn: 1, endLineNumber: 1, endColumn: 1 },
				text: "a\nb",
			},
		],
		false,
	);
	const length = model.getValueLength();
	model.dispose();
	return { eol, length };
}
export async function refresh() {
	await lease?.copy.session.refreshDocument();
	return state();
}
function panelViews(rawOnly: boolean, a = true, b = true, secondary = true) {
	flushSync(() =>
		root.render(
			<MantineProvider>
				<I18nextProvider i18n={i18n}>
					<div style={{ display: "flex", height: "100vh" }}>
						<div style={{ width: "50%" }}>
							<FilePanelContent
								key={rawOnly ? "raw-a" : "v1"}
								filePath={path}
								narratorId={config.narratorId}
								confirmed
								visible={a}
							/>
						</div>
						{secondary && (
							<div style={{ width: "50%" }}>
								<FilePanelContent
									key={rawOnly ? "raw-b" : "raw"}
									filePath={path}
									narratorId={config.narratorId}
									confirmed
									visible={b}
								/>
							</div>
						)}
					</div>
				</I18nextProvider>
			</MantineProvider>,
		),
	);
}
export async function mixedStart(filePath: string) {
	await close();
	localStorage.removeItem("narrafork_editor_raw_v2");
	path = filePath;
	controller = new AbortController();
	lease = await editorDocumentRegistry.acquire(
		{ narratorId: config.narratorId, path, deviceId: "local", origin: () => "legacy" },
		controller.signal,
	);
	await lease.copy.ensureLoaded();
	panelViews(false, true, true, false);
}
export function mixedAddRaw() {
	localStorage.setItem("narrafork_editor_raw_v2", "1");
	panelViews(false);
}
export async function mixedInspect() {
	if (!extraLease)
		extraLease = await editorDocumentRegistry.acquire(
			{
				narratorId: config.narratorId,
				path,
				deviceId: "local",
				origin: () => "legacy",
				protocol: "raw-v2",
			},
			new AbortController().signal,
		);
	return {
		v1: lease?.copy.session.state,
		raw: extraLease.copy.session.state,
		sharedModel: lease?.copy.model === extraLease.copy.model,
		v1Text: lease?.copy.model?.getValue(),
		rawText: extraLease.copy.model?.getValue(),
	};
}
export function focusV1() {
	api.editor
		.getEditors()
		.find((view) => view.getModel() === lease?.copy.model)
		?.focus();
}
export async function saveV1() {
	await lease?.copy.session.save();
	return lease?.copy.session.state;
}
export async function beginHiddenLoad(filePath: string) {
	await close();
	path = filePath;
	localStorage.setItem("narrafork_editor_raw_v2", "1");
	panelViews(true);
}
export function loadingViews(a: boolean, b: boolean) {
	panelViews(true, a, b);
}
export async function close() {
	flushSync(() => root.render(null));
	controller?.abort();
	lease?.release();
	extraLease?.release();
	lease = undefined;
	extraLease = undefined;
	localStorage.removeItem("narrafork_editor_raw_v2");
	editorDocumentRegistry.shutdown();
	await new Promise((resolve) => setTimeout(resolve, 0));
}
Object.assign(window, {
	rawFixture: {
		open,
		state,
		dualViews,
		fingerprint,
		reload,
		edit,
		undo,
		page,
		close,
		windowsControl,
		refresh,
		mixedStart,
		mixedAddRaw,
		mixedInspect,
		focusV1,
		saveV1,
		beginHiddenLoad,
		loadingViews,
	},
});
