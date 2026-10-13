/** Independently paginated legacy permission and buffer projections. */
export interface NarratorPanelState<
	TPermission = Record<string, unknown>,
	TBuffer = Record<string, unknown>,
> {
	permissions: TPermission[];
	buffer: TBuffer[];
	draft: { hasDraft: boolean; revision: number; updatedAt: string | null };
	hasMore: { permissions: boolean; buffer: boolean };
	/** Opaque cursors bound to the narrator/collection. Send "end" to skip a completed collection. */
	nextCursor?: { permissions?: string; buffer?: string };
}

export interface NarratorNavigationSummary {
	parentNarratorId: string | null;
	parentNarratorTitle: string | null;
	chapterId: string | null;
	chapterTitle: string | null;
	projectId: string | null;
}

export const NARRATOR_PANEL_PAGE_SIZE = 32;
