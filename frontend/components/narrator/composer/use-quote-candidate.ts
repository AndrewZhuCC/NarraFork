import type { FileReference, FileReferenceEditorSelection } from "@shared/file-reference";
import { type RefObject, useCallback, useEffect, useRef, useState } from "react";
import type { ComposerQuote } from "./composer-quotes";
import { fileReferenceToken, savedSelectionReference } from "./file-reference-input";
import { NARRATOR_PANEL_SELECTOR, readPanelQuoteSelection } from "./quote-selection";

/** Long enough to skip intermediate drag states, short enough to feel immediate. */
export const QUOTE_SELECTION_SETTLE_MS = 120;

export type QuoteCandidate =
	| { kind: "text"; key: string; quote: ComposerQuote }
	| {
			kind: "file";
			key: string;
			label: string;
			/** Null while the file has unsaved edits: only saved bytes can be referenced. */
			reference: FileReference | null;
	  };
type FileQuoteCandidate = Extract<QuoteCandidate, { kind: "file" }>;

// Several composers can be mounted (workspace panes). The shortcut belongs to
// whichever one most recently gained a candidate, never to all of them.
let ownerSequence = 0;
let shortcutOwner = 0;

function isQuoteShortcut(event: KeyboardEvent): boolean {
	return (
		event.key === "Tab" &&
		!event.altKey &&
		!event.ctrlKey &&
		!event.metaKey &&
		!event.shiftKey &&
		!event.repeat &&
		event.keyCode !== 229 &&
		!event.isComposing
	);
}

function targetElement(target: EventTarget | null): Element | null {
	return target && typeof (target as Element).closest === "function" ? (target as Element) : null;
}
function isEditableTarget(target: EventTarget | null): boolean {
	return !!targetElement(target)?.closest(
		'input, textarea, select, [contenteditable]:not([contenteditable="false"]), .monaco-editor, [role="dialog"], [role="menu"], [role="listbox"]',
	);
}
function textQuoteKey(quote: ComposerQuote): string {
	return JSON.stringify([quote.label, quote.text, quote.anchor ?? null]);
}

/** Tab is a selection action, not a global replacement for ordinary focus/indent keys. */
function canQuoteWithTab(
	candidate: QuoteCandidate,
	textarea: HTMLElement,
	event: KeyboardEvent,
): boolean {
	if (event.defaultPrevented) return false;
	const target = targetElement(event.target);
	// File editors expose their own source-bound gutter action. Tab always belongs to Monaco.
	if (candidate.kind === "file") return false;
	if (target?.closest('button, a[href], select, [role="button"], [role="menu"], [role="listbox"]'))
		return false;
	if (isEditableTarget(target) && (!target || !textarea.contains(target))) return false;
	const panel = textarea.closest(NARRATOR_PANEL_SELECTOR);
	const quote = panel
		? readPanelQuoteSelection(panel, textarea.ownerDocument.getSelection())
		: null;
	return !!quote && textQuoteKey(quote) === candidate.key;
}

function editorSelectionLabel(selection: FileReferenceEditorSelection): string {
	return fileReferenceToken({
		...selection.target,
		id: "candidate",
		label: selection.label,
	}).replace(/^#file:/, "");
}

export function editorSelectionCandidate(
	selection: FileReferenceEditorSelection | null | undefined,
): FileQuoteCandidate | null {
	const range = selection?.target.selection;
	if (!selection || !range) return null;
	if (range.startLineNumber === range.endLineNumber && range.startColumn === range.endColumn)
		return null;
	const { deviceId, path } = selection.target;
	return {
		kind: "file",
		key: JSON.stringify([deviceId, path, range]),
		label: editorSelectionLabel(selection),
		reference: savedSelectionReference(selection),
	};
}

/**
 * The temporary message/tool quote in this composer's own list. File selections
 * use source-bound Monaco gutter buttons, never this popup or the global Tab.
 * Consuming/dismissing hides a candidate until its source selection changes.
 */
export function useQuoteCandidate({
	textareaRef,
	element,
	onShortcut,
}: {
	textareaRef: RefObject<HTMLElement | null>;
	element?: HTMLElement | null;
	onShortcut: (candidate: QuoteCandidate) => void;
}) {
	const [candidate, setCandidate] = useState<QuoteCandidate | null>(null);
	const [showShortcut, setShowShortcut] = useState(true);
	const touchInputRef = useRef(false);
	const candidateRef = useRef(candidate);
	candidateRef.current = candidate;
	const onShortcutRef = useRef(onShortcut);
	onShortcutRef.current = onShortcut;
	const ownerRef = useRef(0);
	if (!ownerRef.current) ownerRef.current = ++ownerSequence;
	const hiddenKeysRef = useRef<{ text: string | null; file: string | null }>({
		text: null,
		file: null,
	});

	const hide = useCallback((value: QuoteCandidate) => {
		hiddenKeysRef.current[value.kind] = value.key;
		setCandidate((current) => (current?.key === value.key ? null : current));
	}, []);

	useEffect(() => {
		const doc = (element ?? textareaRef.current)?.ownerDocument;
		if (!doc) return;
		const record = (touch: boolean, updateHint = true) => {
			touchInputRef.current = touch;
			if (updateHint && candidateRef.current) setShowShortcut(!touch);
		};
		const onQuoteControl = (event: Event) =>
			!!targetElement(event.target)?.closest(
				"[data-inline-quote-candidate], [data-composer-quote-candidate]",
			);
		const pointer = (event: PointerEvent) =>
			record(event.pointerType === "touch" || event.pointerType === "pen", !onQuoteControl(event));
		const touch = (event: TouchEvent) => record(true, !onQuoteControl(event));
		const keyboard = (event: KeyboardEvent) => {
			// Virtual-keyboard/IME composition does not imply a physical keyboard was used.
			if (
				event.isComposing ||
				event.keyCode === 229 ||
				event.key === "Process" ||
				event.key === "Unidentified"
			)
				return;
			record(false);
		};
		doc.addEventListener("pointerdown", pointer, { capture: true, passive: true });
		doc.addEventListener("touchstart", touch, { capture: true, passive: true });
		doc.addEventListener("keydown", keyboard, true);
		return () => {
			doc.removeEventListener("pointerdown", pointer, true);
			doc.removeEventListener("touchstart", touch, true);
			doc.removeEventListener("keydown", keyboard, true);
		};
	}, [textareaRef, element]);

	useEffect(() => {
		const textarea = element ?? textareaRef.current;
		if (!textarea) return;
		const doc = textarea.ownerDocument;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const check = () => {
			const panel = textarea.closest(NARRATOR_PANEL_SELECTOR);
			const quote = panel ? readPanelQuoteSelection(panel, doc.getSelection()) : null;
			if (!quote) {
				hiddenKeysRef.current.text = null;
				setCandidate((current) => (current?.kind === "text" ? null : current));
				return;
			}
			const key = textQuoteKey(quote);
			if (key === hiddenKeysRef.current.text) return;
			setShowShortcut(!touchInputRef.current);
			setCandidate((current) => (current?.key === key ? current : { kind: "text", key, quote }));
		};
		const schedule = () => {
			clearTimeout(timer);
			timer = setTimeout(check, QUOTE_SELECTION_SETTLE_MS);
		};
		doc.addEventListener("selectionchange", schedule);
		return () => {
			clearTimeout(timer);
			doc.removeEventListener("selectionchange", schedule);
		};
	}, [textareaRef, element]);

	useEffect(() => {
		const owner = ownerRef.current;
		if (candidate) shortcutOwner = owner;
		else if (shortcutOwner === owner) shortcutOwner = 0;
	}, [candidate]);
	useEffect(
		() => () => {
			if (shortcutOwner === ownerRef.current) shortcutOwner = 0;
		},
		[],
	);

	useEffect(() => {
		const view = (element ?? textareaRef.current)?.ownerDocument.defaultView;
		if (!view) return;
		const handler = (event: KeyboardEvent) => {
			const current = candidateRef.current;
			const textarea = element ?? textareaRef.current;
			if (!current || !textarea || shortcutOwner !== ownerRef.current) return;
			if (textarea.getClientRects().length === 0) return;
			if (isQuoteShortcut(event) && canQuoteWithTab(current, textarea, event)) {
				event.preventDefault();
				event.stopPropagation();
				onShortcutRef.current(current);
			} else if (
				event.key === "Escape" &&
				!event.defaultPrevented &&
				!isEditableTarget(event.target)
			) {
				hide(current);
			}
		};
		// Capture only a live source selection; normal editor Tab and focus navigation remain native.
		view.addEventListener("keydown", handler, true);
		return () => view.removeEventListener("keydown", handler, true);
	}, [textareaRef, element, hide]);

	return { candidate, hide, showShortcut };
}
