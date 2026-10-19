import { useBlocker } from "@tanstack/react-router";
import { useCallback, useRef } from "react";

/**
 * Block in-app navigation (TanStack Router) and page unload while `active` is true.
 *
 * Returns the router's blocker resolver: while `status === "blocked"` the caller shows a
 * confirmation and calls `proceed()` (leave, discarding edits) or `reset()` (stay). Every
 * navigation is blocked — including a hash-only tab switch on the same page, because the
 * knowledge pages unmount inactive tabs and the edits would be lost just the same.
 *
 * `shouldBlockFn` reads the LATEST `active` through a ref assigned during render, so a caller
 * that clears its dirty state inside `flushSync` can navigate immediately afterwards (e.g.
 * "create, then open the new entry") without tripping its own guard.
 */
export function useUnsavedChangesGuard(active: boolean) {
	const activeRef = useRef(active);
	activeRef.current = active;
	const shouldBlockFn = useCallback(() => activeRef.current, []);
	return useBlocker({
		shouldBlockFn,
		enableBeforeUnload: active,
		disabled: !active,
		withResolver: true,
	});
}
