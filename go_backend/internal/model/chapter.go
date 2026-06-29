package model

// Chapter is a unit of work: a git worktree + AI session. Chapters form the
// nodes of a project's story network.
type Chapter struct {
	ID          string  `json:"id"`
	ProjectID   string  `json:"projectId"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
	Status      string  `json:"status"` // active | dormant | merged | abandoned | frozen
	Role        string  `json:"role"`   // trunk | branch | exploration | review

	Branch       string  `json:"branch"`
	WorktreePath *string `json:"worktreePath"`
	BaseBranch   string  `json:"baseBranch"`

	ParentChapterID     *string `json:"parentChapterId"`
	ForkPoint           *string `json:"forkPoint"` // raw JSON
	MergedIntoChapterID *string `json:"mergedIntoChapterId"`
	MergeCommitSha      *string `json:"mergeCommitSha"`
	MergeStrategy       *string `json:"mergeStrategy"` // merge | squash | cherry-pick
	PreMergeTargetSha   *string `json:"preMergeTargetSha"`
	ContainerConfig     *string `json:"containerConfig"` // raw JSON

	ExplorationGroupID *string `json:"explorationGroupId"`

	IsRoot bool `json:"isRoot"`

	HeadCommitSha  *string `json:"headCommitSha"`
	StartCommitSha *string `json:"startCommitSha"`
	CommitCount    int     `json:"commitCount"`

	// Graph visualization
	Color           *string  `json:"color"`
	GroupLabel      *string  `json:"groupLabel"`
	Pinned          bool     `json:"pinned"`
	AnchorCommitSha *string  `json:"anchorCommitSha"`
	AxisOffset      float64  `json:"axisOffset"`
	CrossOffset     float64  `json:"crossOffset"`
	PanelExpanded   bool     `json:"panelExpanded"`
	PanelWidth      *float64 `json:"panelWidth"`
	PanelHeight     *float64 `json:"panelHeight"`

	// Review
	ReviewSourceChapterID *string `json:"reviewSourceChapterId"`
	ReviewStatus          *string `json:"reviewStatus"` // reviewing | concluded | converted | dismissed

	LastAccessedAt *string `json:"lastAccessedAt"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
}

// Chapter status constants.
const (
	ChapterStatusActive    = "active"
	ChapterStatusDormant   = "dormant"
	ChapterStatusMerged    = "merged"
	ChapterStatusAbandoned = "abandoned"
	ChapterStatusFrozen    = "frozen"
)

// Chapter role constants.
const (
	ChapterRoleTrunk       = "trunk"
	ChapterRoleBranch      = "branch"
	ChapterRoleExploration = "exploration"
	ChapterRoleReview      = "review"
)

// Merge strategy constants.
const (
	MergeStrategyMerge      = "merge"
	MergeStrategySquash     = "squash"
	MergeStrategyCherryPick = "cherry-pick"
)
