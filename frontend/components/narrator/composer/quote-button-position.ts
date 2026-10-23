export interface QuoteButtonBox {
	top: number;
	left: number;
	right: number;
	bottom: number;
}

/**
 * `selection`: next to the selection end (mouse). `composer`: on the composer's top edge (touch),
 * away from the platform selection menu and handles that iOS/Android draw around the selection.
 */
export type QuoteButtonPlacement = "selection" | "composer";

export interface QuoteButtonLayout {
	placement: QuoteButtonPlacement;
	/** Rect of the selection end; null falls back to the composer edge. */
	selection: QuoteButtonBox | null;
	/** Visible message area: the selection anchor must be inside it, the button stays inside it. */
	bounds: QuoteButtonBox;
	composer: QuoteButtonBox;
	viewport: { width: number; height: number };
	size: { width: number; height: number };
}

export const QUOTE_BUTTON_GAP = 6;
const EDGE_MARGIN = 4;

function clamp(value: number, min: number, max: number): number {
	return Math.max(min, Math.min(max, value));
}

/** Fixed-position top/left for the floating quote button, or null when it must stay hidden. */
export function quoteButtonPosition(
	layout: QuoteButtonLayout,
): { top: number; left: number } | null {
	const { selection, bounds, composer, viewport, size } = layout;
	const maxLeft = Math.max(EDGE_MARGIN, viewport.width - size.width - EDGE_MARGIN);
	if (layout.placement === "composer" || !selection) {
		if (composer.right <= composer.left) return null;
		return {
			top: Math.max(EDGE_MARGIN, composer.top - QUOTE_BUTTON_GAP - size.height),
			left: clamp(composer.right - size.width, EDGE_MARGIN, maxLeft),
		};
	}
	const top = Math.max(bounds.top, EDGE_MARGIN);
	const bottom = Math.min(bounds.bottom, composer.top, viewport.height);
	// The selection end scrolled out of the visible message area.
	if (selection.bottom <= top || selection.top >= bottom || bottom - top < size.height) return null;
	const above = selection.top - QUOTE_BUTTON_GAP - size.height;
	const below = selection.bottom + QUOTE_BUTTON_GAP;
	const y =
		above >= top
			? above
			: below + size.height <= bottom
				? below
				: clamp(above, top, bottom - size.height);
	const minLeft = Math.max(EDGE_MARGIN, bounds.left);
	return {
		top: y,
		left: clamp(
			selection.right - size.width / 2,
			minLeft,
			Math.max(minLeft, Math.min(maxLeft, bounds.right - size.width)),
		),
	};
}
