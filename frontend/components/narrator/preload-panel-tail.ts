interface TailPreloadModule {
	firstScreenPageSizeForLod(lod: number): number;
	preloadPretextDocumentTail(
		id: string,
		opts: { firstScreenPageSize: number; signal: AbortSignal },
	): Promise<unknown>;
}

/** Only pending-detail panels need a fallback producer; the mounted list owns ready data. */
export async function preloadPendingPanelTail({
	narratorId,
	signal,
	readOpeningLod,
	getCurrent,
	loadModule,
}: {
	narratorId: string;
	signal: AbortSignal;
	readOpeningLod: () => number;
	getCurrent: () => { narratorId: string; detailReady: boolean };
	loadModule: () => Promise<TailPreloadModule>;
}): Promise<void> {
	const shouldSkip = () => {
		const current = getCurrent();
		return signal.aborted || current.narratorId !== narratorId || current.detailReady;
	};
	if (shouldSkip()) return;
	const module = await loadModule();
	// Dynamic imports resolve asynchronously: canonical child loading can finish
	// in between. Never start a second tail after it consumed the shared handoff.
	if (shouldSkip()) return;
	await module.preloadPretextDocumentTail(narratorId, {
		firstScreenPageSize: module.firstScreenPageSizeForLod(readOpeningLod()),
		signal,
	});
}
