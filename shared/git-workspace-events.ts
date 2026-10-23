import type { GitLineStatsFile } from "./git-line-stats";

export type GitWorkspaceCategory = "worktree" | "index" | "refs" | "head" | "stash";
export type GitWorkspaceView = "summary" | "details";

/** A projection of shared Git facts, never permissions or modification attribution. */
export interface GitWorkspaceSnapshot {
	revision: number;
	updating: boolean;
	status?: {
		hasChanges: boolean;
		staged: number;
		unstaged: number;
		untracked: number;
		totalFiles: number;
		totalFilesExact?: boolean;
		headSha: string;
		branch: string;
		truncated?: boolean;
		/** Only present in the details projection. */
		files?: Array<{ status: string; path: string; oldPath?: string }>;
	};
	lineStats?: {
		linesAdded: number;
		linesRemoved: number;
		complete: boolean;
		/** Only present in the details projection; totals cover the whole measurement. */
		files?: GitLineStatsFile[];
	};
	statusError?: string;
	lineStatsError?: string;
	/** The transmitted detail list was capped, independently of measurement completeness. */
	detailsTruncated?: boolean;
}

export interface GitWorkspaceSubscribe {
	type: "git_workspace_subscribe";
	subscriptionId: string;
	narratorId?: string;
	workspaceKey?: string;
	view?: GitWorkspaceView;
}
export type GitWorkspaceClientMessage =
	| GitWorkspaceSubscribe
	| {
			type: "git_workspace_unsubscribe" | "git_workspace_refresh";
			subscriptionId: string;
	  };
export interface GitWorkspaceEventIdentity {
	subscriptionId: string;
	version: number;
	workspaceKey: string;
	repositoryKey: string;
	narratorId?: string;
}
export type GitWorkspaceServerMessage =
	| ({ type: "git_workspace_subscribed" } & GitWorkspaceEventIdentity)
	| ({
			type: "git_workspace_changed";
			categories: GitWorkspaceCategory[];
	  } & GitWorkspaceEventIdentity)
	| ({
			type: "git_workspace_snapshot";
			view: GitWorkspaceView;
			snapshot: GitWorkspaceSnapshot;
	  } & GitWorkspaceEventIdentity)
	| { type: "git_workspace_error"; subscriptionId: string; code: string; message: string };
