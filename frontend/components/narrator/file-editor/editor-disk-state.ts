/**
 * editor-disk-state.ts — bounded, shared probes for external disk changes.
 *
 * The probe asks the server "what is the current v1 identity of this authorized
 * document's file?" and compares it with the working copy's saved base hash. It
 * never reads text, never creates a session and never moves the saved baseline.
 *
 * - One entry per working copy, shared by all of its views; only a copy with a
 *   visible view (foreground page) probes. Hidden copies are re-verified on reveal.
 * - At most two probes in flight per realm; at most one in flight plus one queued
 *   per copy. A reply is discarded if the authorized document, the saved base or
 *   the save phase moved while it was in flight.
 * - Wake-ups: becoming visible, page focus, WS reconnect, the existing
 *   narrator-scoped `workspace_paths_changed` hint (one shared listener per actual
 *   file narrator, throttled), and a 15 s visible-only fallback. A hint is never
 *   treated as a version or as authorization.
 * - Clean idle + changed: the ordinary session load reinstalls the authorized
 *   source, aborting if the user typed meanwhile. Dirty: only an advisory flag.
 */
import type { EditorDiskState } from "@shared/editor-document";
import { ApiError } from "../../../lib/api/client";
import { editorDocumentApi } from "../../../lib/api/editor-documents";
import { narratorWSManager } from "../../../lib/narrator-ws-manager";
import { sessionDirty } from "./editor-session-state";
import type { EditorWorkingCopy } from "./editor-working-copy";

const MAX_CONCURRENT = 2;
const VISIBLE_IDLE_MS = 15_000;
const HINT_THROTTLE_MS = 250;

interface Entry {
	copy: EditorWorkingCopy;
	views: number;
	visibleViews: number;
	queued: boolean;
	inflight?: AbortController;
	idleTimer?: ReturnType<typeof setTimeout>;
}
interface NarratorHint {
	copies: number;
	release: () => void;
	timer?: ReturnType<typeof setTimeout>;
}
export interface EditorDiskStateView {
	setVisible(visible: boolean): void;
	detach(): void;
}

export class EditorDiskStateRegistry {
	private entries = new Map<EditorWorkingCopy, Entry>();
	private hints = new Map<string, NarratorHint>();
	private running = 0;
	private removeGlobal?: () => void;
	/** Diagnostics only (tests/acceptance): never drives behaviour. */
	readonly diagnostics = { probes: 0, reloads: 0, discarded: 0 };

	/** One handle per view; the copy's entry lives while any view is attached. */
	attach(copy: EditorWorkingCopy, visible: boolean): EditorDiskStateView {
		const { narratorId, deviceId, readOnlySource } = copy.options;
		// Probes need an editor-document session (readOnlySource has none). Whether
		// the provider can verify it is decided per probe from the current caps.
		if (!narratorId || readOnlySource || (!copy.session.state.capabilities && deviceId !== "local"))
			return { setVisible: () => {}, detach: () => {} };
		let entry = this.entries.get(copy);
		if (!entry) {
			entry = { copy, views: 0, visibleViews: 0, queued: false };
			this.entries.set(copy, entry);
			this.retainHint(narratorId);
			this.watchGlobal();
		}
		const owned = entry;
		owned.views++;
		let shown = false;
		let attached = true;
		const setVisible = (next: boolean) => {
			if (!attached || shown === next) return;
			shown = next;
			owned.visibleViews += next ? 1 : -1;
			if (owned.visibleViews === 1 && next) this.request(owned);
			else if (!owned.visibleViews) {
				clearTimeout(owned.idleTimer);
				owned.idleTimer = undefined;
			}
		};
		setVisible(visible);
		return {
			setVisible,
			detach: () => {
				if (!attached) return;
				setVisible(false);
				attached = false;
				if (--owned.views) return;
				owned.inflight?.abort();
				this.entries.delete(copy);
				this.releaseHint(narratorId);
				if (!this.entries.size) {
					this.removeGlobal?.();
					this.removeGlobal = undefined;
				}
				this.pump();
			},
		};
	}
	/** Collapse any number of wake-ups into one queued probe per copy. */
	private request(entry: Entry) {
		// Hidden copies drop the wake-up; becoming visible again re-verifies.
		if (!entry.visibleViews) return;
		entry.queued = true;
		this.pump();
	}
	private requestNarrator(narratorId?: string) {
		for (const entry of this.entries.values())
			if (!narratorId || entry.copy.options.narratorId === narratorId) this.request(entry);
	}
	private pump() {
		for (const entry of this.entries.values()) {
			if (this.running >= MAX_CONCURRENT) return;
			if (entry.queued && !entry.inflight && entry.visibleViews) this.start(entry);
		}
	}
	private start(entry: Entry) {
		entry.queued = false;
		const session = entry.copy.session;
		const docId = session.documentId;
		const { phase, loading, invalidated, baseHash, capabilities } = session.state;
		// A declared provider without a bounded read cannot verify disk identity.
		if (capabilities && !capabilities.boundedRead) return;
		// Unknown/in-flight saves and reloads own the baseline; check again later.
		if (!docId || phase !== "idle" || loading || invalidated) {
			this.scheduleIdle(entry);
			return;
		}
		const controller = new AbortController();
		entry.inflight = controller;
		this.running++;
		this.diagnostics.probes++;
		void editorDocumentApi
			.diskState(entry.copy.options.narratorId, docId, controller.signal)
			.then(
				(state) => this.apply(entry, docId, baseHash, state),
				(error) => this.failed(entry, docId, error),
			)
			.finally(() => {
				this.running--;
				if (entry.inflight === controller) entry.inflight = undefined;
				this.scheduleIdle(entry);
				this.pump();
			});
	}
	private current(entry: Entry, docId: string, baseHash: string | null) {
		const { state, documentId } = entry.copy.session;
		return (
			this.entries.get(entry.copy) === entry &&
			documentId === docId &&
			state.baseHash === baseHash &&
			state.phase === "idle" &&
			!state.loading &&
			!state.invalidated
		);
	}
	private apply(entry: Entry, docId: string, baseHash: string | null, disk: EditorDiskState) {
		if (!this.current(entry, docId, baseHash)) {
			// Our own receipt (or a reload/rebind) superseded this observation.
			this.diagnostics.discarded++;
			entry.queued = true;
			return;
		}
		if (!disk.exists) return entry.copy.session.setExternalDisk("deleted");
		if (disk.baseHash === baseHash) return entry.copy.session.setExternalDisk(null);
		const state = entry.copy.session.state;
		if (sessionDirty(state) || state.conflict || state.confirmation)
			return entry.copy.session.setExternalDisk("modified");
		if (!entry.visibleViews) return; // re-verified on the next reveal
		this.diagnostics.reloads++;
		void entry.copy.session.load(true).then((installed) => {
			if (!installed && this.entries.get(entry.copy) === entry)
				entry.copy.session.setExternalDisk("modified");
		});
	}
	private failed(entry: Entry, docId: string, error: unknown) {
		// Idle server documents expire; reauthorize the binding without touching text.
		// A changed execution binding invalidates through the existing identity check.
		if (
			error instanceof ApiError &&
			error.status === 410 &&
			this.current(entry, docId, entry.copy.session.state.baseHash)
		)
			void entry.copy.session.refreshDocument().then((refreshed) => {
				if (refreshed && this.entries.get(entry.copy) === entry) this.request(entry);
			});
		// Transport failures are not disk evidence; the next wake-up retries.
	}
	private scheduleIdle(entry: Entry) {
		clearTimeout(entry.idleTimer);
		entry.idleTimer = undefined;
		if (!entry.visibleViews || this.entries.get(entry.copy) !== entry) return;
		if (entry.queued) return this.pump();
		entry.idleTimer = setTimeout(() => {
			entry.idleTimer = undefined;
			this.request(entry);
		}, VISIBLE_IDLE_MS);
	}
	private retainHint(narratorId: string) {
		const existing = this.hints.get(narratorId);
		if (existing) {
			existing.copies++;
			return;
		}
		// Reuse the shared realm socket: one subscription + listener per actual file narrator.
		const subscription = narratorWSManager.subscribe([narratorId], { kind: "list" });
		const hint: NarratorHint = { copies: 1, release: () => {} };
		const listener = narratorWSManager.addListener(
			{ narratorIds: [narratorId], types: ["workspace_paths_changed"] },
			() => {
				hint.timer ??= setTimeout(() => {
					hint.timer = undefined;
					this.requestNarrator(narratorId);
				}, HINT_THROTTLE_MS);
			},
		);
		hint.release = () => {
			clearTimeout(hint.timer);
			narratorWSManager.removeListener(listener);
			narratorWSManager.unsubscribe(subscription);
		};
		this.hints.set(narratorId, hint);
	}
	private releaseHint(narratorId: string) {
		const hint = this.hints.get(narratorId);
		if (!hint || --hint.copies) return;
		this.hints.delete(narratorId);
		hint.release();
	}
	private watchGlobal() {
		if (this.removeGlobal || typeof document === "undefined") return;
		const focus = () => {
			if (document.visibilityState === "visible") this.requestNarrator();
		};
		const removeConnection = narratorWSManager.onConnectionChange((connected, reconnect) => {
			if (connected && reconnect) this.requestNarrator();
		});
		document.addEventListener("visibilitychange", focus);
		window.addEventListener("focus", focus);
		this.removeGlobal = () => {
			document.removeEventListener("visibilitychange", focus);
			window.removeEventListener("focus", focus);
			removeConnection();
		};
	}
	/** Diagnostics only. */
	get size() {
		return this.entries.size;
	}
	/** Diagnostics only: bounded metadata for the copy owning `model`, never text. */
	inspect(model: unknown) {
		for (const entry of this.entries.values()) {
			if (entry.copy.model !== model) continue;
			const { phase, loading, invalidated, baseHash, externalDisk } = entry.copy.session.state;
			return {
				phase,
				loading,
				invalidated: !!invalidated,
				baseHash,
				externalDisk: externalDisk ?? null,
				documentId: entry.copy.session.documentId ?? null,
				views: entry.views,
				visibleViews: entry.visibleViews,
				queued: entry.queued,
				inflight: !!entry.inflight,
				idleTimer: !!entry.idleTimer,
				globalListening: !!this.removeGlobal,
			};
		}
		return null;
	}
}

export const editorDiskStateRegistry = new EditorDiskStateRegistry();
