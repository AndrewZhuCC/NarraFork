import { useNavigate, useRouterState } from "@tanstack/react-router";
import { useCallback, useEffect, useSyncExternalStore } from "react";
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
let pendingKey: string | null = null;
const listeners = new Set<() => void>();
let debounceTimer: ReturnType<typeof setTimeout> | null = null;
const tabSources = new Set<() => RecentTab[]>();

/** The sidebar supplies its rendered order, including collapsed groups and workspace children. */
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
	// A mounted but empty/collapsed list has no targets. Never navigate hidden members.
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
export function cancelPendingTabNavigation(): void {
	if (debounceTimer) clearTimeout(debounceTimer);
	debounceTimer = null;
	setPendingKey(null);
}
function navigateToTab(tab: RecentTab, navigate: (destination: { to: string }) => unknown) {
	const id = getRecentTabNarratorId(tab);
	if (id) navigate({ to: `/narrators/${id}` });
}
export function handleRecentTabShortcut(
	event: KeyboardEvent,
	pathname: string,
	navigate: (destination: { to: string }) => unknown,
	bindings: ShortcutBindings,
): void {
	if (shortcutTargetBlocked(event)) return;
	const direction = matchesShortcut(event, bindings.previous)
		? -1
		: matchesShortcut(event, bindings.next)
			? 1
			: 0;
	const slot = SHORTCUT_ACTIONS.filter((action) => action.startsWith("narrator")).findIndex(
		(action) => matchesShortcut(event, bindings[action]),
	);
	if (!direction && slot < 0) return;
	// Bare navigation keys should still edit a draft. Modified switching is allowed there.
	if (!event.ctrlKey && !event.metaKey && !event.altKey && shortcutTargetBlocked(event, true))
		return;
	const tabs = currentTabs();
	if (!tabs.length) return;
	if (slot >= 0) {
		const tab = tabs[slot];
		if (!tab) return;
		event.preventDefault();
		cancelPendingTabNavigation();
		navigateToTab(tab, navigate);
		return;
	}
	let base = pendingKey ? tabs.findIndex((tab) => `${tab.type}:${tab.id}` === pendingKey) : -1;
	if (base < 0) base = tabs.findIndex((tab) => isTabActive(tab, pathname));
	const index =
		base < 0
			? direction > 0
				? 0
				: tabs.length - 1
			: (base + direction + tabs.length) % tabs.length;
	const tab = tabs[index];
	event.preventDefault();
	const nextKey = `${tab.type}:${tab.id}`;
	setPendingKey(nextKey);
	document
		.querySelector(`[data-tab-sort-id="${globalThis.CSS.escape(nextKey)}"]`)
		?.scrollIntoView({ block: "nearest", behavior: "smooth" });
	if (debounceTimer) clearTimeout(debounceTimer);
	debounceTimer = setTimeout(() => {
		const key = pendingKey;
		cancelPendingTabNavigation();
		const current = currentTabs().find((item) => `${item.type}:${item.id}` === key);
		if (current) navigateToTab(current, navigate);
	}, DEBOUNCE_MS);
}

/** One global listener, mounted by AppRootLayout. */
export function useRecentTabKeyboardNav() {
	const pathname = useRouterState({ select: (state) => state.location.pathname });
	const navigate = useNavigate();
	const { bindings } = useNarratorShortcuts();
	const handleKeyDown = useCallback(
		(event: KeyboardEvent) => {
			handleRecentTabShortcut(event, pathname, navigate, bindings);
		},
		[pathname, navigate, bindings],
	);
	useEffect(() => {
		document.addEventListener("keydown", handleKeyDown);
		return () => {
			document.removeEventListener("keydown", handleKeyDown);
			cancelPendingTabNavigation();
		};
	}, [handleKeyDown]);
}
