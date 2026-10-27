/**
 * Outline panels on a dockview surface: finding the panel a request belongs to and writing a
 * panel's outline params. Shared by the session page's dock, the workspace and the outline
 * page's dock, so all three apply the same identity rule (`lib/outline-panel.ts`).
 */
import type { IDockviewPanel } from "dockview-react";
import {
	isSameOutlinePanel,
	normalizeOutlinePanelState,
	type OutlinePanelState,
} from "../../../lib/outline-panel";

/**
 * Every outline field, absent ones as `undefined`: dockview merges updated params and deletes
 * keys set to `undefined`, so this replaces the panel's outline state as a whole and leaves its
 * other params (`panelType`, `hostNarratorId`, `panelRowId`) alone.
 */
export function outlineParamsPatch(state: OutlinePanelState): {
	[K in keyof Required<OutlinePanelState>]: OutlinePanelState[K] | undefined;
} {
	return {
		spaceId: state.spaceId,
		blockId: state.blockId,
		pageId: state.pageId,
		view: state.view,
		asOf: state.asOf,
		title: state.title,
	};
}

/** The outline state of a live panel, or null when it is not an outline panel. */
export function outlineStateOfPanel(panel: IDockviewPanel): OutlinePanelState | null {
	const params = panel.params as { panelType?: unknown } | undefined;
	return params?.panelType === "outline" ? normalizeOutlinePanelState(params) : null;
}

/** The outline panel `request` opens in (one per space and page), if `accept`ed panels have it. */
export function findOutlinePanel(
	panels: readonly IDockviewPanel[],
	request: OutlinePanelState,
	accept: (panel: IDockviewPanel) => boolean = () => true,
): IDockviewPanel | undefined {
	return panels.find((panel) => {
		const state = outlineStateOfPanel(panel);
		return state !== null && accept(panel) && isSameOutlinePanel(state, request);
	});
}

/**
 * Navigate an existing panel to `request`, keeping its title until the new place loads (so the
 * tab does not flash a generic name).
 */
export function navigateOutlinePanel(panel: IDockviewPanel, request: OutlinePanelState): void {
	const current = outlineStateOfPanel(panel) ?? {};
	panel.api.updateParameters(
		outlineParamsPatch({ ...request, title: request.title ?? current.title }),
	);
}
