import type { EditorDocumentDescriptor } from "@shared/editor-document";
import type { editor } from "monaco-editor/editor/editor.api";
import { editorDocumentApi } from "../../../lib/api/editor-documents";
import { createEditorLocalId } from "./editor-local-id";
import {
	EditorDocumentSession,
	type EditorSessionOptions,
	sessionExitBlocked,
} from "./editor-session-state";
import { captureEditorSnapshot, encodeEditorSnapshot } from "./editor-worker-client";
import { loadMonacoLanguage } from "./monaco-languages";
import { loadMonaco } from "./monaco-loader";

export type WorkingCopyOptions = Pick<
	EditorSessionOptions,
	"narratorId" | "path" | "deviceId" | "origin" | "translate" | "readOnlySource"
>;

/** Model creation is independent of any view, including preview-only consumers. */
export async function createWorkingCopyModel(
	path: string,
	key: string,
): Promise<editor.ITextModel> {
	const api = await loadMonaco();
	const language = await loadMonacoLanguage(api, path);
	return api.editor.createModel(
		"",
		language.language,
		api.Uri.from({
			scheme: "narrafork-editor",
			authority: "working-copy",
			path: `/${encodeURIComponent(key)}`,
			query: createEditorLocalId(),
		}),
	);
}

/** One mutable model, baseline and immutable save coordinator; views own no draft data. */
export class EditorWorkingCopy {
	readonly session: EditorDocumentSession;
	readonly key: string;
	readonly lifetimeId = createEditorLocalId();
	readonly recoveryKey?: string;
	model: editor.ITextModel | null = null;
	private listeners = new Set<() => void>();
	private subscription: { dispose(): void } | null = null;
	private loading: Promise<void> | null = null;
	private disposed = false;
	private generation = 0;
	private applying = false;
	private beforeApply = new Set<() => () => void>();
	onWillApply = (listener: () => () => void) => {
		this.beforeApply.add(listener);
		return () => {
			this.beforeApply.delete(listener);
		};
	};
	private seed: EditorDocumentDescriptor | undefined;
	constructor(
		readonly options: WorkingCopyOptions,
		descriptor?: EditorDocumentDescriptor,
	) {
		this.key = descriptor?.resourceKey ?? createEditorLocalId();
		this.recoveryKey = descriptor?.recoveryKey;
		this.seed = descriptor;
		this.session = new EditorDocumentSession({
			...options,
			resourceKey: descriptor?.resourceKey,
			snapshot: () => {
				if (!this.model) throw new Error("Editor is not ready");
				return captureEditorSnapshot(this.model);
			},
			encode: encodeEditorSnapshot,
			api: {
				...editorDocumentApi,
				create: async (...args) => {
					if (this.seed) {
						const seed = this.seed;
						this.seed = undefined;
						return seed;
					}
					return editorDocumentApi.create(...args);
				},
			},
			applyContent: (text) => {
				if (!this.model || this.disposed) throw new Error("Editor closed");
				const restore = [...this.beforeApply].map((listener) => listener());
				this.applying = true;
				try {
					this.model.setValue(text);
				} finally {
					this.applying = false;
				}
				for (const listener of restore) listener();
				return this.version();
			},
			onChange: () => this.emit(),
		});
	}
	private version() {
		if (!this.model) throw new Error("Editor is not ready");
		return {
			revision: this.model.getVersionId(),
			alternativeVersionId: this.model.getAlternativeVersionId(),
			length: this.model.getValueLength(),
		};
	}
	getSnapshot = () => this.session.state;
	subscribe = (listener: () => void) => {
		this.listeners.add(listener);
		return () => {
			this.listeners.delete(listener);
		};
	};
	private emit() {
		for (const listener of this.listeners) listener();
	}
	get protected() {
		return (
			sessionExitBlocked(this.session.state) ||
			!!this.session.state.confirmation ||
			!!this.session.state.conflict
		);
	}
	ensureLoaded(): Promise<void> {
		if (this.disposed || this.session.state.invalidated || this.session.state.loaded)
			return Promise.resolve();
		if (this.loading) return this.loading;
		const generation = this.generation;
		this.loading = (async () => {
			if (!this.model) {
				const model = await createWorkingCopyModel(this.options.path, this.key);
				if (this.disposed || generation !== this.generation) {
					model.dispose();
					return;
				}
				this.model = model;
				this.subscription = model.onDidChangeContent(() => {
					if (!this.applying) this.session.change(this.version());
				});
			}
			await this.session.load();
		})().finally(() => {
			this.loading = null;
		});
		return this.loading;
	}
	invalidate(reason: string) {
		this.generation++;
		this.session.invalidate(reason);
		if (this.seed)
			void editorDocumentApi.release(this.options.narratorId, this.seed.docId).catch(() => {});
		this.seed = undefined;
	}
	discardRetainedDraft(): boolean {
		if (!this.session.discardRetainedDraft()) return false;
		this.dispose();
		return true;
	}
	dispose() {
		if (this.disposed) return;
		this.disposed = true;
		this.generation++;
		this.subscription?.dispose();
		this.session.dispose();
		this.model?.dispose();
		this.model = null;
		if (this.seed)
			void editorDocumentApi.release(this.options.narratorId, this.seed.docId).catch(() => {});
		this.seed = undefined;
		this.beforeApply.clear();
		this.listeners.clear();
	}
}
