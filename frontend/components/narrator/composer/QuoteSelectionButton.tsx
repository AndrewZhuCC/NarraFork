import { Z } from "@frontend/lib/z-index";
import { IconArrowBarToRight, IconFileText, IconQuote, IconX } from "@tabler/icons-react";
import { useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import classes from "./QuoteSelectionButton.module.css";
import {
	type QuoteButtonBox,
	type QuoteButtonPlacement,
	quoteButtonPosition,
} from "./quote-button-position";
import { NARRATOR_PANEL_SELECTOR } from "./quote-selection";
import type { QuoteCandidate } from "./use-quote-candidate";

export interface QuoteSelectionButtonProps {
	candidate: QuoteCandidate | null;
	/** The composer shell: positions the touch placement and finds the owning panel/document. */
	anchor: HTMLElement | null;
	placement: QuoteButtonPlacement;
	onAccept(candidate: QuoteCandidate): void;
	onDismiss(candidate: QuoteCandidate): void;
	showShortcut?: boolean;
	disabled?: boolean;
}

function box(rect: DOMRect): QuoteButtonBox {
	return { top: rect.top, left: rect.left, right: rect.right, bottom: rect.bottom };
}

function selectionEnd(candidate: QuoteCandidate): QuoteButtonBox | null {
	if (candidate.kind !== "text" || !candidate.range) return null;
	const rects = [...candidate.range.getClientRects()].filter(
		(rect) => rect.width > 0 || rect.height > 0,
	);
	const rect = candidate.backward ? rects[0] : rects.at(-1);
	if (!rect) return null;
	// A backward selection ends at its first rect's left edge.
	return candidate.backward ? { ...box(rect), right: rect.left } : box(rect);
}

/**
 * Floating "quote selection" action. It is portalled and fixed-positioned so selecting
 * text never resizes the composer (which would move the text under the pointer).
 *
 * The iOS/Android selection menus cannot be extended or reliably suppressed from a web
 * page, so on touch the button sits on the composer's top edge instead of next to the
 * selection, where the platform menu (above) and the selection handles (below) live.
 */
export function QuoteSelectionButton({
	candidate,
	anchor,
	placement,
	onAccept,
	onDismiss,
	showShortcut = true,
	disabled = false,
}: QuoteSelectionButtonProps) {
	const { t } = useTranslation("narrator");
	const ref = useRef<HTMLDivElement>(null);
	const [position, setPosition] = useState<{ top: number; left: number } | null>(null);

	useLayoutEffect(() => {
		const element = ref.current;
		const view = anchor?.ownerDocument.defaultView;
		if (!candidate || !anchor || !element || !view) {
			setPosition(null);
			return;
		}
		const update = () => {
			const composer = anchor.getBoundingClientRect();
			// A hidden pane (e.g. inactive tab) has no box; neither does its button.
			if (composer.width === 0 && composer.height === 0) {
				setPosition(null);
				return;
			}
			const viewport = { width: view.innerWidth, height: view.innerHeight };
			const panel = anchor.closest(NARRATOR_PANEL_SELECTOR)?.getBoundingClientRect();
			const next = quoteButtonPosition({
				placement,
				selection: selectionEnd(candidate),
				bounds: panel
					? box(panel)
					: { top: 0, left: 0, right: viewport.width, bottom: viewport.height },
				composer: box(composer),
				viewport,
				size: { width: element.offsetWidth, height: element.offsetHeight },
			});
			setPosition((current) =>
				current && next && current.top === next.top && current.left === next.left ? current : next,
			);
		};
		update();
		let frame = 0;
		const schedule = () => {
			if (frame) return;
			frame = view.requestAnimationFrame(() => {
				frame = 0;
				update();
			});
		};
		const doc = anchor.ownerDocument;
		const visual = view.visualViewport;
		const resize = new view.ResizeObserver(schedule);
		resize.observe(anchor);
		resize.observe(element);
		doc.addEventListener("scroll", schedule, { capture: true, passive: true });
		view.addEventListener("resize", schedule);
		visual?.addEventListener("resize", schedule);
		visual?.addEventListener("scroll", schedule);
		return () => {
			if (frame) view.cancelAnimationFrame(frame);
			resize.disconnect();
			doc.removeEventListener("scroll", schedule, true);
			view.removeEventListener("resize", schedule);
			visual?.removeEventListener("resize", schedule);
			visual?.removeEventListener("scroll", schedule);
		};
	}, [candidate, anchor, placement]);

	if (!candidate || !anchor) return null;
	const unavailable = candidate.kind === "file" && !candidate.reference;
	const label = candidate.kind === "text" ? candidate.quote.label : candidate.label;
	const Icon = candidate.kind === "text" ? IconQuote : IconFileText;
	const keyboardShortcut = showShortcut && candidate.kind === "text";
	return createPortal(
		// biome-ignore lint/a11y/useSemanticElements: a fieldset would add form semantics to a toolbar.
		<div
			ref={ref}
			className={classes.root}
			data-quote-candidate
			data-placement={placement}
			role="group"
			aria-label={t("quote.add", { defaultValue: "Quote selection" })}
			style={{
				zIndex: Z.popover,
				top: position?.top ?? 0,
				left: position?.left ?? 0,
				visibility: position ? undefined : "hidden",
			}}
			// Keep the source selection (and focus) alive while the button is pressed.
			onPointerDown={(event) => event.preventDefault()}
		>
			<button
				type="button"
				className={classes.accept}
				aria-label={t("quote.add", { defaultValue: "Quote selection" })}
				aria-keyshortcuts={keyboardShortcut ? "Tab" : undefined}
				disabled={disabled || unavailable}
				title={
					unavailable
						? t("quote.fileUnsaved", { defaultValue: "Save the file before quoting" })
						: candidate.kind === "text"
							? candidate.quote.text
							: label
				}
				onClick={() => onAccept(candidate)}
			>
				{keyboardShortcut ? (
					<kbd
						className={classes.shortcut}
						aria-label={t("quote.tabKey")}
						title={t("quote.tabKey")}
					>
						<IconArrowBarToRight size={12} aria-hidden="true" />
						<span>Tab</span>
					</kbd>
				) : null}
				<Icon size={14} aria-hidden="true" data-quote-action />
				<span className={classes.label}>{label}</span>
			</button>
			<button
				type="button"
				className={classes.dismiss}
				aria-label={t("quote.dismiss", { defaultValue: "Dismiss" })}
				onClick={() => onDismiss(candidate)}
			>
				<IconX size={12} />
			</button>
		</div>,
		anchor.ownerDocument.body,
	);
}
