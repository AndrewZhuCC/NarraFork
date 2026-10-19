/**
 * One tool-call inspector for a message list.
 *
 * The menu that opens it lives on a virtual row. That row is unmounted as soon as
 * it leaves the overscan window — streaming follow, scroll anchoring, a layout
 * pass — and a modal mounted inside the row unmounts with it. Compact-summary and
 * the fullscreen viewer already sit on the shell for this reason; the inspector
 * does too.
 *
 * Rows call `openHostedToolCallInspector`. A list that mounted
 * `useToolCallInspectorHost` accepts the request and renders the dialog. A row
 * rendered without that host (unit tests, a surface that owns its own dialog)
 * gets `false` back and keeps its local fallback.
 */

import { lazy, type ReactNode, Suspense, useCallback, useEffect, useState } from "react";

const ToolCallInspector = lazy(() =>
	import("./ToolCallInspector").then((m) => ({ default: m.ToolCallInspector })),
);

export interface ToolCallInspectorRequest {
	narratorId: string;
	toolUseId: string;
	toolCallId?: string;
	messageId?: string;
	executionAttempt?: number;
}

const hosts = new Map<string, Set<(request: ToolCallInspectorRequest) => void>>();

export function registerToolCallInspectorHost(
	narratorId: string,
	open: (request: ToolCallInspectorRequest) => void,
): () => void {
	// Several lists can be mounted for one narrator (docked panels). A single slot
	// would let the later list's unmount drop the earlier list's host.
	let bucket = hosts.get(narratorId);
	if (!bucket) {
		bucket = new Set();
		hosts.set(narratorId, bucket);
	}
	bucket.add(open);
	return () => {
		const current = hosts.get(narratorId);
		if (!current) return;
		current.delete(open);
		if (current.size === 0) hosts.delete(narratorId);
	};
}

/**
 * @returns true when at least one mounted list host accepted the request.
 * Every host for that narrator opens; the click cannot name which list it came
 * from, and a second panel of the same narrator should show the same call.
 */
export function openHostedToolCallInspector(request: ToolCallInspectorRequest): boolean {
	const bucket = hosts.get(request.narratorId);
	if (!bucket || bucket.size === 0 || !request.toolUseId) return false;
	for (const open of bucket) open(request);
	return true;
}

export function useToolCallInspectorHost(narratorId: string): ReactNode {
	const [request, setRequest] = useState<ToolCallInspectorRequest | null>(null);
	const open = useCallback((next: ToolCallInspectorRequest) => {
		setRequest(next);
	}, []);
	useEffect(() => {
		setRequest((current) => (current?.narratorId === narratorId ? current : null));
		return registerToolCallInspectorHost(narratorId, open);
	}, [narratorId, open]);
	const close = useCallback(() => setRequest(null), []);
	if (!request) return null;
	return (
		<Suspense fallback={null}>
			<ToolCallInspector
				narratorId={request.narratorId}
				toolUseId={request.toolUseId}
				toolCallId={request.toolCallId}
				messageId={request.messageId}
				executionAttempt={request.executionAttempt}
				opened
				onClose={close}
			/>
		</Suspense>
	);
}
