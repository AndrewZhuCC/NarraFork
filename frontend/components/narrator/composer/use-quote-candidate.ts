import type { FileReference, FileReferenceEditorSelection } from "@shared/file-reference";
import { type RefObject, useCallback, useEffect, useRef, useState } from "react";
import type { ComposerQuote } from "./composer-quotes";
import { fileReferenceToken, savedSelectionReference } from "./file-reference-input";
import {
	NARRATOR_PANEL_SELECTOR,
	type QuoteSourceLabels,
	readPanelQuoteSelection,
} from "./quote-selection";

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
		event.code === "KeyQ" &&
		event.altKey &&
		!event.ctrlKey &&
		!event.metaKey &&
		!event.shiftKey &&
		!event.repeat &&
		!event.isComposing
	);
}

function isEditableTarget(target: EventTarget | null): boolean {
	return (
		target instanceof Element &&
		!!target.closest(
			'input, textarea, select, [contenteditable]:not([contenteditable="false"]), .monaco-editor, [role="dialog"], [role="menu"], [role="listbox"]',
		)
	);
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
 * The "temporary chip": the latest quotable selection in this composer's own
 * message list or in a file panel publishing to this narrator. Consuming or
 * dismissing a candidate hides it until that source's selection changes.
 */
export function useQuoteCandidate({
	textareaRef,
	editorSelection,
	labels,
	onShortcut,
}: {
	textareaRef: RefObject<HTMLTextAreaElement | null>;
	editorSelection: FileReferenceEditorSelection | null | undefined;
	labels: QuoteSourceLabels;
	onShortcut: (candidate: QuoteCandidate) => void;
}) {
	const [candidate, setCandidate] = useState<QuoteCandidate | null>(null);
	const candidateRef = useRef(candidate);
	candidateRef.current = candidate;
	const labelsRef = useRef(labels);
	labelsRef.current = labels;
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
		const textarea = textareaRef.current;
		if (!textarea) return;
		const doc = textarea.ownerDocument;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const check = () => {
			const panel = textarea.closest(NARRATOR_PANEL_SELECTOR);
			const quote = panel
				? readPanelQuoteSelection(panel, doc.getSelection(), labelsRef.current)
				: null;
			if (!quote) {
				hiddenKeysRef.current.text = null;
				setCandidate((current) => (current?.kind === "text" ? null : current));
				return;
			}
			const key = JSON.stringify([quote.label, quote.text]);
			if (key === hiddenKeysRef.current.text) return;
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
	}, [textareaRef]);

	useEffect(() => {
		const next = editorSelectionCandidate(editorSelection);
		if (!next) {
			hiddenKeysRef.current.file = null;
			setCandidate((current) => (current?.kind === "file" ? null : current));
			return;
		}
		if (next.key === hiddenKeysRef.current.file) return;
		setCandidate((current) =>
			current?.kind === "file" && current.key === next.key && !current.reference === !next.reference
				? current
				: next,
		);
	}, [editorSelection]);

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
		const view = textareaRef.current?.ownerDocument.defaultView;
		if (!view) return;
		const handler = (event: KeyboardEvent) => {
			const current = candidateRef.current;
			const textarea = textareaRef.current;
			if (!current || !textarea || shortcutOwner !== ownerRef.current) return;
			if (textarea.getClientRects().length === 0) return;
			if (isQuoteShortcut(event)) {
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
		// Capture phase: Monaco and other editors must not see the shortcut first.
		view.addEventListener("keydown", handler, true);
		return () => view.removeEventListener("keydown", handler, true);
	}, [textareaRef, hide]);

	return { candidate, hide };
}
