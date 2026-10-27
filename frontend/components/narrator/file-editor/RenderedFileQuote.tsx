import type { FileReferenceEditorSelection, FileSelection } from "@shared/file-reference";
import { type ReactNode, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { QuoteSelectionButton } from "../composer/QuoteSelectionButton";
import {
	editorSelectionCandidate,
	QUOTE_SELECTION_SETTLE_MS,
	QUOTE_TOUCH_SELECTION_SETTLE_MS,
} from "../composer/use-quote-candidate";
import { readRenderedFileSelection } from "./rendered-file-selection";

interface RenderedSelection {
	key: string;
	selection: FileSelection;
	range: Range;
	backward: boolean;
}

/** Adapts a file preview to the existing quote UI; saved-file authority stays with its caller. */
export function RenderedFileQuote({
	text,
	ownerKey,
	source,
	disabledReason,
	onQuoteSelection,
	children,
}: {
	text: string;
	ownerKey: string;
	source?: FileReferenceEditorSelection | null;
	disabledReason?: string | null;
	onQuoteSelection?: (selection: FileSelection) => boolean;
	children: ReactNode;
}) {
	const rootRef = useRef<HTMLDivElement>(null);
	const activeRef = useRef(false);
	const touchRef = useRef(false);
	const [selected, setSelected] = useState<RenderedSelection | null>(null);
	const lines = useMemo(() => text.split(/\r\n|\r|\n/), [text]);
	const key = JSON.stringify([ownerKey, source ?? null]);
	const enabled = !!source && !!onQuoteSelection;
	const current = useRef({ source, disabledReason, onQuoteSelection, lines, key });
	current.current = { source, disabledReason, onQuoteSelection, lines, key };
	const candidate = useMemo(() => {
		if (!source || !selected || selected.key !== key) return null;
		const file = editorSelectionCandidate({
			...source,
			target: { ...source.target, selection: selected.selection },
		});
		return file ? { ...file, range: selected.range, backward: selected.backward } : null;
	}, [source, selected, key]);

	// biome-ignore lint/correctness/useExhaustiveDependencies: a new owner or preview invalidates the previous DOM range
	useLayoutEffect(() => {
		activeRef.current = false;
		setSelected(null);
	}, [key, text]);

	useEffect(() => {
		const root = rootRef.current;
		if (!root || !enabled) return;
		const doc = root.ownerDocument;
		const view = doc.defaultView;
		if (!view) return;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const clear = () => {
			activeRef.current = false;
			setSelected(null);
		};
		const update = () => {
			if (!activeRef.current) return;
			const selection = doc.getSelection();
			const range = readRenderedFileSelection(root, selection, current.current.lines);
			if (!range || !selection) {
				setSelected(null);
				return;
			}
			const domRange = selection.getRangeAt(0);
			setSelected({
				key: current.current.key,
				selection: range,
				range: domRange.cloneRange(),
				backward:
					selection.focusNode === domRange.startContainer &&
					selection.focusOffset === domRange.startOffset,
			});
		};
		const schedule = () => {
			if (!activeRef.current) return;
			clearTimeout(timer);
			timer = setTimeout(
				update,
				touchRef.current ? QUOTE_TOUCH_SELECTION_SETTLE_MS : QUOTE_SELECTION_SETTLE_MS,
			);
		};
		const pointerDown = (event: PointerEvent) => {
			const target = event.target as Element | null;
			if (target?.closest?.("[data-quote-candidate]")) return;
			if (!target || !root.contains(target)) {
				clear();
				return;
			}
			touchRef.current = event.pointerType === "touch" || event.pointerType === "pen";
			activeRef.current = true;
		};
		const accept = () => {
			const value = current.current;
			if (!activeRef.current || value.disabledReason || value.source?.dirty) return false;
			const range = readRenderedFileSelection(root, doc.getSelection(), value.lines);
			if (!range || !value.onQuoteSelection?.(range)) return false;
			const selection = doc.getSelection();
			if (selection?.anchorNode && root.contains(selection.anchorNode)) selection.removeAllRanges();
			clear();
			return true;
		};
		acceptRef.current = accept;
		const keyDown = (event: KeyboardEvent) => {
			if (
				event.key !== "Tab" ||
				event.defaultPrevented ||
				event.shiftKey ||
				event.altKey ||
				event.ctrlKey ||
				event.metaKey ||
				event.repeat ||
				event.isComposing ||
				event.keyCode === 229
			)
				return;
			if ((event.target as Element | null)?.closest?.("[data-quote-candidate]")) return;
			if (!accept()) return;
			event.preventDefault();
			event.stopImmediatePropagation();
		};
		doc.addEventListener("selectionchange", schedule);
		doc.addEventListener("pointerdown", pointerDown, true);
		doc.addEventListener("pointerup", schedule, true);
		doc.addEventListener("keydown", keyDown, true);
		return () => {
			clearTimeout(timer);
			activeRef.current = false;
			acceptRef.current = () => false;
			doc.removeEventListener("selectionchange", schedule);
			doc.removeEventListener("pointerdown", pointerDown, true);
			doc.removeEventListener("pointerup", schedule, true);
			doc.removeEventListener("keydown", keyDown, true);
		};
	}, [enabled]);
	const acceptRef = useRef<() => boolean>(() => false);

	return (
		<div ref={rootRef} data-rendered-file-quote>
			{children}
			<QuoteSelectionButton
				candidate={
					candidate && disabledReason ? { ...candidate, label: disabledReason } : candidate
				}
				anchor={
					rootRef.current?.closest<HTMLElement>(
						"[data-file-editor-preview], [data-file-viewer-preview]",
					) ?? rootRef.current
				}
				placement={touchRef.current ? "touch" : "selection"}
				onAccept={() => acceptRef.current()}
				onDismiss={() => {
					activeRef.current = false;
					setSelected(null);
				}}
				showShortcut={!touchRef.current}
				disabled={!!disabledReason || !candidate?.reference}
			/>
		</div>
	);
}
