import { useNavigate, useRouterState } from "@tanstack/react-router";
import { useEffect, useRef, useSyncExternalStore } from "react";
import {
	activeNavRowKey,
	navRowElement,
	navRowUrl,
	nextNavRow,
	readNavRows,
} from "../components/nav/recent-tab-keyboard-nav";
import { getRecentTabNarratorId, isTabActive } from "../components/nav/recent-tabs-logic";
import {
	matchesShortcut,
	SHORTCUT_ACTIONS,
	type ShortcutBindings,
	shortcutTargetBlocked,
} from "../lib/narrator-shortcuts";
import { queryClient } from "../lib/query-client";
import { useNarratorShortcuts } from "./useNarratorShortcuts";
import { RECENT_TABS_QUERY_KEY, type RecentTab } from "./useRecentTabs";

const DEBOUNCE_MS = 400;
/** The sidebar's session list; directional shortcuts walk its rendered rows. */
const LIST_SELECTOR = "[data-recent-tab-list]";

let pendingKey: string | null = null;
const listeners = new Set<() => void>();
let debounceTimer: ReturnType<typeof setTimeout> | null = null;
const tabSources = new Set<() => RecentTab[]>();

/** Numbered narrator shortcuts use the sidebar's rendered, deduplicated session order. */
export function registerRecentTabKeyboardSource(source: () => RecentTab[]): () => void {
	tabSources.add(source);
	return () => {
		tabSources.delete(source);
	};
}
export function narratorShortcutTabs(tabs: readonly RecentTab[]): RecentTab[] {
	const seen = new Set<string>();
	return tabs.filter((tab) => {
		const id = getRecentTabNarratorId(tab);
		if (!id || seen.has(id)) return false;
		seen.add(id);
		return true;
	});
}
function currentTabs(): RecentTab[] {
	for (const source of tabSources) {
		const tabs = narratorShortcutTabs(source());
		if (tabs.length) return tabs;
	}
	// A mounted but empty/collapsed list has no numbered targets.
	if (tabSources.size) return [];
	return narratorShortcutTabs(queryClient.getQueryData<RecentTab[]>(RECENT_TABS_QUERY_KEY) ?? []);
}
function setPendingKey(key: string | null) {
	if (pendingKey === key) return;
	pendingKey = key;
	for (const listener of listeners) listener();
}
export function getPendingTabKey(): string | null {
	return pendingKey;
}
function subscribe(listener: () => void) {
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
	};
}
export function usePendingTabKey() {
	return useSyncExternalStore(subscribe, () => pendingKey);
}
function cancelDebounce() {
	if (debounceTimer) clearTimeout(debounceTimer);
	debounceTimer = null;
}
export function cancelPendingTabNavigation(): void {
	cancelDebounce();
	setPendingKey(null);
}
function navigationUrl(key: string): string | null {
	return navRowUrl(key) ?? (key.startsWith("subagent:") ? `/narrators/${key.slice(9)}` : null);
}
export function handleRecentTabShortcut(
	event: KeyboardEvent,
	pathname: string,
	navigate: (destination: { to: string }) => unknown,
	bindings: ShortcutBindings,
): void {
	if (shortcutTargetBlocked(event)) return;
	const direction = matchesShortcut(event, bindings.previous)
		? "up"
		: matchesShortcut(event, bindings.next)
			? "down"
			: null;
	const slot = SHORTCUT_ACTIONS.filter((action) => action.startsWith("narrator")).findIndex(
		(action) => matchesShortcut(event, bindings[action]),
	);
	if (!direction && slot < 0) return;
	// Bare navigation keys should still edit a draft. Modified switching is allowed there.
	if (!event.ctrlKey && !event.metaKey && !event.altKey && shortcutTargetBlocked(event, true))
		return;
	const tabs = currentTabs();
	if (slot >= 0) {
		const tab = tabs[slot];
		if (!tab) return;
		event.preventDefault();
		cancelPendingTabNavigation();
		navigate({ to: `/narrators/${getRecentTabNarratorId(tab)}` });
		return;
	}
	if (!direction) return;
	const root = document.querySelector(LIST_SELECTOR);
	const rows = root
		? readNavRows(root)
		: tabs.map((tab) => ({ key: `${tab.type}:${tab.id}`, kind: "tab" as const }));
	const activeTab = root ? null : tabs.find((tab) => isTabActive(tab, pathname));
	const from =
		pendingKey ??
		(root ? activeNavRowKey(root) : activeTab ? `${activeTab.type}:${activeTab.id}` : null);
	const next = nextNavRow(rows, from, direction);
	if (!next) return;
	event.preventDefault();
	setPendingKey(next.key);
	if (root) navRowElement(root, next.key)?.scrollIntoView({ block: "nearest", behavior: "smooth" });
	cancelDebounce();
	// A folder header or "N more" row waits for Enter instead of opening a page.
	if (next.kind !== "tab") return;
	debounceTimer = setTimeout(() => {
		const key = pendingKey;
		cancelPendingTabNavigation();
		if (!key) return;
		const list = document.querySelector(LIST_SELECTOR);
		if (
			list
				? !navRowElement(list, key)
				: !currentTabs().some((tab) => `${tab.type}:${tab.id}` === key)
		)
			return;
		const url = navigationUrl(key);
		if (url) navigate({ to: url });
	}, DEBOUNCE_MS);
}

/** Inside a text field: Enter and the plain arrows remain the field's. */
function isTyping(target: EventTarget | null): boolean {
	if (!(target instanceof HTMLElement)) return false;
	return target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable;
}

/**
 * Walk sidebar sessions, folder headers and "N more" rows with the configured keys.
 * Enter opens the highlighted session now, or toggles a folder row. On "N more", plain
 * arrows resize its preview. Escape, a pointer press or a page change ends navigation.
 */
export function useRecentTabKeyboardNav() {
	const navigate = useNavigate();
	const pathname = useRouterState({ select: (state) => state.location.pathname });
	const { bindings } = useNarratorShortcuts();
	const latest = useRef({ navigate, pathname, bindings });
	latest.current = { navigate, pathname, bindings };

	// biome-ignore lint/correctness/useExhaustiveDependencies: re-run on page change only
	useEffect(() => {
		cancelPendingTabNavigation();
	}, [pathname]);

	useEffect(() => {
		const end = () => {
			if (pendingKey !== null) cancelPendingTabNavigation();
		};
		window.addEventListener("pointerdown", end, true);
		return () => window.removeEventListener("pointerdown", end, true);
	}, []);

	useEffect(() => {
		const list = () => document.querySelector(LIST_SELECTOR);
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.defaultPrevented || shortcutTargetBlocked(event)) return;
			if (pendingKey !== null && !event.altKey && !event.shiftKey && !event.metaKey) {
				if (
					!event.ctrlKey &&
					(event.key === "ArrowUp" || event.key === "ArrowDown") &&
					pendingKey.startsWith("strip:") &&
					!isTyping(event.target)
				) {
					const root = list();
					const el = root && navRowElement(root, pendingKey);
					if (el instanceof HTMLElement && el !== event.target) {
						event.preventDefault();
						el.dispatchEvent(new KeyboardEvent("keydown", { key: event.key, bubbles: true }));
					}
					return;
				}
				if (event.key === "Escape") {
					cancelPendingTabNavigation();
					return;
				}
				if (event.key === "Enter" && !event.isComposing && !isTyping(event.target)) {
					event.preventDefault();
					const key = pendingKey;
					const root = list();
					const el = root && navRowElement(root, key);
					const url = navigationUrl(key);
					if (url) {
						cancelPendingTabNavigation();
						latest.current.navigate({ to: url });
					} else if (el instanceof HTMLElement) {
						cancelDebounce();
						(el.matches("button") ? el : el.querySelector<HTMLElement>("button"))?.click();
					}
					return;
				}
			}
			handleRecentTabShortcut(
				event,
				latest.current.pathname,
				latest.current.navigate,
				latest.current.bindings,
			);
		};
		document.addEventListener("keydown", onKeyDown);
		return () => {
			document.removeEventListener("keydown", onKeyDown);
			cancelPendingTabNavigation();
		};
	}, []);
}
