import type {
	EditorConfirmationData,
	EditorConflictData,
	EditorDocumentDescriptor,
	EditorOperationResult,
	EditorSaveResult,
	EditorUploadDescriptor,
} from "@shared/editor-document";
import {
	EDITOR_BOUNDED_READ_CAPABILITIES,
	type EditorProviderCapabilities,
} from "@shared/editor-provider-contracts";
import { ApiError } from "../../../lib/api/client";
import { editorDocumentApi } from "../../../lib/api/editor-documents";
import {
	EditorSnapshotReplay,
	type EditorTextSnapshot,
	getEncodedEditorSnapshotHash,
	hashEditorSnapshot,
} from "./editor-worker-client";

export interface DocumentVersion {
	revision: number;
	alternativeVersionId: number;
	length: number;
}
export type EditorRetiredReceipt =
	| EditorOperationResult
	| { status: "not-applied"; operationId?: string };
export interface EditorSessionState {
	loaded: boolean;
	loading: boolean;
	invalidated?: boolean;
	discarded?: boolean;
	/** Receipt recovery is separate from the permanently invalidated editable binding. */
	receipt?: EditorRetiredReceipt;
	receiptError?: string | null;
	checkingReceipt?: boolean;
	baseHash: string | null;
	encoding: string;
	baseline: number | null;
	equivalentVersion: number | null;
	version: DocumentVersion | null;
	phase: "idle" | "encoding" | "uploading" | "committing" | "unknown";
	error: string | null;
	backupIncomplete?: boolean;
	restoredDraft?: boolean;
	restoredDiskChanged?: boolean;
	confirmation: EditorConfirmationData | null;
	conflict: (EditorConflictData & { docId: string }) | null;
	/** Advisory observation only; never a baseline. Cleared whenever baseHash moves. */
	externalDisk?: "modified" | "deleted" | null;
	/**
	 * Capabilities of the current authorized descriptor (or the bounded-read
	 * source). undefined = not yet authorized, or an older server that does not
	 * declare them. Replaced only together with the descriptor.
	 */
	capabilities?: EditorProviderCapabilities;
}
export function sessionDirty(state: EditorSessionState): boolean {
	return (
		!!state.version &&
		state.version.alternativeVersionId !== state.baseline &&
		state.version.alternativeVersionId !== state.equivalentVersion
	);
}
export function sessionExitBlocked(state: EditorSessionState): boolean {
	return sessionDirty(state) || state.phase !== "idle";
}
export function sessionCanSave(state: EditorSessionState): boolean {
	return (
		state.loaded &&
		!state.invalidated &&
		// Declared capabilities are authoritative; only an older server that omits
		// them keeps the legacy v1 save path.
		state.capabilities?.v1Save !== false &&
		!state.loading &&
		state.phase === "idle" &&
		!state.confirmation &&
		!state.conflict &&
		sessionDirty(state)
	);
}
interface Attempt {
	version: DocumentVersion;
	baseHash: string | null;
	docId: string;
	uploadId?: string;
	operationId?: string;
	blob?: Blob;
	controller: AbortController;
}
export interface EditorSessionOptions {
	narratorId: string;
	path: string;
	deviceId: string;
	/** A registry seed fixes authorization before even the first source read/retry. */
	resourceKey?: string;
	origin: () => "reference" | "legacy";
	snapshot: () => EditorTextSnapshot;
	encode: (snapshot: EditorTextSnapshot, signal: AbortSignal) => Promise<Blob>;
	hash?: typeof hashEditorSnapshot;
	encodedHash?: typeof getEncodedEditorSnapshotHash;
	applyContent: (text: string, document?: EditorDocumentDescriptor) => DocumentVersion | null;
	onChange: (state: EditorSessionState) => void;
	beforeDispatch?: (operation: {
		operationId?: string;
		snapshotRevision: number;
		status: "query-first";
	}) => Promise<undefined | { draftWriterId?: string; draftModelLifetime?: string }>;
	onSaved?: (revision: number) => void;
	api?: typeof editorDocumentApi;
	translate?: (key: string, fallback: string) => string;
	readOnlySource?: (
		signal: AbortSignal,
	) => Promise<{ content: string; hash: string; encoding: string }>;
}
const initial = (): EditorSessionState => ({
	loaded: false,
	loading: false,
	baseHash: null,
	encoding: "utf-8",
	baseline: null,
	equivalentVersion: null,
	version: null,
	phase: "idle",
	error: null,
	confirmation: null,
	conflict: null,
});
const message = (error: unknown) => (error instanceof Error ? error.message : String(error));
const expired = (error: unknown) => error instanceof ApiError && error.status === 410;

/** Owns only metadata and an immutable save attempt. The Monaco model owns all draft text. */
export class EditorDocumentSession {
	state = initial();
	private api: typeof editorDocumentApi;
	private document: EditorDocumentDescriptor | null = null;
	private attempt: Attempt | null = null;
	private loadController: AbortController | null = null;
	private disposed = false;
	private baselineSource: EditorSnapshotReplay | Blob | null = null;
	private baselineDigest: Promise<string | null> | null = null;
	private baselineController = new AbortController();
	private baselineLength = -1;
	private baselineGeneration = 0;
	private receiptQuery: Promise<void> | null = null;
	/**
	 * At most one explicit user-requested save intent queued while an immutable attempt
	 * runs. Not a buffer of edits: only the next successful receipt may consume it, and
	 * then only if the latest revision still differs from the newly saved baseline.
	 */
	private pendingIntent = false;
	private clearPendingIntent() {
		this.pendingIntent = false;
	}
	constructor(private options: EditorSessionOptions) {
		this.api = options.api ?? editorDocumentApi;
	}
	/** React StrictMode replays effect setup/cleanup without recreating memoized adapters. */
	activate() {
		this.disposed = false;
	}
	private update(patch: Partial<EditorSessionState>) {
		if (this.disposed || this.state.invalidated) return;
		const rebased = "baseHash" in patch && patch.baseHash !== this.state.baseHash;
		this.state = { ...this.state, ...(rebased ? { externalDisk: null } : {}), ...patch };
		this.options.onChange(this.state);
	}
	setExternalDisk(next: "modified" | "deleted" | null) {
		if ((this.state.externalDisk ?? null) !== next) this.update({ externalDisk: next });
	}
	/** Reauthorize an expired document id while keeping the draft and old baseline. */
	async refreshDocument(): Promise<boolean> {
		if (this.attempt || this.state.phase !== "idle" || this.loadController) return false;
		const controller = new AbortController();
		this.loadController = controller;
		try {
			await this.recover(controller.signal);
			return this.loadController === controller;
		} catch (error) {
			if (this.loadController === controller) this.authorizationFailed(error);
			return false;
		} finally {
			if (this.loadController === controller) this.loadController = null;
		}
	}
	private resetBaseline(version: DocumentVersion | null, source?: Blob) {
		this.baselineController.abort();
		this.baselineController = new AbortController();
		this.baselineGeneration++;
		this.baselineDigest = null;
		this.baselineLength = version?.length ?? -1;
		this.baselineSource = null;
		if (source) {
			const digest = (this.options.encodedHash ?? getEncodedEditorSnapshotHash)(source);
			if (digest) this.baselineDigest = Promise.resolve(digest);
		} else if (version) {
			try {
				this.baselineSource = new EditorSnapshotReplay(this.options.snapshot());
			} catch {
				/* Equality is optional; inability to verify must remain conservatively dirty. */
			}
		}
	}
	private async fingerprint(source: EditorTextSnapshot | Blob, signal: AbortSignal) {
		signal.throwIfAborted();
		if (source instanceof Blob) {
			// The export Worker computed this digest while encoding the same immutable snapshot.
			// No browser WebCrypto requirement and no second full-document allocation.
			const digest = (this.options.encodedHash ?? getEncodedEditorSnapshotHash)(source);
			if (!digest) throw new Error("Snapshot digest unavailable");
			return digest;
		}
		return (this.options.hash ?? hashEditorSnapshot)(source, signal);
	}
	/** Current authorized document id, if any. Used ONLY for identity-probe reads. */
	get documentId(): string | undefined {
		return this.document?.docId;
	}
	/** Explicit idle/background verification only: change() never reads or hashes document text. */
	async verifyEquivalent(signal: AbortSignal) {
		const version = this.state.version;
		if (
			!version ||
			!sessionCanSave(this.state) ||
			version.length !== this.baselineLength ||
			this.disposed
		)
			return;
		const generation = this.baselineGeneration;
		if (!this.baselineDigest && this.baselineSource) {
			const source = this.baselineSource;
			const controller = this.baselineController;
			const digest = this.fingerprint(
				source instanceof EditorSnapshotReplay ? source.snapshot() : source,
				controller.signal,
			)
				.then((value) => {
					if (
						controller.signal.aborted ||
						generation !== this.baselineGeneration ||
						this.baselineDigest !== digest
					)
						return null;
					this.baselineSource = null;
					return value;
				})
				.catch(() => {
					// An old cancelled job must not clear a newer retry (or a saved baseline).
					if (this.baselineDigest === digest) this.baselineDigest = null;
					return null;
				});
			this.baselineDigest = digest;
		}
		if (!this.baselineDigest) return;
		try {
			// Build the baseline once, and only then reserve a second snapshot/worker job.
			const baseline = await this.baselineDigest;
			signal.throwIfAborted();
			if (
				!baseline ||
				generation !== this.baselineGeneration ||
				this.state.version?.revision !== version.revision
			)
				return;
			const candidate = await this.fingerprint(this.options.snapshot(), signal);
			if (
				!signal.aborted &&
				generation === this.baselineGeneration &&
				this.state.version?.revision === version.revision &&
				sessionCanSave(this.state) &&
				candidate === baseline
			)
				this.update({ equivalentVersion: version.alternativeVersionId });
		} catch {
			/* Cancellation/failure never manufactures a clean baseline. */
		}
	}
	private text(key: string, fallback: string) {
		return this.options.translate?.(key, fallback) ?? fallback;
	}
	private authorizationFailed(error: unknown) {
		if (!(error instanceof ApiError) || (error.status !== 401 && error.status !== 403))
			return false;
		this.invalidate(
			this.text(
				"identityChanged",
				"Editor authorization changed. The previous draft was preserved; reopen the file in its new context.",
			),
		);
		return true;
	}
	private release(docId: string) {
		void this.api.release(this.options.narratorId, docId).catch(() => {});
	}
	invalidate(reason: string) {
		if (this.state.invalidated) return;
		this.clearPendingIntent();
		const dispatched = this.state.phase === "committing" || this.state.phase === "unknown";
		if (!dispatched) this.cancel();
		this.update({
			invalidated: true,
			loading: false,
			error: reason,
			...(dispatched ? { phase: "unknown" } : {}),
		});
		this.loadController?.abort();
		this.attempt?.controller.abort();
		this.baselineController.abort();
	}
	private async authorize(signal: AbortSignal) {
		if (this.state.invalidated) throw new Error(this.state.error ?? "Editor identity changed");
		const next = await this.api.create(
			this.options.narratorId,
			{
				path: this.options.path,
				deviceId: this.options.deviceId,
				origin: this.options.origin(),
			},
			signal,
		);
		const expectedKey = this.options.resourceKey ?? this.document?.resourceKey;
		if (expectedKey && next.resourceKey !== expectedKey) {
			this.release(next.docId);
			const reason = this.text(
				"identityChanged",
				"Editor authorization changed. The previous draft was preserved; reopen the file in its new context.",
			);
			this.invalidate(reason);
			throw new Error(reason);
		}
		return next;
	}
	/** Credential-free association for crash backups; never permits a second apply. */
	get draftOperation() {
		return this.attempt && this.state.phase !== "idle"
			? {
					operationId: this.attempt.operationId,
					snapshotRevision: this.attempt.version.revision,
					status: "query-first" as const,
				}
			: undefined;
	}
	setBackupError(incomplete: boolean) {
		if (this.state.backupIncomplete === incomplete || this.disposed) return;
		this.state = { ...this.state, backupIncomplete: incomplete };
		this.options.onChange(this.state);
	}
	/** Explicit user recovery only: the original baseline is retained even if disk changed. */
	restoreDraft(text: string, baseHash: string | null, encoding: string) {
		if (
			this.disposed ||
			!this.state.loaded ||
			this.state.invalidated ||
			this.state.phase !== "idle" ||
			sessionDirty(this.state)
		)
			return false;
		const diskChanged = this.state.baseHash !== baseHash;
		const version = this.options.applyContent(text);
		if (!version) return false;
		this.resetBaseline(null);
		this.update({
			version,
			baseline: null,
			equivalentVersion: null,
			baseHash,
			encoding,
			restoredDraft: true,
			restoredDiskChanged: diskChanged,
			confirmation: null,
			conflict: null,
		});
		return true;
	}
	/** Reauthorization deliberately does NOT install the newly observed disk baseline or text. */
	private async recover(signal: AbortSignal) {
		const next = await this.authorize(signal);
		if (this.disposed || signal.aborted) {
			this.release(next.docId);
			signal.throwIfAborted();
			throw new Error("Editor closed");
		}
		const old = this.document;
		this.document = next;
		if (old) this.release(old.docId);
		// Same resourceKey (checked by authorize) but possibly different provider support.
		this.update({ capabilities: next.capabilities });
		return next;
	}
	change(version: DocumentVersion) {
		if (this.state.invalidated && !this.disposed) {
			this.state = { ...this.state, version };
			this.options.onChange(this.state);
			return;
		}
		if (this.state.version === null && this.state.loaded) this.resetBaseline(version);
		const confirmationChanged =
			this.state.confirmation && this.attempt?.version.revision !== version.revision;
		if (confirmationChanged) this.cancel();
		this.update({
			version,
			...(this.state.version === null && this.state.loaded
				? { baseline: version.alternativeVersionId }
				: {}),
		});
	}
	/**
	 * `background` is the disk-state refresh of a clean document: it never makes the
	 * view read-only, never preempts an explicit load, and silently yields (returns
	 * false) if the user edits before the fresh source is installed.
	 */
	async load(background = false): Promise<boolean> {
		if (this.state.phase !== "idle" || this.disposed || this.state.invalidated) return false;
		if (background && (this.loadController || sessionDirty(this.state))) return false;
		this.loadController?.abort();
		const controller = new AbortController();
		this.loadController = controller;
		const revision = this.state.version?.revision;
		if (!background) this.update({ loading: true, error: null });
		let next: EditorDocumentDescriptor | null = null;
		let installed = false;
		const changedDuringReload = () => {
			if (background) controller.abort();
			return new Error(
				this.text(
					"changedDuringReload",
					"The draft changed while reloading. Your edits were preserved.",
				),
			);
		};
		try {
			if (this.options.readOnlySource) {
				const source = await this.options.readOnlySource(controller.signal);
				controller.signal.throwIfAborted();
				if (this.disposed || this.loadController !== controller) return false;
				if (revision !== this.state.version?.revision) throw changedDuringReload();
				const version = this.options.applyContent(source.content.replace(/\r\n?/g, "\n"));
				this.resetBaseline(version);
				this.update({
					loaded: true,
					baseHash: source.hash,
					encoding: source.encoding,
					version,
					baseline: version?.alternativeVersionId ?? null,
					equivalentVersion: null,
					conflict: null,
					confirmation: null,
					// The source resolved a complete, identity-checked bounded read; no save path.
					capabilities: EDITOR_BOUNDED_READ_CAPABILITIES,
				});
				return true;
			}
			let text: string;
			for (let retry = 0; ; retry++) {
				try {
					controller.signal.throwIfAborted();
					next = await this.authorize(controller.signal);
					controller.signal.throwIfAborted();
					if (this.disposed || this.loadController !== controller) return false;
					// A background refresh stops as soon as the clean document becomes a draft.
					if (background && revision !== this.state.version?.revision) throw changedDuringReload();
					text = await this.api.source(
						this.options.narratorId,
						next.docId,
						next.versionHandle,
						controller.signal,
						next.utf8Bytes,
					);
					break;
				} catch (error) {
					if (
						retry !== 0 ||
						!(error instanceof ApiError) ||
						error.status !== 409 ||
						error.data?.code !== "EDITOR_SOURCE_CHANGED" ||
						controller.signal.aborted ||
						this.disposed ||
						this.loadController !== controller
					)
						throw error;
					if (next) this.release(next.docId);
					next = null;
				}
			}
			controller.signal.throwIfAborted();
			if (this.disposed || this.loadController !== controller) return false;
			if (revision !== this.state.version?.revision) throw changedDuringReload();
			// The install below is synchronous: no edit or save can interleave after this check.
			if (background && (this.state.phase !== "idle" || this.attempt)) return false;
			this.cancel();
			const old = this.document;
			this.document = next;
			next = null;
			const version = this.options.applyContent(text, this.document);
			this.resetBaseline(version);
			this.update({
				loaded: true,
				baseHash: this.document.baseHash,
				encoding: this.document.encoding,
				version,
				baseline: version?.alternativeVersionId ?? null,
				equivalentVersion: null,
				conflict: null,
				confirmation: null,
				capabilities: this.document.capabilities,
			});
			installed = true;
			if (old) this.release(old.docId);
		} catch (error) {
			if (this.loadController === controller && !this.disposed && this.authorizationFailed(error))
				return false;
			if (this.loadController === controller && !this.disposed && !background)
				this.update({ error: message(error) });
		} finally {
			if (next) this.release(next.docId);
			if (this.loadController === controller) {
				this.loadController = null;
				if (!background) this.update({ loading: false });
			}
		}
		return installed;
	}
	private settle(result: EditorSaveResult, attempt: Attempt) {
		if (this.attempt !== attempt || this.disposed || this.state.invalidated) return;
		if (result.snapshotRevision !== attempt.version.revision) {
			this.clearPendingIntent();
			this.update({
				phase: "unknown",
				error: this.text(
					"receiptMismatch",
					"Save receipt revision does not match. Check the save result before retrying.",
				),
			});
			return;
		}
		this.attempt = null;
		this.options.onSaved?.(attempt.version.revision);
		this.resetBaseline(attempt.version, attempt.blob);
		this.update({
			baseHash: result.hash,
			equivalentVersion: null,
			baseline: attempt.version.alternativeVersionId,
			phase: "idle",
			error: null,
			confirmation: null,
			conflict: null,
		});
		// One queued user intent may chain only after a confirmed receipt updates the
		// baseline. A fresh snapshot captures the newest keystrokes; nothing is replayed.
		if (this.pendingIntent) {
			this.pendingIntent = false;
			if (sessionCanSave(this.state)) void this.save();
		}
	}
	async save() {
		if (!this.options.narratorId || this.options.readOnlySource) return;
		// An in-flight attempt owns an immutable snapshot: queue at most one explicit
		// intent and let settle() start the next capture against the saved baseline.
		if (this.attempt) {
			// A repeat at the attempt's own revision is the same save, not a new request.
			if (this.state.version && this.state.version.revision !== this.attempt.version.revision)
				this.pendingIntent = true;
			return;
		}
		if (!sessionCanSave(this.state)) return;
		let snapshot: EditorTextSnapshot;
		try {
			snapshot = this.options.snapshot();
		} catch (error) {
			this.update({ error: message(error) });
			return;
		}
		const attempt: Attempt = {
			version: {
				revision: snapshot.revision,
				alternativeVersionId: snapshot.alternativeVersionId,
				length: snapshot.length,
			},
			baseHash: this.state.baseHash,
			docId: this.document?.docId ?? "",
			controller: new AbortController(),
		};
		this.attempt = attempt;
		this.update({ phase: "encoding", error: null });
		try {
			const blob = await this.options.encode(snapshot, attempt.controller.signal);
			attempt.blob = blob;
			attempt.controller.signal.throwIfAborted();
			this.update({ phase: "uploading" });
			if (!this.document) attempt.docId = (await this.recover(attempt.controller.signal)).docId;
			const createUpload = () =>
				this.api.createUpload(
					this.options.narratorId,
					attempt.docId,
					{
						baseHash: attempt.baseHash,
						encoding: this.state.encoding,
						snapshotRevision: attempt.version.revision,
					},
					attempt.controller.signal,
				);
			for (let recovery = 0; ; recovery++) {
				try {
					const upload = await createUpload();
					attempt.uploadId = upload.uploadId;
					attempt.operationId = upload.operationId;
					attempt.controller.signal.throwIfAborted();
					const sealed = await this.api.upload(
						this.options.narratorId,
						attempt.docId,
						upload.uploadId,
						blob,
						attempt.controller.signal,
					);
					attempt.operationId = sealed.operationId ?? attempt.operationId;
					break;
				} catch (error) {
					// No commit has been sent: one reauthorization/re-upload is safe and bounded.
					if (!expired(error) || recovery > 0) throw error;
					attempt.docId = (await this.recover(attempt.controller.signal)).docId;
					attempt.uploadId = undefined;
					attempt.operationId = undefined;
				}
			}
			attempt.controller.signal.throwIfAborted();
			await this.commit(attempt);
		} catch (error) {
			if (this.attempt === attempt && this.authorizationFailed(error)) return;
			if (this.attempt === attempt && this.state.phase !== "unknown") {
				this.clearPendingIntent();
				this.cancel();
				this.update({ error: message(error) });
			}
		}
	}
	private async commit(attempt: Attempt, confirmationToken?: string) {
		if (!attempt.uploadId || this.attempt !== attempt || this.state.invalidated) return;
		this.update({ phase: "committing", confirmation: null, error: null });
		try {
			const provenance = await this.options.beforeDispatch?.({
				operationId: attempt.operationId,
				snapshotRevision: attempt.version.revision,
				status: "query-first",
			});
			if (this.attempt !== attempt || this.state.invalidated) return;
			attempt.controller.signal.throwIfAborted();
			const result = await this.api.commit(
				this.options.narratorId,
				attempt.docId,
				attempt.uploadId,
				{ ...(provenance ?? {}), ...(confirmationToken ? { confirmationToken } : {}) },
				attempt.controller.signal,
			);
			if (this.attempt !== attempt) return;
			attempt.operationId = result.operationId;
			if (result.status === "saved") this.settle(result, attempt);
			else if (!this.state.invalidated) {
				this.clearPendingIntent();
				this.update({ phase: "unknown" });
				await this.reconcile();
			}
		} catch (error) {
			if (this.attempt !== attempt || this.disposed) return;
			const data =
				error instanceof ApiError
					? (error.data as { code?: string; operationId?: string } | undefined)
					: undefined;
			if (data?.operationId) attempt.operationId = data.operationId;
			if (this.state.invalidated || this.authorizationFailed(error)) return;
			if (
				data?.code === "EDITOR_DRAFT_WRITER_RETIRED" ||
				data?.code === "EDITOR_DRAFT_PROVENANCE_UNAVAILABLE" ||
				data?.code === "EDITOR_DRAFT_ANCHOR_LIMIT"
			) {
				this.clearPendingIntent();
				this.cancel(true);
				this.update({
					error: this.text(
						data.code === "EDITOR_DRAFT_WRITER_RETIRED"
							? "draftWriterRetired"
							: "draftSafetyFailed",
						data.code === "EDITOR_DRAFT_WRITER_RETIRED"
							? "This writer was retired by explicit draft recovery. Your text is preserved; reopen the previous editor before saving."
							: "The server could not persist the recovery safety check. No write was dispatched; keep the draft and retry after storage is repaired.",
					),
				});
				return;
			}
			if (data?.code === "NEEDS_CONFIRMATION") {
				this.clearPendingIntent();
				if (this.state.version?.revision !== attempt.version.revision) {
					this.cancel(true);
					this.update({
						error: this.text(
							"confirmationDraftChanged",
							"The draft changed. Save again to confirm the new snapshot.",
						),
					});
				} else this.update({ phase: "idle", confirmation: data as EditorConfirmationData });
				return;
			}
			if (data?.code === "STALE_WRITE") {
				this.clearPendingIntent();
				this.attempt = null;
				this.update({
					phase: "idle",
					conflict: { ...(data as EditorConflictData), docId: attempt.docId },
				});
				return;
			}
			// After dispatch, a transport/5xx/expiry failure is not proof that the disk was untouched.
			if (!(error instanceof ApiError) || error.status >= 500 || expired(error)) {
				this.clearPendingIntent();
				this.update({ phase: "unknown", error: message(error) });
				await this.reconcile();
				return;
			}
			this.clearPendingIntent();
			this.cancel(true);
			this.update({ error: message(error) });
		}
	}
	async confirm() {
		const attempt = this.attempt;
		const confirmation = this.state.confirmation;
		if (
			!attempt ||
			!confirmation ||
			this.state.phase !== "idle" ||
			this.state.loading ||
			this.state.invalidated
		)
			return;
		if (this.state.version?.revision !== attempt.version.revision) {
			this.cancel();
			return;
		}
		await this.commit(attempt, confirmation.confirmationToken);
	}
	/** Explicit confirmed discard only; unresolved writes must retain their evidence. */
	discardRetainedDraft(): boolean {
		if (
			!this.state.invalidated ||
			this.state.phase !== "idle" ||
			this.receiptQuery ||
			this.attempt ||
			this.disposed
		)
			return false;
		this.state = {
			...this.state,
			discarded: true,
			version: null,
			baseline: null,
			equivalentVersion: null,
			confirmation: null,
			conflict: null,
		};
		this.options.onChange(this.state);
		return true;
	}
	/** Only explicit UI recovery may query a retired attempt. It never installs a new base. */
	private reconcileInvalidated(): Promise<void> {
		if (this.receiptQuery) return this.receiptQuery;
		const attempt = this.attempt;
		const uploadId = attempt?.uploadId;
		if (!attempt || this.state.phase !== "unknown" || !uploadId || this.disposed)
			return Promise.resolve();
		const publish = (
			patch: Pick<
				Partial<EditorSessionState>,
				"receipt" | "receiptError" | "checkingReceipt" | "phase"
			>,
		) => {
			if (this.disposed || !this.state.invalidated) return;
			this.state = { ...this.state, ...patch };
			this.options.onChange(this.state);
		};
		const current = () => !this.disposed && this.attempt === attempt && !!this.state.invalidated;
		const finish = (receipt: EditorRetiredReceipt) => {
			if (!current()) return;
			this.attempt = null;
			// The old draft, version, baseline, encoding and authorization remain untouched.
			publish({ receipt, receiptError: null, phase: "idle" });
		};
		publish({ checkingReceipt: true, receiptError: null });
		this.receiptQuery = Promise.resolve()
			.then(async () => {
				try {
					let upload: EditorUploadDescriptor | undefined;
					if (!attempt.operationId) {
						upload = await this.api.getUpload(this.options.narratorId, attempt.docId, uploadId);
						if (!current()) return;
						attempt.operationId = upload.operationId;
					}
					if (attempt.operationId) {
						try {
							// The receipt endpoint independently checks current narrator ACL and verified ownership.
							const receipt = await this.api.operation(
								this.options.narratorId,
								attempt.operationId,
							);
							if (!current()) return;
							if (
								receipt.operationId !== attempt.operationId ||
								(receipt.status === "saved" &&
									(!receipt.result ||
										receipt.result.operationId !== attempt.operationId ||
										receipt.result.snapshotRevision !== attempt.version.revision))
							)
								throw new Error(
									this.text(
										"receiptMismatch",
										"Save receipt revision does not match. Check the save result before retrying.",
									),
								);
							if (receipt.status === "saved" || receipt.status === "failed") finish(receipt);
							else publish({ receipt });
							return;
						} catch (error) {
							if (!(error instanceof ApiError) || error.status !== 404) throw error;
							if (!current()) return;
						}
					}
					// Not-found alone is never success or proof of non-application. Old upload lookup
					// and cancellation require current narrator ACL plus original transfer ownership;
					// they cannot read or write a file under either execution binding.
					upload ??= await this.api.getUpload(this.options.narratorId, attempt.docId, uploadId);
					if (!current()) return;
					const cancelled =
						upload.state === "sealed" || upload.state === "uploading"
							? ((await this.api.cancelUpload(this.options.narratorId, attempt.docId, uploadId)) as
									| { state?: string }
									| undefined)
							: upload;
					if (!current()) return;
					if (cancelled?.state === "cancelled" || cancelled?.state === "expired")
						finish({ status: "not-applied", operationId: attempt.operationId });
					else
						publish({
							receiptError: this.text(
								"verifyBeforeSaving",
								"Save outcome needs verification. Do not submit another save.",
							),
						});
				} catch (error) {
					if (current()) publish({ receiptError: message(error) });
				}
			})
			.finally(() => {
				publish({ checkingReceipt: false });
				this.receiptQuery = null;
			});
		return this.receiptQuery;
	}
	async reconcile() {
		if (this.state.invalidated) return this.reconcileInvalidated();
		const attempt = this.attempt;
		if (!attempt || this.state.phase !== "unknown" || !attempt.uploadId || this.state.invalidated)
			return;
		try {
			let upload: EditorUploadDescriptor | undefined;
			if (!attempt.operationId) {
				upload = await this.api.getUpload(this.options.narratorId, attempt.docId, attempt.uploadId);
				if (this.attempt !== attempt || this.disposed || this.state.invalidated) return;
				attempt.operationId = upload.operationId;
			}
			if (attempt.operationId) {
				try {
					const operation = await this.api.operation(this.options.narratorId, attempt.operationId);
					if (this.attempt !== attempt || this.disposed || this.state.invalidated) return;
					if (operation.status === "saved" && operation.result)
						this.settle(operation.result, attempt);
					else if (operation.status === "failed") {
						this.clearPendingIntent();
						this.attempt = null;
						this.update({ phase: "idle", error: operation.error?.message ?? "Save failed" });
					} else
						this.update({
							error: this.text(
								"verifyBeforeSaving",
								"Save outcome needs verification. Do not submit another save.",
							),
						});
					return;
				} catch (error) {
					// IDs are preallocated: a missing operation may mean commit never arrived.
					// It is not evidence of an untouched disk; only upload cancellation proves that.
					if (!(error instanceof ApiError) || error.status !== 404) throw error;
					if (this.attempt !== attempt || this.disposed || this.state.invalidated) return;
				}
			}
			upload ??= await this.api.getUpload(this.options.narratorId, attempt.docId, attempt.uploadId);
			if (this.attempt !== attempt || this.disposed || this.state.invalidated) return;
			attempt.operationId = upload.operationId ?? attempt.operationId;
			// sealed alone is not sufficient: atomically cancel before a delayed commit can win.
			// A committing/settled response (including a cancellation race) must remain blocked.
			const cancelled =
				upload.state === "sealed" || upload.state === "uploading"
					? ((await this.api.cancelUpload(
							this.options.narratorId,
							attempt.docId,
							attempt.uploadId,
						)) as { state?: string } | undefined)
					: upload;
			if (this.attempt !== attempt || this.disposed || this.state.invalidated) return;
			if (cancelled?.state === "cancelled" || cancelled?.state === "expired") {
				this.cancel(true);
				this.update({
					error: this.text(
						"uploadNotCommitted",
						"The upload was not committed. You can save again.",
					),
				});
			}
		} catch (error) {
			if (this.attempt === attempt) this.update({ error: message(error) });
		}
	}
	cancel(verifiedUncommitted = false) {
		if (
			!verifiedUncommitted &&
			(this.state.phase === "committing" || this.state.phase === "unknown")
		) {
			this.clearPendingIntent();
			this.attempt?.controller.abort();
			this.update({ phase: "unknown" });
			return;
		}
		this.clearPendingIntent();
		const attempt = this.attempt;
		this.attempt = null;
		attempt?.controller.abort();
		// A cancelled upload-creation response may hide its newly allocated uploadId.
		// Release that session rather than leave an unreachable active upload consuming its quota.
		if (
			attempt &&
			this.state.phase === "uploading" &&
			!attempt.uploadId &&
			this.document?.docId === attempt.docId
		) {
			this.release(attempt.docId);
			this.document = null;
		}
		if (attempt?.uploadId)
			void this.api
				.cancelUpload(this.options.narratorId, attempt.docId, attempt.uploadId)
				.catch(() => {});
		this.update({ phase: "idle", confirmation: null });
	}
	async conflictPreview(signal?: AbortSignal) {
		const conflict = this.state.conflict;
		if (!conflict) throw new Error("No conflict snapshot");
		return this.api.sourcePreview(
			this.options.narratorId,
			conflict.docId,
			conflict.conflictVersionHandle,
			signal,
		);
	}
	async conflictDownload(signal?: AbortSignal) {
		const conflict = this.state.conflict;
		if (!conflict) throw new Error("No conflict snapshot");
		return this.api.sourceBlob(
			this.options.narratorId,
			conflict.docId,
			conflict.conflictVersionHandle,
			signal,
		);
	}
	async conflictSource(signal?: AbortSignal) {
		const conflict = this.state.conflict;
		if (!conflict) throw new Error("No conflict snapshot");
		return this.api.source(
			this.options.narratorId,
			conflict.docId,
			conflict.conflictVersionHandle,
			signal,
		);
	}
	keepMine() {
		const conflict = this.state.conflict;
		if (!conflict || this.state.loading || this.state.invalidated) return;
		this.resetBaseline(null);
		this.update({
			baseHash: conflict.currentHash,
			equivalentVersion: null,
			encoding: conflict.encoding,
			baseline: null,
			conflict: null,
			error: null,
		});
	}
	async takeTheirs() {
		const conflict = this.state.conflict;
		if (!conflict || this.state.loading || this.state.invalidated) return;
		const revision = this.state.version?.revision;
		this.loadController?.abort();
		const controller = new AbortController();
		this.loadController = controller;
		this.update({ loading: true, error: null });
		try {
			const text = await this.conflictSource(controller.signal);
			controller.signal.throwIfAborted();
			if (this.state.conflict !== conflict || this.disposed || this.loadController !== controller)
				return;
			if (this.state.version?.revision !== revision)
				throw new Error(
					this.text(
						"changedDuringReload",
						"The draft changed while reloading. Your edits were preserved.",
					),
				);
			const version = this.options.applyContent(text);
			this.resetBaseline(version);
			this.update({
				version,
				baseline: version?.alternativeVersionId ?? null,
				equivalentVersion: null,
				baseHash: conflict.currentHash,
				encoding: conflict.encoding,
				conflict: null,
			});
		} catch (error) {
			if (this.loadController === controller && this.authorizationFailed(error)) return;
			if (this.loadController === controller) this.update({ error: message(error) });
		} finally {
			if (this.loadController === controller) {
				this.loadController = null;
				this.update({ loading: false });
			}
		}
	}
	get resourceCosts() {
		return {
			documents: this.document ? 1 : 0,
			baselineBytes:
				this.baselineSource instanceof EditorSnapshotReplay
					? this.baselineSource.estimatedBytes
					: (this.baselineSource?.size ?? 0),
		};
	}
	cancelBackgroundVerification() {
		this.baselineController.abort();
		this.baselineController = new AbortController();
		// Retain the replay owner while hashing is incomplete. A future visible consumer
		// can restart immediately, even if the old worker has not settled its abort yet.
		if (this.baselineSource) this.baselineDigest = null;
	}
	dispose() {
		this.disposed = true;
		this.clearPendingIntent();
		this.resetBaseline(null);
		this.loadController?.abort();
		this.attempt?.controller.abort();
		if (this.attempt?.uploadId)
			void this.api
				.cancelUpload(this.options.narratorId, this.attempt.docId, this.attempt.uploadId)
				.catch(() => {});
		if (this.document) this.release(this.document.docId);
		this.attempt = null;
		this.document = null;
	}
}
