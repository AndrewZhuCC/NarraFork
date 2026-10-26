/** Git facts belong to a device/worktree, not a narrator. */
export type GitWorkspaceState =
	| "ready"
	| "not_git"
	| "missing_directory"
	| "git_unavailable"
	| "access_denied"
	| "device_offline"
	| "unsupported";

export interface GitWorkspace {
	workspaceKey: string | null;
	repositoryKey: string | null;
	deviceId: string;
	cwd: string;
	rootPath: string | null;
	state: GitWorkspaceState;
	/** Lightweight cached HEAD label; never used to authorize Git operations. */
	branch?: string | null;
	capabilities: { read: boolean; write: boolean };
	reason?: string;
	repositories?: Array<{ workspaceKey: string; rootPath: string; relativePath: string }>;
	discoveryTruncated?: boolean;
	narratorId?: string;
	projectId?: string;
}

/** Authorized display snapshot delivered together with narrator detail. */
export interface GitWorkspaceSummary {
	workspace: GitWorkspace;
	branch: string | null;
	revision: number;
}
