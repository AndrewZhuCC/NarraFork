import { type RefObject, useEffect, useRef } from "react";
import { useNarratorPanelVisible } from "../components/narrator/narrator-panel-visibility";
import { matchesShortcut, shortcutTargetBlocked } from "../lib/narrator-shortcuts";
import { readNarratorShortcuts } from "./useNarratorShortcuts";

type ReadingTarget = {
	host: HTMLElement;
	hovered: boolean;
	active: boolean;
	composer?: boolean;
	isPrimary?: boolean;
	read: () => void;
};

type ReadingPage = {
	targets: Set<() => ReadingTarget>;
	lastPanel: Element | null;
	cleanup?: () => void;
};
const pages = new WeakMap<Document, ReadingPage>();
const panelSelector = "[data-narrator-panel]";

/** Intersect with the real clipping ancestors, including the exact-list viewport.
 * A mounted historical row can have client rects while being entirely offscreen. */
function readingPosition(host: HTMLElement): number | null {
	if (!host.isConnected || !host.getClientRects().length) return null;
	const view = host.ownerDocument.defaultView;
	if (!view) return null;
	const body = host.getBoundingClientRect();
	let top = 0;
	let bottom = view.innerHeight;
	let left = 0;
	let right = view.innerWidth;
	for (let node: HTMLElement | null = host; node; node = node.parentElement) {
		if (node.hidden || node.getAttribute("aria-hidden") === "true" || node.inert) return null;
		const style = view.getComputedStyle(node);
		if (
			style.display === "none" ||
			style.visibility === "hidden" ||
			style.visibility === "collapse"
		)
			return null;
		if (node === host) continue;
		const clipsY = /auto|scroll|hidden|clip/.test(style.overflowY || style.overflow);
		const clipsX = /auto|scroll|hidden|clip/.test(style.overflowX || style.overflow);
		if (!clipsY && !clipsX) continue;
		const rect = node.getBoundingClientRect();
		if (clipsY) {
			top = Math.max(top, rect.top);
			bottom = Math.min(bottom, rect.bottom);
		}
		if (clipsX) {
			left = Math.max(left, rect.left);
			right = Math.min(right, rect.right);
		}
	}
	if (Math.min(body.bottom, bottom) <= Math.max(body.top, top)) return null;
	if (Math.min(body.right, right) <= Math.max(body.left, left)) return null;
	return Math.max(0, body.top - top);
}

/** One decision and one callback per key, regardless of the number of mounted messages. */
export function handleReadFromStartShortcut(
	event: KeyboardEvent,
	doc: Document,
	page: ReadingPage,
	shortcut = readNarratorShortcuts().readFromStart,
	composer = false,
): void {
	if (
		doc.visibilityState === "hidden" ||
		!matchesShortcut(event, shortcut) ||
		shortcutTargetBlocked(event, true)
	)
		return;
	if (composer) {
		if (
			event.keyCode === 229 ||
			doc.activeElement?.closest("[data-narrator-composer]") ||
			event
				.composedPath()
				.some(
					(node) =>
						node instanceof Element &&
						node.closest('[role="menu"], [role="listbox"], [role="alertdialog"]'),
				) ||
			Array.from(
				doc.querySelectorAll<HTMLElement>(
					'[role="dialog"], [role="alertdialog"], [aria-modal="true"], [role="menu"], [role="listbox"]',
				),
			).some((node) => readingPosition(node) !== null)
		)
			return;
	}
	// ponytail: geometry is scanned only on the shortcut, bounded by mounted bodies;
	// if lists stop virtualizing, replace this with their visible-row index.
	const targets = Array.from(page.targets, (get) => {
		const target = get();
		return {
			...target,
			panel: target.host.closest(panelSelector),
			position: readingPosition(target.host),
		};
	}).filter((target) => target.position !== null);
	let eligible = targets.filter(
		(target) => !!target.composer === composer && (!composer || !target.host.matches(":disabled")),
	);
	// A visible primary composer takes precedence over subagent hover/focus/last-panel hints.
	if (composer && eligible.some((target) => target.isPrimary)) {
		eligible = eligible.filter((target) => target.isPrimary);
	}
	if (!eligible.length) return;
	const focused = doc.activeElement?.closest("[data-shortcut-reading-host]");
	const reading =
		targets.find((target) => !target.composer && target.host === focused) ??
		targets.find((target) => !target.composer && target.hovered);
	const explicit = composer ? eligible.find((target) => target.panel === reading?.panel) : reading;
	const panels = new Set(eligible.map((target) => target.panel));
	const focusedPanel = doc.activeElement?.closest(panelSelector);
	const panel =
		explicit?.panel ??
		(focusedPanel && panels.has(focusedPanel) ? focusedPanel : null) ??
		(page.lastPanel && panels.has(page.lastPanel) ? page.lastPanel : null) ??
		eligible.find((target) => target.active)?.panel;
	const candidates = eligible.filter((target) => panels.size === 1 || target.panel === panel);
	const chosen = explicit ?? candidates.sort((a, b) => (a.position ?? 0) - (b.position ?? 0))[0];
	if (!chosen) return;
	event.preventDefault();
	page.lastPanel = chosen.panel;
	chosen.read();
}

/** Register bodies, not key listeners. The final unmount removes the page listeners. */
export function registerReadFromStartTarget(get: () => ReadingTarget): () => void {
	const doc = get().host.ownerDocument;
	let page = pages.get(doc);
	if (!page) {
		page = { targets: new Set(), lastPanel: null };
		pages.set(doc, page);
	}
	const current = page;
	const onKeyDown = (event: KeyboardEvent) => {
		const bindings = readNarratorShortcuts();
		handleReadFromStartShortcut(event, doc, current, bindings.readFromStart);
		handleReadFromStartShortcut(event, doc, current, bindings.focusComposer, true);
	};
	const onReading = (event: Event) => {
		if (event.target instanceof Element) {
			const panel = event.target.closest(panelSelector);
			if (panel) current.lastPanel = panel;
		}
	};
	// Keep listener ownership with the page, not whichever body registered first.
	if (!current.targets.size) {
		doc.addEventListener("keydown", onKeyDown);
		doc.addEventListener("pointerdown", onReading, true);
		doc.addEventListener("wheel", onReading, { capture: true, passive: true });
		current.cleanup = () => {
			doc.removeEventListener("keydown", onKeyDown);
			doc.removeEventListener("pointerdown", onReading, true);
			doc.removeEventListener("wheel", onReading, true);
		};
	}
	current.targets.add(get);
	return () => {
		if (!current.targets.delete(get) || current.targets.size) return;
		current.cleanup?.();
		pages.delete(doc);
	};
}

export function useFocusComposerShortcut(
	ref: RefObject<HTMLTextAreaElement | null>,
	isPrimary = false,
): void {
	const active = useNarratorPanelVisible();
	const latest = useRef({ active, isPrimary });
	latest.current = { active, isPrimary };
	useEffect(() => {
		const host = ref.current;
		if (!host) return;
		return registerReadFromStartTarget(() => ({
			host,
			hovered: false,
			...latest.current,
			composer: true,
			read: () => host.focus(),
		}));
	}, [ref]);
}

export function useReadFromStartShortcut(
	ref: RefObject<HTMLElement | null>,
	enabled: boolean,
	hovered: boolean,
	read: () => void,
): void {
	// In workspace panels this context is api.isActive: an arbitration hint, not
	// visibility. Other split panels may still be onscreen and hovered.
	const active = useNarratorPanelVisible();
	const latest = useRef({ hovered, active, read });
	latest.current = { hovered, active, read };
	useEffect(() => {
		const host = ref.current;
		if (!enabled || !host) return;
		return registerReadFromStartTarget(() => ({ host, ...latest.current }));
	}, [ref, enabled]);
}
