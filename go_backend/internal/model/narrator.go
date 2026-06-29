package model

// Narrator is an AI session bound to a chapter (or standalone). Type is
// either "primary" or "subagent".
type Narrator struct {
	ID                string  `json:"id"`
	ChapterID         *string `json:"chapterId"`
	APIConversationID *string `json:"apiConversationId"`
	ForkMessageID     *string `json:"forkMessageId"`
	Type              string  `json:"type"` // primary | subagent
	SubagentType      *string `json:"subagentType"`
	Title             *string `json:"title"`
	InheritMode       string  `json:"inheritMode"` // full | compressed | fresh
	ParentNarratorID  *string `json:"parentNarratorId"`
	ContextSummary    *string `json:"contextSummary"`

	Model               *string `json:"model"`
	PendingModelRestore *string `json:"pendingModelRestore"`
	SystemPrompt        *string `json:"systemPrompt"`

	PermissionMode         *string `json:"permissionMode"` // default|acceptEdits|bypassPermissions|readOnly|dontAsk
	PreviousPermissionMode *string `json:"previousPermissionMode"`
	PlanFileID             *string `json:"planFileId"`
	ReasoningEffort        *string `json:"reasoningEffort"` // none|low|medium|high|xhigh|max

	FastMode    bool `json:"fastMode"`
	RelaxedPlan bool `json:"relaxedPlan"`

	PlanReflectionAutoApproveOverride string `json:"planReflectionAutoApproveOverride"` // inherit|on|off
	DangerReflectionOverride          string `json:"dangerReflectionOverride"`          // inherit|on|off|light|standard|strict

	MessageCount  int     `json:"messageCount"`
	TotalCostUSD  float64 `json:"totalCostUsd"`
	LastMessageAt *string `json:"lastMessageAt"`

	Status    string `json:"status"`    // idle|working|waiting|archived
	Substatus string `json:"substatus"` // JSON array
	PlanMode  bool   `json:"planMode"`

	CWD          *string `json:"cwd"`
	ErrorMessage *string `json:"errorMessage"`

	TodosJSON      *string `json:"todosJson"` // raw JSON
	TodosToolUseID *string `json:"todosToolUseId"`

	PruneBoundaryMessageID *string `json:"pruneBoundaryMessageId"`
	PrunedPercent          *int    `json:"prunedPercent"`
	PruneEnabled           bool    `json:"pruneEnabled"`

	EnabledTools *string `json:"enabledTools"` // raw JSON string[] (nullable)
	Variant      string  `json:"variant"`      // primary | subagent:<type>
	Traits       string  `json:"traits"`       // JSON string[]
	Handle       *string `json:"handle"`

	IsBackground          bool    `json:"isBackground"`
	BackgroundStatus      *string `json:"backgroundStatus"` // running|completed|failed|cancelled
	BackgroundResult      *string `json:"backgroundResult"`
	BackgroundCompletedAt *string `json:"backgroundCompletedAt"`

	IsAskInPassing bool    `json:"isAskInPassing"`
	TurnStartedAt  *string `json:"turnStartedAt"`
	MessageVersion int     `json:"messageVersion"`

	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// Narrator type constants.
const (
	NarratorTypePrimary  = "primary"
	NarratorTypeSubagent = "subagent"
)

// Narrator inherit mode constants.
const (
	InheritModeFull       = "full"
	InheritModeCompressed = "compressed"
	InheritModeFresh      = "fresh"
)

// Narrator status constants.
const (
	NarratorStatusIdle     = "idle"
	NarratorStatusWorking  = "working"
	NarratorStatusWaiting  = "waiting"
	NarratorStatusArchived = "archived"
)

// Permission mode constants.
const (
	PermissionModeDefault           = "default"
	PermissionModeAcceptEdits       = "acceptEdits"
	PermissionModeBypassPermissions = "bypassPermissions"
	PermissionModeReadOnly          = "readOnly"
	PermissionModeDontAsk           = "dontAsk"
)
