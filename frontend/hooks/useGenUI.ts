import { loadGenUiPreview, type PreviewBundle } from "@frontend/components/gen-ui/preview-loader";
import { GEN_UI_FILE_LIMITS, type GenUiFileReference } from "@shared/gen-ui-files";
import { useCallback, useEffect, useState } from "react";

/** Mounted only inside an open preview. No query cache, history, drafts or retained builds. */
export function useGenUI(narratorId: string, file: GenUiFileReference) {
	const [bundle, setBundle] = useState<PreviewBundle | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [attempt, setAttempt] = useState(0);
	const { toolUseId, toolCallId } = file;
	const reload = useCallback(() => {
		setBundle(null);
		setError(null);
		setAttempt((value) => value + 1);
	}, []);
	// biome-ignore lint/correctness/useExhaustiveDependencies: an explicit refresh must rebuild unchanged file references.
	useEffect(() => {
		const controller = new AbortController();
		let active = true;
		const timer = setTimeout(() => {
			controller.abort();
			if (active) setError("Preview build timed out");
		}, GEN_UI_FILE_LIMITS.timeoutMs + 5_000);
		setBundle(null);
		setError(null);
		void loadGenUiPreview(narratorId, { toolUseId, toolCallId }, controller.signal)
			.then((next) => {
				if (active && !controller.signal.aborted) setBundle(next);
			})
			.catch((reason: unknown) => {
				if (active && !controller.signal.aborted) {
					setError(reason instanceof Error ? reason.message : "Preview build failed");
					controller.abort();
				}
			})
			.finally(() => clearTimeout(timer));
		return () => {
			active = false;
			clearTimeout(timer);
			controller.abort();
		};
	}, [narratorId, toolUseId, toolCallId, attempt]);
	return { bundle, error, reload };
}
