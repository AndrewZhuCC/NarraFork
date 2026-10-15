import { editorDocumentApi } from "../../../lib/api/editor-documents";
import { EditorWorkingCopy, type WorkingCopyOptions } from "./editor-working-copy";

interface Entry {
	copy: EditorWorkingCopy;
	references: number;
	viewReferences: number;
	removeListener: () => void;
}
export interface EditorDocumentLease {
	copy: EditorWorkingCopy;
	release(): void;
}
export interface EditorRecoveryLease {
	/** One independently authorized recovery owner, never a duplicate editable view lease. */
	retainedCopies: readonly EditorWorkingCopy[];
	release(): void;
}

/** Realm-local only. A path (even an absolute path) is NEVER an authorization identity. */
export class EditorDocumentRegistry {
	private entries = new Set<Entry>();
	private canonical = new Map<string, Entry>();
	private generations = new Map<string, number>();
	private removeUnload: (() => void) | undefined;
	private watchUnload() {
		if (this.removeUnload || typeof window === "undefined") return;
		const listener = (event: BeforeUnloadEvent) => {
			if (![...this.entries].some((entry) => entry.copy.protected)) return;
			event.preventDefault();
			event.returnValue = "";
		};
		window.addEventListener("beforeunload", listener);
		this.removeUnload = () => window.removeEventListener("beforeunload", listener);
	}
	async acquire(options: WorkingCopyOptions, signal: AbortSignal): Promise<EditorDocumentLease> {
		const generation = this.generations.get(options.narratorId) ?? 0;
		// Each new consumer is reauthorized. No provisional sharing without a complete known scope.
		const descriptor = options.readOnlySource
			? undefined
			: await editorDocumentApi.create(
					options.narratorId,
					{
						path: options.path,
						deviceId: options.deviceId,
						origin: options.origin(),
					},
					signal,
				);
		const releaseExtra = () => {
			if (descriptor)
				void editorDocumentApi.release(options.narratorId, descriptor.docId).catch(() => {});
		};
		if (signal.aborted || generation !== (this.generations.get(options.narratorId) ?? 0)) {
			releaseExtra();
			signal.throwIfAborted();
			const fallback =
				"Editor authorization changed. The previous draft was preserved; reopen the file in its new context.";
			throw new Error(options.translate?.("identityChanged", fallback) ?? fallback);
		}
		let entry = descriptor?.resourceKey ? this.canonical.get(descriptor.resourceKey) : undefined;
		if (entry?.copy.getSnapshot().invalidated) entry = undefined;
		if (entry) releaseExtra();
		else {
			const copy = new EditorWorkingCopy(options, descriptor);
			entry = { copy, references: 0, viewReferences: 0, removeListener: () => {} };
			const owned = entry;
			entry.removeListener = copy.subscribe(() => {
				if (owned.references === 0) this.collect(owned);
			});
			this.entries.add(entry);
			this.canonical.set(copy.key, entry);
			this.watchUnload();
		}
		if (descriptor?.recoveryKey)
			for (const other of this.entries) {
				if (
					other === entry ||
					other.copy.recoveryKey !== descriptor.recoveryKey ||
					other.copy.options.path !== options.path
				)
					continue;
				// A fresh server authorization confirms this is a different execution binding
				// owned by the same verified principal. Never migrate its draft to the new model.
				if (other.copy.key !== entry.copy.key)
					other.copy.invalidate(
						options.translate?.(
							"identityChanged",
							"Editor authorization changed; the previous draft was preserved.",
						) ?? "Editor authorization changed; the previous draft was preserved.",
					);
			}
		const viewing = entry;
		viewing.viewReferences++;
		viewing.references++;
		let released = false;
		return {
			copy: entry.copy,
			release: () => {
				if (released) return;
				released = true;
				viewing.viewReferences--;
				viewing.references--;
				this.collect(viewing);
			},
		};
	}
	/** Recovery authorization does not open or authorize the current file. The optional
	 * exclusion merely prevents presenting an ordinarily reacquired working copy twice. */
	async discoverRetained(
		options: WorkingCopyOptions,
		signal: AbortSignal,
		exclude: Promise<EditorWorkingCopy | null> = Promise.resolve(null),
	): Promise<EditorRecoveryLease> {
		const origin = options.origin();
		const candidates = [...this.entries].filter(
			(entry) =>
				entry.copy.recoveryKey &&
				entry.copy.protected &&
				entry.copy.options.narratorId === options.narratorId &&
				entry.copy.options.deviceId === options.deviceId &&
				entry.copy.options.origin() === origin &&
				entry.copy.options.path === options.path,
		);
		if (!candidates.length) return { retainedCopies: [], release: () => {} };
		const generation = this.generations.get(options.narratorId) ?? 0;
		// The path above is only a UI filter. ONLY this fresh, server-verified owner scope
		// may expose a previous draft; JWT/cache hints and a successful create are not proof.
		const scope = await editorDocumentApi.recoveryScope(
			options.narratorId,
			{ deviceId: options.deviceId, origin },
			signal,
		);
		let removeAbort: (() => void) | undefined;
		const current = await Promise.race([
			exclude,
			new Promise<never>((_resolve, reject) => {
				const abort = () => reject(signal.reason);
				removeAbort = () => signal.removeEventListener("abort", abort);
				signal.addEventListener("abort", abort, { once: true });
				if (signal.aborted) abort();
			}),
		]).finally(() => removeAbort?.());
		signal.throwIfAborted();
		if (generation !== (this.generations.get(options.narratorId) ?? 0))
			throw new Error(
				options.translate?.("identityChanged", "Editor authorization changed") ??
					"Editor authorization changed",
			);
		const retained = candidates.filter(
			(entry) =>
				this.entries.has(entry) &&
				entry.copy !== current &&
				entry.copy.recoveryKey === scope.recoveryKey &&
				entry.copy.protected &&
				!entry.copy.getSnapshot().discarded,
		);
		for (const entry of retained) {
			entry.copy.invalidate(
				options.translate?.(
					"identityChanged",
					"Editor authorization changed; the previous draft was preserved.",
				) ?? "Editor authorization changed; the previous draft was preserved.",
			);
			entry.references++;
		}
		let released = false;
		return {
			retainedCopies: retained.map((entry) => entry.copy),
			release: () => {
				if (released) return;
				released = true;
				for (const entry of retained) {
					entry.references--;
					this.collect(entry);
				}
			},
		};
	}
	/** Called only after explicit user confirmation. Never dispose a model attached to an old view. */
	discardRetained(copy: EditorWorkingCopy): boolean {
		const entry = [...this.entries].find((candidate) => candidate.copy === copy);
		if (!entry || entry.viewReferences !== 0 || !copy.discardRetainedDraft()) return false;
		if (this.canonical.get(copy.key) === entry) this.canonical.delete(copy.key);
		this.entries.delete(entry);
		entry.removeListener();
		if (!this.entries.size) {
			this.removeUnload?.();
			this.removeUnload = undefined;
		}
		return true;
	}
	private collect(entry: Entry) {
		// StrictMode's synchronous detach/reattach is not a model lifetime. This is NOT an LRU.
		queueMicrotask(() => {
			if (entry.references || entry.copy.protected) return;
			if (this.canonical.get(entry.copy.key) === entry) this.canonical.delete(entry.copy.key);
			this.entries.delete(entry);
			if (!this.entries.size) {
				this.removeUnload?.();
				this.removeUnload = undefined;
			}
			entry.removeListener();
			entry.copy.dispose();
		});
	}
	invalidateNarrator(narratorId: string, reason: string) {
		this.generations.set(narratorId, (this.generations.get(narratorId) ?? 0) + 1);
		for (const entry of this.entries.values())
			if (entry.copy.options.narratorId === narratorId) entry.copy.invalidate(reason);
	}
	/** Dirty/inflight orphans remain owned until explicitly resolved; never silently discard them. */
	get size() {
		return this.entries.size;
	}
}
export const editorDocumentRegistry = new EditorDocumentRegistry();
