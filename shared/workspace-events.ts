/**
 * UI layout workspace change events, shared by the server broadcaster and the
 * frontend listener.
 *
 * These concern the per-user UI split-layout workspaces (`workspaces` table +
 * `workspace_panels` membership) — deliberately distinct from:
 *   - `workspace_context_changed` (a narrator's EXECUTION context: device/cwd);
 *   - `git_workspace_changed` (git worktree state).
 *
 * The frame is an invalidation SIGNAL, not a payload: it carries identifiers and
 * the new layout revision only, and clients re-fetch the workspace over HTTP.
 * Layout blobs can be megabytes; fanning them out over the socket would put that
 * serialization on the single JS thread once per connected device.
 */
export type WorkspaceChangeReason = "layout" | "membership" | "title" | "deleted";

export interface WorkspaceChangedEvent {
	type: "workspace_changed";
	workspaceId: string;
	reason: WorkspaceChangeReason;
	/**
	 * The revision the layout blob reached. Present for `reason: "layout"`; absent
	 * otherwise (membership/title writes do not bump the arrangement revision).
	 * Diagnostic only: clients re-fetch rather than trusting the frame.
	 */
	layoutRevision?: number;
}
