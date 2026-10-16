import { IconFileText, IconQuote, IconX } from "@tabler/icons-react";
import { useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import classes from "./InlineComposerEditor.module.css";
import type { QuoteCandidate } from "./use-quote-candidate";

export interface InlineQuoteCandidateProps {
	candidate: QuoteCandidate | null;
	onAccept(candidate: QuoteCandidate): void;
	onDismiss(candidate: QuoteCandidate): void;
	anchorElement: HTMLElement | null;
	showShortcut?: boolean;
}

/** The candidate hook remains the sole multi-pane selection-Tab/Escape owner. */
export function InlineQuoteCandidate({
	candidate,
	onAccept,
	onDismiss,
	anchorElement,
	showShortcut = true,
}: InlineQuoteCandidateProps) {
	const { t } = useTranslation("narrator");
	const rootRef = useRef<HTMLFieldSetElement>(null);
	const [position, setPosition] = useState({ left: 8, top: 8, ready: false });
	useLayoutEffect(() => {
		if (!candidate || !anchorElement) return;
		const ownerWindow = anchorElement.ownerDocument.defaultView;
		if (!ownerWindow) return;
		const update = () => {
			const anchor = anchorElement.getBoundingClientRect();
			const box = rootRef.current?.getBoundingClientRect();
			const viewport = ownerWindow.visualViewport;
			const minX = (viewport?.offsetLeft ?? 0) + 8;
			const minY = (viewport?.offsetTop ?? 0) + 8;
			const maxX = minX + (viewport?.width ?? ownerWindow.innerWidth) - 16;
			const maxY = minY + (viewport?.height ?? ownerWindow.innerHeight) - 16;
			const height = box?.height ?? 34;
			const width = box?.width ?? 280;
			const above = anchor.top - height - 6;
			setPosition({
				left: Math.max(minX, Math.min(anchor.left, maxX - width)),
				top: Math.max(minY, Math.min(above >= minY ? above : anchor.bottom + 6, maxY - height)),
				ready: true,
			});
		};
		update();
		const observer = new ownerWindow.ResizeObserver(update);
		observer.observe(anchorElement);
		if (rootRef.current) observer.observe(rootRef.current);
		ownerWindow.addEventListener("resize", update);
		ownerWindow.addEventListener("scroll", update, true);
		ownerWindow.visualViewport?.addEventListener("resize", update);
		ownerWindow.visualViewport?.addEventListener("scroll", update);
		return () => {
			observer.disconnect();
			ownerWindow.removeEventListener("resize", update);
			ownerWindow.removeEventListener("scroll", update, true);
			ownerWindow.visualViewport?.removeEventListener("resize", update);
			ownerWindow.visualViewport?.removeEventListener("scroll", update);
		};
	}, [candidate, anchorElement]);
	if (!candidate || !anchorElement) return null;
	const unavailable = candidate.kind === "file" && !candidate.reference;
	const label = candidate.kind === "text" ? candidate.quote.label : candidate.label;
	const Icon = candidate.kind === "text" ? IconQuote : IconFileText;
	return createPortal(
		<fieldset
			ref={rootRef}
			className={classes.candidate}
			style={{
				left: position.left,
				top: position.top,
				visibility: position.ready ? "visible" : "hidden",
			}}
			data-inline-quote-candidate
			aria-label={t("quote.add", { defaultValue: "Quote selection" })}
			onPointerDown={(event) => event.preventDefault()}
		>
			<button
				type="button"
				className={classes.candidateAccept}
				disabled={unavailable}
				title={
					unavailable
						? t("quote.fileUnsaved", { defaultValue: "Save the file before quoting" })
						: label
				}
				onClick={() => onAccept(candidate)}
			>
				<Icon size={14} />
				<span className={classes.label}>
					{t("quote.add", { defaultValue: "Quote selection" })} · {label}
				</span>
				{showShortcut && candidate.kind === "text" && <kbd>Tab</kbd>}
			</button>
			<button
				type="button"
				className={classes.action}
				aria-label={t("quote.dismiss", { defaultValue: "Dismiss" })}
				onClick={() => onDismiss(candidate)}
			>
				<IconX size={14} />
			</button>
		</fieldset>,
		anchorElement.ownerDocument.body,
	);
}
