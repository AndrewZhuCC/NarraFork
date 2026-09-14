import { type RefObject, useEffect } from "react";
import { matchesShortcut, shortcutTargetBlocked } from "../lib/narrator-shortcuts";
import { readNarratorShortcuts } from "./useNarratorShortcuts";

export function handleReadFromStartShortcut(
	event: KeyboardEvent,
	host: HTMLElement,
	hovered: boolean,
	read: () => void,
	shortcut: string,
): void {
	if (!matchesShortcut(event, shortcut) || shortcutTargetBlocked(event, true)) return;
	if (!host.isConnected || !host.getClientRects().length) return;
	const focusedHost = host.ownerDocument.activeElement?.closest("[data-shortcut-reading-host]");
	if (focusedHost ? focusedHost !== host : !hovered) return;
	event.preventDefault();
	read();
}

/** Focused content handles its own keys; only the hovered body needs a document listener. */
export function useReadFromStartShortcut(
	ref: RefObject<HTMLElement | null>,
	enabled: boolean,
	hovered: boolean,
	read: () => void,
): void {
	useEffect(() => {
		const host = ref.current;
		if (!enabled || !host) return;
		const onKeyDown = (event: KeyboardEvent) =>
			handleReadFromStartShortcut(
				event,
				host,
				hovered,
				read,
				readNarratorShortcuts().readFromStart,
			);
		host.addEventListener("keydown", onKeyDown);
		if (hovered) document.addEventListener("keydown", onKeyDown);
		return () => {
			host.removeEventListener("keydown", onKeyDown);
			if (hovered) document.removeEventListener("keydown", onKeyDown);
		};
	}, [ref, enabled, hovered, read]);
}
