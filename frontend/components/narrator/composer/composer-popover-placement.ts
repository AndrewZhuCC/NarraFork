import { type CSSProperties, type RefObject, useLayoutEffect, useState } from "react";

export type ComposerPopoverSide = "above" | "below";

/**
 * Which side of the composer a popover opens on.
 *
 * Above is the default: the narrator composer sits at the bottom of its panel, so
 * the space is above it. A composer near the top of the viewport (the dashboard)
 * has the room below instead — opening above there clips the list off-screen.
 * Flip only when the popover would not fit above AND there is more room below.
 */
export function resolveComposerPopoverSide(
	anchor: { top: number; bottom: number },
	viewportHeight: number,
	popoverHeight: number,
): ComposerPopoverSide {
	const spaceAbove = anchor.top;
	const spaceBelow = viewportHeight - anchor.bottom;
	return spaceAbove < popoverHeight && spaceBelow > spaceAbove ? "below" : "above";
}

/** Measures the anchor when the popover opens (and on resize/scroll while open). */
export function useComposerPopoverSide(
	anchorRef: RefObject<HTMLElement | null> | undefined,
	visible: boolean,
	popoverHeight: number,
): ComposerPopoverSide {
	const [side, setSide] = useState<ComposerPopoverSide>("above");
	useLayoutEffect(() => {
		const anchor = anchorRef?.current;
		if (!visible || !anchor) return;
		const win = anchor.ownerDocument.defaultView;
		if (!win) return;
		const measure = () =>
			setSide(
				resolveComposerPopoverSide(anchor.getBoundingClientRect(), win.innerHeight, popoverHeight),
			);
		measure();
		win.addEventListener("resize", measure);
		win.addEventListener("scroll", measure, true);
		return () => {
			win.removeEventListener("resize", measure);
			win.removeEventListener("scroll", measure, true);
		};
	}, [anchorRef, visible, popoverHeight]);
	return side;
}

/** Absolute offset for a popover laid out against the composer's relative box. */
export function composerPopoverOffset(side: ComposerPopoverSide): CSSProperties {
	return side === "below" ? { top: "100%", marginTop: 4 } : { bottom: "100%", marginBottom: 4 };
}
