/** Independent working-tree statistics; never part of the lightweight status scan. */
export interface GitLineStatsFile {
	path: string;
	stagedLinesAdded?: number;
	stagedLinesRemoved?: number;
	unstagedLinesAdded?: number;
	unstagedLinesRemoved?: number;
	/** False when this file could not be measured completely. */
	complete: boolean;
}

export interface GitLineStats {
	files: GitLineStatsFile[];
	/** Sum of measured staged + unstaged + untracked changes, not HEAD net diff. */
	linesAdded: number;
	linesRemoved: number;
	/** False means the totals are partial, not a complete measurement of zero. */
	complete: boolean;
}
