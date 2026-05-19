package narrators

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"narrafork-go/internal/agentbasic"
	"narrafork-go/internal/config"
	"narrafork-go/internal/db"
	gitx "narrafork-go/internal/git"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
	"narrafork-go/internal/jsondb"
	"narrafork-go/internal/simplecontent"
	"narrafork-go/internal/ws"
)

type ExternalToolContext struct {
	NarratorID       string
	ParentNarratorID string
	PermissionMode   string
	SubagentType     string
	Type             string
	Variant          string
	ToolAccess       string
	CustomTools      []string
}

type ExternalToolProvider interface {
	AgentToolSpecs(ctx context.Context, info ExternalToolContext) []agentbasic.ExternalToolSpec
	ResolveToolPermission(toolName string, permissionMode string) (action string, reason string, handled bool)
	ExecuteTool(ctx context.Context, toolName string, args map[string]any) (agentbasic.ToolRun, bool)
}

type Service struct {
	DB        *sql.DB
	RuntimeDB *sql.DB
	// BrowserSessionPersistenceEnabled keeps the Go-owned browser-session extension
	// disabled by default so Bun-final shared schema stays untouched unless an
	// explicit cutover enables it.
	BrowserSessionPersistenceEnabled bool
	Hub                              *ws.Hub
	Settings                         *config.Store
	ExternalTools                    ExternalToolProvider
	TerminalRuntime                  terminalAgentRuntime
	contextOverflowRetry             func(context.Context, string)

	runMu          sync.Mutex
	activeRuns     map[string]context.CancelFunc
	activeRunToken map[string]string
}

type askQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Preview     string `json:"preview,omitempty"`
}

type askQuestionInput struct {
	Question    string              `json:"question"`
	Header      string              `json:"header"`
	Options     []askQuestionOption `json:"options"`
	MultiSelect bool                `json:"multiSelect"`
}

type Narrator struct {
	ID                                string   `json:"id"`
	ChapterID                         *string  `json:"chapterId"`
	APIConversationID                 *string  `json:"apiConversationId,omitempty"`
	ForkMessageID                     *string  `json:"forkMessageId,omitempty"`
	Type                              string   `json:"type"`
	SubagentType                      *string  `json:"subagentType,omitempty"`
	Title                             *string  `json:"title"`
	InheritMode                       string   `json:"inheritMode"`
	ParentNarratorID                  *string  `json:"parentNarratorId,omitempty"`
	ContextSummary                    *string  `json:"contextSummary,omitempty"`
	Model                             *string  `json:"model"`
	SystemPrompt                      *string  `json:"systemPrompt,omitempty"`
	PermissionMode                    *string  `json:"permissionMode"`
	PreviousPermissionMode            *string  `json:"previousPermissionMode,omitempty"`
	PlanFileID                        *string  `json:"planFileId,omitempty"`
	ReasoningEffort                   *string  `json:"reasoningEffort,omitempty"`
	FastMode                          bool     `json:"fastMode"`
	RelaxedPlan                       bool     `json:"relaxedPlan"`
	PlanReflectionAutoApproveOverride string   `json:"planReflectionAutoApproveOverride"`
	DangerReflectionOverride          string   `json:"dangerReflectionOverride"`
	MessageCount                      int      `json:"messageCount"`
	TotalCostUSD                      float64  `json:"totalCostUsd"`
	LastMessageAt                     *string  `json:"lastMessageAt"`
	Status                            string   `json:"status"`
	Substatus                         []string `json:"substatus"`
	PlanMode                          bool     `json:"planMode"`
	Cwd                               *string  `json:"cwd,omitempty"`
	ErrorMessage                      *string  `json:"errorMessage,omitempty"`
	TodosJSON                         any      `json:"todosJson,omitempty"`
	TodosToolUseID                    *string  `json:"todosToolUseId,omitempty"`
	EnabledTools                      any      `json:"enabledTools"`
	Variant                           string   `json:"variant"`
	Traits                            []string `json:"traits"`
	IsBackground                      bool     `json:"isBackground"`
	BackgroundStatus                  *string  `json:"backgroundStatus,omitempty"`
	BackgroundResult                  *string  `json:"backgroundResult,omitempty"`
	BackgroundCompletedAt             *string  `json:"backgroundCompletedAt,omitempty"`
	IsAskInPassing                    bool     `json:"isAskInPassing"`
	MessageVersion                    int      `json:"messageVersion"`
	CreatedAt                         string   `json:"createdAt"`
	UpdatedAt                         string   `json:"updatedAt"`
}

type ToolCallRecord struct {
	ID                       string  `json:"id"`
	NarratorID               string  `json:"narratorId"`
	MessageID                string  `json:"messageId"`
	ToolUseID                string  `json:"toolUseId"`
	ToolName                 string  `json:"toolName"`
	InputJSON                any     `json:"inputJson"`
	OutputJSON               any     `json:"outputJson"`
	Status                   string  `json:"status"`
	ErrorMessage             *string `json:"errorMessage,omitempty"`
	PermissionDecisionReason *string `json:"permissionDecisionReason,omitempty"`
	PermissionSuggestions    any     `json:"permissionSuggestions,omitempty"`
	CreatedAt                string  `json:"createdAt"`
}

type Message struct {
	ID              string           `json:"id"`
	NarratorID      string           `json:"narratorId"`
	MessageUUID     *string          `json:"sdkMessageUuid,omitempty"`
	ParentToolUseID *string          `json:"parentToolUseId,omitempty"`
	Role            string           `json:"role"`
	ContentJSON     any              `json:"contentJson"`
	ContentText     *string          `json:"contentText"`
	Model           *string          `json:"model,omitempty"`
	CommitSHA       *string          `json:"commitSha,omitempty"`
	CommandText     *string          `json:"commandText,omitempty"`
	CreatedBy       *string          `json:"createdBy,omitempty"`
	Seq             int              `json:"seq,omitempty"`
	CreatedAt       string           `json:"createdAt"`
	ToolCalls       []ToolCallRecord `json:"toolCalls"`
	Children        []Message        `json:"children"`
}

type MessagePage struct {
	Messages     []Message `json:"messages"`
	HasMore      bool      `json:"hasMore"`
	NextCursor   *string   `json:"nextCursor"`
	HasMoreAfter bool      `json:"hasMoreAfter"`
	PrevCursor   *string   `json:"prevCursor"`
}

type BufferedMessage struct {
	ID                string
	NarratorID        string
	Text              string
	ImagesJSON        any
	CommandText       *string
	CreatedBy         *string
	CreatorJSON       any
	TextFilePathsJSON any
	Priority          bool
	Seq               int
	BufferedAt        string
}

type CatchUpChildAnchor struct {
	ParentToolUseID string  `json:"parentToolUseId"`
	NarratorID      *string `json:"narratorId,omitempty"`
	LastMessageID   *string `json:"lastMessageId,omitempty"`
}

type CatchUpCursor struct {
	ParentLastMessageID *string              `json:"parentLastMessageId,omitempty"`
	ChildAnchors        []CatchUpChildAnchor `json:"childAnchors"`
}

type CatchUpPage struct {
	TopLevel       []Message      `json:"topLevel"`
	OrphanChildren []Message      `json:"orphanChildren"`
	HitLimit       bool           `json:"hitLimit"`
	Cursor         *CatchUpCursor `json:"cursor,omitempty"`
}

type messageRef struct {
	NarratorID string
	MessageID  string
	Seq        int
}

type reviewChapterMeta struct {
	ID              string
	Role            string
	ReviewStatus    sql.NullString
	SourceChapterID sql.NullString
	WorktreePath    sql.NullString
	StartCommitSHA  sql.NullString
	HeadCommitSHA   sql.NullString
}

type resolvedChildAnchor struct {
	ParentToolUseID string
	NarratorID      string
	LastMessageID   string
	Seq             int
}

const maxCatchUpChildAnchors = 32

type CreateInput struct {
	ChapterID                         *string
	Title                             *string
	Model                             string
	SystemPrompt                      *string
	PermissionMode                    string
	StartInPlanMode                   *bool
	ReasoningEffort                   *string
	FastMode                          *bool
	RelaxedPlan                       *bool
	PlanReflectionAutoApproveOverride string
	DangerReflectionOverride          string
	Cwd                               *string
}

type ForkNarratorOptions struct {
	Title        string
	NewChapterID *string
	InheritMode  string
	Cwd          *string
	Locale       string
	Standalone   bool
}

func NewService(sqlDB *sql.DB, hub *ws.Hub, settings *config.Store) *Service {
	service := &Service{DB: sqlDB, Hub: hub, Settings: settings}
	agentbasic.SetBrowserSessionObserver(service)
	if hub != nil {
		hub.SetNarratorSyncProvider(service)
	}
	return service
}

func (s *Service) SetRuntimeDB(sqlDB *sql.DB) {
	if s == nil {
		return
	}
	s.RuntimeDB = sqlDB
	if s.BrowserSessionPersistenceEnabled {
		_ = s.EnsureBrowserSessionStorage(context.Background())
		_ = s.importLegacyBrowserSessions(context.Background())
	}
}

func (s *Service) EnableBrowserSessionPersistence() {
	if s == nil {
		return
	}
	s.BrowserSessionPersistenceEnabled = true
	if s.RuntimeDB != nil {
		_ = s.EnsureBrowserSessionStorage(context.Background())
		_ = s.importLegacyBrowserSessions(context.Background())
	}
}

func (s *Service) DisableBrowserSessionPersistence() {
	if s == nil {
		return
	}
	s.BrowserSessionPersistenceEnabled = false
}

func (s *Service) browserDB() *sql.DB {
	if s == nil || !s.BrowserSessionPersistenceEnabled {
		return nil
	}
	if s.RuntimeDB != nil {
		return s.RuntimeDB
	}
	return s.DB
}

func (s *Service) RecoverOnStartup(ctx context.Context) error {
	now := db.NowISO()
	if _, err := s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["interrupted"]', updated_at = ? WHERE status IN ('working', 'waiting')`, now); err != nil {
		return err
	}

	rows, err := s.DB.QueryContext(ctx, `SELECT id, permission_suggestions FROM narrator_tool_calls WHERE status = 'pending'`)
	if err != nil {
		return err
	}
	defer rows.Close()

	const restartMessage = "Interrupted by server restart"
	type stalePermission struct {
		id          string
		suggestions sql.NullString
	}
	stale := []stalePermission{}
	for rows.Next() {
		var row stalePermission
		if err := rows.Scan(&row.id, &row.suggestions); err != nil {
			return err
		}
		stale = append(stale, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, row := range stale {
		abortedSuggestions, changed := abortActiveReflectionSuggestionsRaw(row.suggestions, restartMessage, now)
		if changed {
			if _, err := s.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET status = 'fail', error_message = ?, permission_decided_by = 'server_restart', permission_decided_at = ?, permission_decision_reason = ?, permission_suggestions = ? WHERE id = ?`, restartMessage, now, restartMessage, abortedSuggestions, row.id); err != nil {
				return err
			}
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET status = 'fail', error_message = ?, permission_decided_by = 'server_restart', permission_decided_at = ? WHERE id = ?`, restartMessage, now, row.id); err != nil {
			return err
		}
	}

	outputJSON, _ := json.Marshal(map[string]any{"output": restartMessage})
	if _, err := s.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET status = 'fail', error_message = ?, output_json = COALESCE(output_json, ?) WHERE status = 'running'`, restartMessage, string(outputJSON)); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET status = 'fail', error_message = ?, output_json = COALESCE(output_json, ?) WHERE status = 'initializing'`, restartMessage, string(outputJSON)); err != nil {
		return err
	}
	return nil
}

var persistedReflectionSuggestionTypes = map[string]bool{
	"danger_reflection": true,
	"plan_reflection":   true,
	"goal_reflection":   true,
}

func abortActiveReflectionSuggestionsRaw(raw sql.NullString, reason, resolvedAt string) (string, bool) {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return "", false
	}
	var suggestions []any
	if err := json.Unmarshal([]byte(raw.String), &suggestions); err != nil {
		return "", false
	}
	changed := false
	for index, suggestion := range suggestions {
		record, ok := suggestion.(map[string]any)
		if !ok {
			continue
		}
		typeValue := stringValue(record["type"])
		if !persistedReflectionSuggestionTypes[typeValue] {
			continue
		}
		status := stringValue(record["status"])
		if status == "" {
			status = "running"
		}
		if status != "running" && status != "awaiting_user" {
			continue
		}
		record["status"] = "aborted"
		record["reason"] = reason
		record["resolvedAt"] = resolvedAt
		suggestions[index] = record
		changed = true
	}
	if !changed {
		return "", false
	}
	data, err := json.Marshal(suggestions)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func externalToolContextFromNarrator(n Narrator) ExternalToolContext {
	subagentType := ptrString(n.SubagentType)
	access := customSubagentToolAccessForType(subagentType)
	return ExternalToolContext{
		NarratorID:       n.ID,
		ParentNarratorID: ptrString(n.ParentNarratorID),
		PermissionMode:   ptrString(n.PermissionMode),
		SubagentType:     subagentType,
		Type:             n.Type,
		Variant:          n.Variant,
		ToolAccess:       access.ToolAccess,
		CustomTools:      access.CustomTools,
	}
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Narrator, error) {
	now := db.NowISO()
	narratorID := id.New()
	model := input.Model
	if model == "" {
		model = s.defaultNarratorModel()
	}
	permission := normalizePermissionMode(input.PermissionMode, s.defaultNarratorPermissionMode())
	startInPlanMode := boolPtrValue(input.StartInPlanMode, false)
	fastMode := boolPtrValue(input.FastMode, false)
	relaxedPlan := boolPtrValue(input.RelaxedPlan, false)
	planReflectionOverride := normalizeBooleanOverride(input.PlanReflectionAutoApproveOverride)
	dangerReflectionOverride := normalizeBooleanOverride(input.DangerReflectionOverride)
	traits := []string{}
	if input.ChapterID == nil {
		traits = append(traits, "standalone")
	}
	if startInPlanMode {
		traits = append(traits, "plan")
	}
	traitsJSON, _ := json.Marshal(traits)
	var previousPermissionMode any
	var planFileID any
	if startInPlanMode {
		previousPermissionMode = permission
		planFileID = id.Must(8)
	}
	effectivePermission := permission
	if startInPlanMode {
		effectivePermission = "readOnly"
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO narrators (
		id, chapter_id, type, title, inherit_mode, model, system_prompt, permission_mode,
		previous_permission_mode, plan_file_id, reasoning_effort, fast_mode, relaxed_plan,
		plan_reflection_auto_approve_override, danger_reflection_override, status, substatus,
		plan_mode, cwd, prune_enabled, variant, traits, is_background, is_ask_in_passing,
		message_version, created_at, updated_at
	) VALUES (?, ?, 'primary', ?, 'fresh', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'idle', '[]', ?, ?, 1, 'primary', ?, 0, 0, 0, ?, ?)`,
		narratorID, nullablePtr(input.ChapterID), nullablePtr(input.Title), model,
		nullablePtr(input.SystemPrompt), effectivePermission, previousPermissionMode, planFileID,
		nullablePtr(input.ReasoningEffort), boolInt(fastMode), boolInt(relaxedPlan),
		planReflectionOverride, dangerReflectionOverride, boolInt(startInPlanMode),
		nullablePtr(input.Cwd), string(traitsJSON), now, now)
	if err != nil {
		return Narrator{}, err
	}
	return s.Get(ctx, narratorID)
}

func (s *Service) defaultNarratorModel() string {
	if s.Settings != nil {
		if model := strings.TrimSpace(s.Settings.Core().Agent.DefaultModel); model != "" {
			return model
		}
	}
	return config.DefaultAgentModel
}

func (s *Service) defaultNarratorPermissionMode() string {
	if s.Settings != nil {
		return normalizePermissionMode(s.Settings.Core().Agent.DefaultPermissionMode, "default")
	}
	return "default"
}

func (s *Service) ForkNarrator(ctx context.Context, parentID, forkMessageUUID, title, inheritMode string) (Narrator, error) {
	return s.ForkNarratorWithOptions(ctx, parentID, forkMessageUUID, ForkNarratorOptions{Title: title, InheritMode: inheritMode, Standalone: true})
}

func (s *Service) ForkNarratorWithOptions(ctx context.Context, parentID, forkMessageUUID string, opts ForkNarratorOptions) (Narrator, error) {
	parent, err := s.Get(ctx, parentID)
	if err != nil {
		return Narrator{}, err
	}
	inheritMode := strings.TrimSpace(opts.InheritMode)
	if inheritMode == "" {
		inheritMode = "full"
	}
	if inheritMode != "full" && inheritMode != "fresh" && inheritMode != "compressed" {
		return Narrator{}, httpx.BadRequest("invalid inheritMode")
	}
	if parent.ChapterID != nil && opts.NewChapterID == nil && !opts.Standalone {
		return Narrator{}, httpx.BadRequest("chapter-bound narrators can only be forked together with a chapter")
	}
	newTitle := strings.TrimSpace(opts.Title)
	if newTitle == "" {
		base := "Forked narrator"
		if parent.Title != nil && strings.TrimSpace(*parent.Title) != "" {
			base = strings.TrimSpace(*parent.Title)
		}
		newTitle = base + " (fork)"
	}
	model := ""
	if parent.Model != nil {
		model = *parent.Model
	}
	permission := ""
	if parent.PermissionMode != nil {
		permission = *parent.PermissionMode
	}
	forkMessageID := ""
	refs := []messageRef{}
	if inheritMode != "fresh" {
		if strings.TrimSpace(forkMessageUUID) != "" {
			ref, err := s.forkAnchorRef(ctx, parentID, forkMessageUUID)
			if err != nil {
				return Narrator{}, err
			}
			forkMessageID = ref.MessageID
			refs, err = s.refsUpToSeq(ctx, parentID, ref.Seq, 500)
			if err != nil {
				return Narrator{}, err
			}
		} else if inheritMode == "full" || inheritMode == "compressed" {
			refs, err = s.latestRefs(ctx, parentID, 500)
			if err != nil {
				return Narrator{}, err
			}
			if len(refs) > 0 {
				forkMessageID = refs[len(refs)-1].MessageID
			}
		}
	}
	cwd := opts.Cwd
	if cwd == nil {
		cwd = parent.Cwd
	}
	child, err := s.Create(ctx, CreateInput{ChapterID: opts.NewChapterID, Title: &newTitle, Model: model, PermissionMode: permission, Cwd: cwd, SystemPrompt: parent.SystemPrompt, ReasoningEffort: parent.ReasoningEffort, FastMode: &parent.FastMode, RelaxedPlan: &parent.RelaxedPlan, PlanReflectionAutoApproveOverride: parent.PlanReflectionAutoApproveOverride, DangerReflectionOverride: parent.DangerReflectionOverride})
	if err != nil {
		return Narrator{}, err
	}
	if err := s.copyRefsToNarrator(ctx, child.ID, refs); err != nil {
		return Narrator{}, err
	}
	contextSummary := parent.ContextSummary
	systemPrompt := parent.SystemPrompt
	if inheritMode == "compressed" {
		fallbackSummary := fmt.Sprintf("Go backend compressed fork fallback: copied %d parent message reference(s) instead of generating an AI context summary.", len(refs))
		contextSummary = &fallbackSummary
		if systemPrompt != nil && strings.TrimSpace(*systemPrompt) != "" {
			combined := *systemPrompt + "\n\n## Previous Context Summary\n\n" + fallbackSummary
			systemPrompt = &combined
		}
	}
	now := db.NowISO()
	_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET parent_narrator_id = ?, fork_message_id = ?, inherit_mode = ?, context_summary = ?, system_prompt = ?, message_count = ?, last_message_at = (SELECT MAX(m.created_at) FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ?), updated_at = ? WHERE id = ?`, parentID, nullableString(forkMessageID), inheritMode, nullablePtr(contextSummary), nullablePtr(systemPrompt), len(refs), child.ID, now, child.ID)
	if err != nil {
		return Narrator{}, err
	}
	return s.Get(ctx, child.ID)
}

func (s *Service) ForkFromMessages(ctx context.Context, parentID string, messageIDs []string, title string) (Narrator, error) {
	parent, err := s.Get(ctx, parentID)
	if err != nil {
		return Narrator{}, err
	}
	if len(messageIDs) == 0 {
		return Narrator{}, httpx.BadRequest("messageIds are required")
	}
	newTitle := strings.TrimSpace(title)
	if newTitle == "" {
		base := "Selected messages"
		if parent.Title != nil && strings.TrimSpace(*parent.Title) != "" {
			base = strings.TrimSpace(*parent.Title)
		}
		newTitle = base + " (selection)"
	}
	model := ""
	if parent.Model != nil {
		model = *parent.Model
	}
	permission := ""
	if parent.PermissionMode != nil {
		permission = *parent.PermissionMode
	}
	placeholders, idArgs := inPlaceholders(messageIDs)
	args := append([]any{parentID}, idArgs...)
	rows, err := s.DB.QueryContext(ctx, `SELECT message_id, seq FROM narrator_message_refs WHERE narrator_id = ? AND message_id IN (`+placeholders+`) ORDER BY seq ASC`, args...)
	if err != nil {
		return Narrator{}, err
	}
	defer rows.Close()
	refs := []messageRef{}
	for rows.Next() {
		var ref messageRef
		if err := rows.Scan(&ref.MessageID, &ref.Seq); err != nil {
			return Narrator{}, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return Narrator{}, err
	}
	if len(refs) == 0 {
		return Narrator{}, httpx.BadRequest("None of the specified messages belong to this narrator")
	}
	child, err := s.Create(ctx, CreateInput{Title: &newTitle, Model: model, PermissionMode: permission, Cwd: parent.Cwd})
	if err != nil {
		return Narrator{}, err
	}
	if err := s.copyRefsToNarrator(ctx, child.ID, refs); err != nil {
		return Narrator{}, err
	}
	now := db.NowISO()
	_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET parent_narrator_id = ?, inherit_mode = 'full', message_count = ?, last_message_at = (SELECT MAX(m.created_at) FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ?), updated_at = ? WHERE id = ?`, parentID, len(refs), child.ID, now, child.ID)
	if err != nil {
		return Narrator{}, err
	}
	return s.Get(ctx, child.ID)
}

func (s *Service) forkAnchorRef(ctx context.Context, narratorID, forkMessageUUID string) (messageRef, error) {
	var ref messageRef
	err := s.DB.QueryRowContext(ctx, `SELECT r.message_id, r.seq FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ? AND (m.sdk_message_uuid = ? OR m.id = ?) ORDER BY r.seq DESC LIMIT 1`, narratorID, forkMessageUUID, forkMessageUUID).Scan(&ref.MessageID, &ref.Seq)
	if errors.Is(err, sql.ErrNoRows) {
		return ref, httpx.BadRequest("Fork message not found")
	}
	return ref, err
}

func (s *Service) refsUpToSeq(ctx context.Context, narratorID string, seq int, limit int) ([]messageRef, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT message_id, seq FROM narrator_message_refs WHERE narrator_id = ? AND seq <= ? ORDER BY seq DESC LIMIT ?`, narratorID, seq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []messageRef{}
	for rows.Next() {
		var ref messageRef
		if err := rows.Scan(&ref.MessageID, &ref.Seq); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	reverseMessageRefs(refs)
	return refs, rows.Err()
}

func (s *Service) latestRefs(ctx context.Context, narratorID string, limit int) ([]messageRef, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT message_id, seq FROM narrator_message_refs WHERE narrator_id = ? ORDER BY seq DESC LIMIT ?`, narratorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []messageRef{}
	for rows.Next() {
		var ref messageRef
		if err := rows.Scan(&ref.MessageID, &ref.Seq); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	reverseMessageRefs(refs)
	return refs, rows.Err()
}

func (s *Service) copyRefsToNarrator(ctx context.Context, narratorID string, refs []messageRef) error {
	if len(refs) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, ref := range refs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES (?, ?, ?, ?, 0)`, id.New(), narratorID, ref.MessageID, i+1); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) List(ctx context.Context, chapterID, status string, standalone bool) ([]Narrator, error) {
	query := `SELECT ` + narratorSelectColumns + ` FROM narrators WHERE 1=1`
	args := []any{}
	if chapterID != "" {
		query += ` AND chapter_id = ?`
		args = append(args, chapterID)
	}
	if standalone {
		query += ` AND chapter_id IS NULL`
	}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Narrator
	for rows.Next() {
		n, err := scanNarrator(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

func (s *Service) Get(ctx context.Context, narratorID string) (Narrator, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+narratorSelectColumns+` FROM narrators WHERE id = ?`, narratorID)
	n, err := scanNarrator(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Narrator{}, httpx.NotFound("Narrator")
	}
	return n, err
}

func (s *Service) UpdateTextField(ctx context.Context, narratorID, column string, value any) (Narrator, error) {
	allowed := map[string]bool{
		"title": true, "model": true, "permission_mode": true, "status": true,
		"reasoning_effort": true, "cwd": true,
	}
	if !allowed[column] {
		return Narrator{}, httpx.BadRequest("invalid narrator field")
	}
	if column == "permission_mode" {
		mode, ok := value.(string)
		if !ok {
			return Narrator{}, httpx.BadRequest("permission_mode must be a string")
		}
		normalized, valid := normalizePermissionModeStrict(mode)
		if !valid {
			return Narrator{}, httpx.BadRequest("permission_mode must be one of default, acceptEdits, bypassPermissions, readOnly, or dontAsk")
		}
		value = normalized
	}
	_, err := s.DB.ExecContext(ctx, fmt.Sprintf(`UPDATE narrators SET %s = ?, updated_at = ? WHERE id = ?`, column), value, db.NowISO(), narratorID)
	if err != nil {
		return Narrator{}, err
	}
	return s.Get(ctx, narratorID)
}

func (s *Service) SetTemporaryModelRestore(ctx context.Context, narratorID, restoreModel string) error {
	if ok, err := s.columnExists(ctx, "narrators", "pending_model_restore"); err != nil || !ok {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE narrators SET pending_model_restore = ?, updated_at = ? WHERE id = ?`, nullableTrimmedString(restoreModel), db.NowISO(), narratorID)
	return err
}

func (s *Service) restoreTemporaryModel(ctx context.Context, narratorID string) error {
	if ok, err := s.columnExists(ctx, "narrators", "pending_model_restore"); err != nil || !ok {
		return err
	}
	var restore sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT pending_model_restore FROM narrators WHERE id = ?`, narratorID).Scan(&restore); err != nil {
		return err
	}
	if !restore.Valid || strings.TrimSpace(restore.String) == "" {
		return nil
	}
	model := any(restore.String)
	if restore.String == "__default__" {
		model = nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE narrators SET model = ?, pending_model_restore = NULL, updated_at = ? WHERE id = ?`, model, db.NowISO(), narratorID)
	return err
}

func (s *Service) columnExists(ctx context.Context, table, column string) (bool, error) {
	rows, err := s.DB.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Service) SetArchived(ctx context.Context, narratorID string, archived bool) (Narrator, error) {
	status := "idle"
	if archived {
		status = "archived"
	}
	return s.UpdateTextField(ctx, narratorID, "status", status)
}

func (s *Service) Messages(ctx context.Context, narratorID string, limit int) ([]Message, error) {
	page, err := s.MessagesCursor(ctx, narratorID, limit, "", "older")
	if err != nil {
		return nil, err
	}
	return page.Messages, nil
}

func (s *Service) MessagesCursor(ctx context.Context, narratorID string, limit int, cursor, direction string) (MessagePage, error) {
	limit = normalizeMessageLimit(limit, 50, 200)
	if direction != "newer" {
		direction = "older"
	}
	isSubagent, err := s.isSubagentNarrator(ctx, narratorID)
	if err != nil {
		return MessagePage{}, err
	}
	conditions := []string{"r.narrator_id = ?", "r.segment_compact_id IS NULL"}
	args := []any{narratorID}
	if !isSubagent {
		conditions = append(conditions, "m.parent_tool_use_id IS NULL")
	}
	if cursorSeq, ok := parseSeqCursor(cursor); ok {
		if direction == "newer" {
			conditions = append(conditions, "r.seq > ?")
		} else {
			conditions = append(conditions, "r.seq < ?")
		}
		args = append(args, cursorSeq)
	}
	order := "r.seq DESC"
	if direction == "newer" {
		order = "r.seq ASC"
	}
	args = append(args, limit+1)
	refs, err := s.queryMessageRefs(ctx, strings.Join(conditions, " AND "), order, args...)
	if err != nil {
		return MessagePage{}, err
	}
	hasMoreInDirection := len(refs) > limit
	if hasMoreInDirection {
		refs = refs[:limit]
	}
	if direction == "older" {
		reverseMessageRefs(refs)
	}
	messages, err := s.loadMessagesForRefs(ctx, narratorID, refs)
	if err != nil {
		return MessagePage{}, err
	}
	page := MessagePage{Messages: messages}
	if hasMoreInDirection && len(refs) > 0 {
		if direction == "older" {
			page.HasMore = true
			page.NextCursor = stringPtr(strconv.Itoa(refs[0].Seq))
		} else {
			page.HasMoreAfter = true
			page.PrevCursor = stringPtr(strconv.Itoa(refs[len(refs)-1].Seq))
		}
	}
	return page, nil
}

func (s *Service) MessagesAround(ctx context.Context, narratorID, messageID string, before, after int) (MessagePage, error) {
	before = normalizeWindowSize(before, 5)
	after = normalizeWindowSize(after, 20)
	fallbackLimit := before + after + 1
	if fallbackLimit < 10 {
		fallbackLimit = 10
	}
	isSubagent, err := s.isSubagentNarrator(ctx, narratorID)
	if err != nil {
		return MessagePage{}, err
	}
	anchorMessageID := messageID
	if !isSubagent {
		if parentToolUseID, ok, err := s.messageParentToolUseID(ctx, messageID); err != nil {
			return MessagePage{}, err
		} else if ok && parentToolUseID != "" {
			if parentMessageID, ok, err := s.parentMessageForToolUse(ctx, narratorID, parentToolUseID); err != nil {
				return MessagePage{}, err
			} else if ok {
				anchorMessageID = parentMessageID
			}
		}
	}
	anchor, err := s.anchorRef(ctx, narratorID, anchorMessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return s.MessagesCursor(ctx, narratorID, fallbackLimit, "", "older")
	}
	if err != nil {
		return MessagePage{}, err
	}
	baseConditions := []string{"r.narrator_id = ?", "r.segment_compact_id IS NULL"}
	if !isSubagent {
		baseConditions = append(baseConditions, "m.parent_tool_use_id IS NULL")
	}
	olderConditions := append(append([]string{}, baseConditions...), "r.seq < ?")
	olderArgs := []any{narratorID, anchor.Seq, before + 1}
	olderRefs, err := s.queryMessageRefs(ctx, strings.Join(olderConditions, " AND "), "r.seq DESC", olderArgs...)
	if err != nil {
		return MessagePage{}, err
	}
	hasMore := len(olderRefs) > before
	if hasMore {
		olderRefs = olderRefs[:before]
	}
	reverseMessageRefs(olderRefs)
	newerConditions := append(append([]string{}, baseConditions...), "r.seq > ?")
	newerArgs := []any{narratorID, anchor.Seq, after + 1}
	newerRefs, err := s.queryMessageRefs(ctx, strings.Join(newerConditions, " AND "), "r.seq ASC", newerArgs...)
	if err != nil {
		return MessagePage{}, err
	}
	hasMoreAfter := len(newerRefs) > after
	if hasMoreAfter {
		newerRefs = newerRefs[:after]
	}
	refs := append(append(append([]messageRef{}, olderRefs...), anchor), newerRefs...)
	messages, err := s.loadMessagesForRefs(ctx, narratorID, refs)
	if err != nil {
		return MessagePage{}, err
	}
	page := MessagePage{Messages: messages, HasMore: hasMore, HasMoreAfter: hasMoreAfter}
	if hasMore {
		cursorSeq := anchor.Seq
		if len(olderRefs) > 0 {
			cursorSeq = olderRefs[0].Seq
		}
		page.NextCursor = stringPtr(strconv.Itoa(cursorSeq))
	}
	if hasMoreAfter {
		cursorSeq := anchor.Seq
		if len(newerRefs) > 0 {
			cursorSeq = newerRefs[len(newerRefs)-1].Seq
		}
		page.PrevCursor = stringPtr(strconv.Itoa(cursorSeq))
	}
	return page, nil
}

func (s *Service) NarratorMessageVersion(ctx context.Context, narratorID string) (int, error) {
	var version int
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(message_version, 0) FROM narrators WHERE id = ?`, narratorID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, httpx.NotFound("Narrator")
	}
	return version, err
}

func (s *Service) NarratorMessagesAfter(ctx context.Context, narratorID string, anchor any, limit int) (map[string]any, error) {
	cursor, err := catchUpCursorFromAnchor(anchor)
	if err != nil {
		return nil, err
	}
	page, err := s.MessagesAfter(ctx, narratorID, cursor, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"topLevel":       page.TopLevel,
		"orphanChildren": page.OrphanChildren,
		"hitLimit":       page.HitLimit,
		"cursor":         page.Cursor,
		"messages":       page.TopLevel,
	}, nil
}

func catchUpCursorFromAnchor(anchor any) (CatchUpCursor, error) {
	switch typed := anchor.(type) {
	case nil:
		return CatchUpCursor{ChildAnchors: []CatchUpChildAnchor{}}, nil
	case CatchUpCursor:
		if typed.ChildAnchors == nil {
			typed.ChildAnchors = []CatchUpChildAnchor{}
		}
		return typed, nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return CatchUpCursor{ChildAnchors: []CatchUpChildAnchor{}}, nil
		}
		if strings.HasPrefix(trimmed, "{") {
			var cursor CatchUpCursor
			if err := json.Unmarshal([]byte(trimmed), &cursor); err != nil {
				return CatchUpCursor{}, httpx.BadRequest("invalid catchUpCursor")
			}
			if cursor.ChildAnchors == nil {
				cursor.ChildAnchors = []CatchUpChildAnchor{}
			}
			return cursor, nil
		}
		return CatchUpCursor{ParentLastMessageID: stringPtr(trimmed), ChildAnchors: []CatchUpChildAnchor{}}, nil
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return CatchUpCursor{}, httpx.BadRequest("invalid catchUpCursor")
		}
		var cursor CatchUpCursor
		if err := json.Unmarshal(data, &cursor); err != nil {
			return CatchUpCursor{}, httpx.BadRequest("invalid catchUpCursor")
		}
		if cursor.ChildAnchors == nil {
			cursor.ChildAnchors = []CatchUpChildAnchor{}
		}
		return cursor, nil
	}
}

func (s *Service) MessagesAfter(ctx context.Context, narratorID string, after CatchUpCursor, limit int) (CatchUpPage, error) {
	limit = normalizeMessageLimit(limit, 40, 200)
	isSubagent, err := s.isSubagentNarrator(ctx, narratorID)
	if err != nil {
		return CatchUpPage{}, err
	}
	var parentAnchorSeq *int
	parentLastMessageID := after.ParentLastMessageID
	baseChildAnchors := map[string]CatchUpChildAnchor{}
	if parentLastMessageID != nil && strings.TrimSpace(*parentLastMessageID) != "" {
		messageID := strings.TrimSpace(*parentLastMessageID)
		msgNarratorID, parentToolUseID, found, err := s.messageCatchUpInfo(ctx, messageID)
		if err != nil {
			return CatchUpPage{}, err
		}
		if found && !isSubagent && parentToolUseID != "" {
			parentMessageID, parentSeq, ok, err := s.parentMessageRefForToolUse(ctx, narratorID, parentToolUseID)
			if err != nil {
				return CatchUpPage{}, err
			}
			if ok {
				parentAnchorSeq = &parentSeq
				parentLastMessageID = stringPtr(parentMessageID)
			}
			if childSeq, ok, err := s.refSeq(ctx, msgNarratorID, messageID); err != nil {
				return CatchUpPage{}, err
			} else {
				anchor := CatchUpChildAnchor{ParentToolUseID: parentToolUseID, NarratorID: stringPtr(msgNarratorID)}
				if ok || childSeq >= 0 {
					anchor.LastMessageID = stringPtr(messageID)
				}
				upsertCatchUpAnchor(baseChildAnchors, anchor)
			}
		} else if seq, ok, err := s.refSeq(ctx, narratorID, messageID); err != nil {
			return CatchUpPage{}, err
		} else if ok {
			parentAnchorSeq = &seq
			toolUseIDs, err := s.toolUseIDsForMessage(ctx, messageID)
			if err != nil {
				return CatchUpPage{}, err
			}
			for _, toolUseID := range toolUseIDs {
				upsertCatchUpAnchor(baseChildAnchors, CatchUpChildAnchor{ParentToolUseID: toolUseID})
			}
		}
	}
	for _, anchor := range after.ChildAnchors {
		if strings.TrimSpace(anchor.ParentToolUseID) != "" {
			upsertCatchUpAnchor(baseChildAnchors, anchor)
		}
	}
	resolvedChildAnchors := map[string]resolvedChildAnchor{}
	if !isSubagent {
		resolvedChildAnchors, err = s.resolveCatchUpChildAnchors(ctx, trimCatchUpAnchors(baseChildAnchors))
		if err != nil {
			return CatchUpPage{}, err
		}
	}
	if parentAnchorSeq == nil && len(resolvedChildAnchors) == 0 {
		return CatchUpPage{TopLevel: []Message{}, OrphanChildren: []Message{}, HitLimit: true}, nil
	}
	parentCount := 0
	if parentAnchorSeq != nil {
		conditions := []string{"r.narrator_id = ?", "r.seq > ?", "r.segment_compact_id IS NULL"}
		args := []any{narratorID, *parentAnchorSeq}
		if !isSubagent {
			conditions = append(conditions, "m.parent_tool_use_id IS NULL")
		}
		parentCount, err = s.countMessageRefs(ctx, strings.Join(conditions, " AND "), args...)
		if err != nil {
			return CatchUpPage{}, err
		}
	}
	childWhere, childArgs := childCatchUpWhere(resolvedChildAnchors)
	childCount := 0
	if childWhere != "" {
		childCount, err = s.countMessageRefs(ctx, childWhere, childArgs...)
		if err != nil {
			return CatchUpPage{}, err
		}
	}
	if parentCount+childCount > limit {
		return CatchUpPage{TopLevel: []Message{}, OrphanChildren: []Message{}, HitLimit: true}, nil
	}
	topRefs := []messageRef{}
	if parentAnchorSeq != nil {
		conditions := []string{"r.narrator_id = ?", "r.seq > ?", "r.segment_compact_id IS NULL"}
		args := []any{narratorID, *parentAnchorSeq, 10000}
		if !isSubagent {
			conditions = append(conditions, "m.parent_tool_use_id IS NULL")
		}
		topRefs, err = s.queryMessageRefs(ctx, strings.Join(conditions, " AND "), "r.seq ASC", args...)
		if err != nil {
			return CatchUpPage{}, err
		}
	}
	childRefs := []messageRef{}
	if childWhere != "" {
		args := append(childArgs, 10000)
		childRefs, err = s.queryMessageRefs(ctx, childWhere, "r.seq ASC", args...)
		if err != nil {
			return CatchUpPage{}, err
		}
	}
	if len(topRefs) == 0 && len(childRefs) == 0 {
		cursor := buildCatchUpCursor(parentLastMessageID, trimCatchUpAnchors(baseChildAnchors), nil, nil)
		return CatchUpPage{TopLevel: []Message{}, OrphanChildren: []Message{}, HitLimit: false, Cursor: &cursor}, nil
	}
	allMessages, err := s.loadMessagesByRefs(ctx, append(append([]messageRef{}, topRefs...), childRefs...))
	if err != nil {
		return CatchUpPage{}, err
	}
	topMsgs := []Message{}
	childMsgs := []Message{}
	for _, msg := range allMessages {
		if isSubagent {
			msg.ParentToolUseID = nil
			topMsgs = append(topMsgs, msg)
		} else if msg.ParentToolUseID != nil {
			childMsgs = append(childMsgs, msg)
		} else {
			topMsgs = append(topMsgs, msg)
		}
	}
	newTopToolUseIDs := collectMessageToolUseIDs(topMsgs)
	if len(newTopToolUseIDs) > 0 {
		extraChildren, err := s.loadChildMessagesForToolUses(ctx, newTopToolUseIDs)
		if err != nil {
			return CatchUpPage{}, err
		}
		existing := map[string]bool{}
		for _, child := range childMsgs {
			existing[child.ID] = true
		}
		for _, child := range extraChildren {
			if !existing[child.ID] {
				childMsgs = append(childMsgs, child)
			}
		}
		childMsgs, err = s.attachToolCalls(ctx, childMsgs)
		if err != nil {
			return CatchUpPage{}, err
		}
	}
	newTopToolUseSet := map[string]bool{}
	for _, toolUseID := range newTopToolUseIDs {
		newTopToolUseSet[toolUseID] = true
	}
	orphanChildren := []Message{}
	for _, child := range childMsgs {
		if child.ParentToolUseID != nil && !newTopToolUseSet[*child.ParentToolUseID] {
			child.Children = []Message{}
			orphanChildren = append(orphanChildren, child)
		}
	}
	tree := buildMessageTree(append(append([]Message{}, topMsgs...), childMsgs...))
	lastTopMessageID := parentLastMessageID
	if len(topMsgs) > 0 {
		lastTopMessageID = stringPtr(topMsgs[len(topMsgs)-1].ID)
	}
	cursor := buildCatchUpCursor(lastTopMessageID, trimCatchUpAnchors(baseChildAnchors), topMsgs, childMsgs)
	return CatchUpPage{TopLevel: tree, OrphanChildren: orphanChildren, HitLimit: false, Cursor: &cursor}, nil
}

func normalizeMessageLimit(limit, fallback, max int) int {
	if limit <= 0 {
		limit = fallback
	}
	if limit > max {
		limit = max
	}
	return limit
}

func normalizeWindowSize(value, fallback int) int {
	if value < 0 {
		return fallback
	}
	if value > 100 {
		return 100
	}
	return value
}

func upsertCatchUpAnchor(anchors map[string]CatchUpChildAnchor, anchor CatchUpChildAnchor) {
	anchor.ParentToolUseID = strings.TrimSpace(anchor.ParentToolUseID)
	if anchor.ParentToolUseID == "" {
		return
	}
	existing := anchors[anchor.ParentToolUseID]
	if anchor.NarratorID == nil {
		anchor.NarratorID = existing.NarratorID
	}
	if anchor.LastMessageID == nil {
		anchor.LastMessageID = existing.LastMessageID
	}
	delete(anchors, anchor.ParentToolUseID)
	anchors[anchor.ParentToolUseID] = anchor
}

func trimCatchUpAnchors(anchors map[string]CatchUpChildAnchor) []CatchUpChildAnchor {
	items := make([]CatchUpChildAnchor, 0, len(anchors))
	for _, anchor := range anchors {
		items = append(items, anchor)
	}
	if len(items) <= maxCatchUpChildAnchors {
		return items
	}
	return items[len(items)-maxCatchUpChildAnchors:]
}

func buildCatchUpCursor(parentLastMessageID *string, baseAnchors []CatchUpChildAnchor, topMessages []Message, childMessages []Message) CatchUpCursor {
	anchors := map[string]CatchUpChildAnchor{}
	for _, anchor := range baseAnchors {
		upsertCatchUpAnchor(anchors, anchor)
	}
	for _, msg := range topMessages {
		for _, call := range msg.ToolCalls {
			if call.ToolUseID != "" {
				upsertCatchUpAnchor(anchors, CatchUpChildAnchor{ParentToolUseID: call.ToolUseID})
			}
		}
	}
	for _, child := range childMessages {
		if child.ParentToolUseID == nil || child.ID == "" {
			continue
		}
		upsertCatchUpAnchor(anchors, CatchUpChildAnchor{ParentToolUseID: *child.ParentToolUseID, NarratorID: stringPtr(child.NarratorID), LastMessageID: stringPtr(child.ID)})
	}
	return CatchUpCursor{ParentLastMessageID: parentLastMessageID, ChildAnchors: trimCatchUpAnchors(anchors)}
}

func childCatchUpWhere(anchors map[string]resolvedChildAnchor) (string, []any) {
	if len(anchors) == 0 {
		return "", nil
	}
	parts := []string{}
	args := []any{}
	for _, anchor := range anchors {
		part := "(r.seq > ? AND r.segment_compact_id IS NULL AND m.parent_tool_use_id = ?"
		args = append(args, anchor.Seq, anchor.ParentToolUseID)
		if strings.TrimSpace(anchor.NarratorID) != "" {
			part += " AND r.narrator_id = ?"
			args = append(args, anchor.NarratorID)
		}
		part += ")"
		parts = append(parts, part)
	}
	return strings.Join(parts, " OR "), args
}

func (s *Service) countMessageRefs(ctx context.Context, where string, args ...any) (int, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE `+where, args...).Scan(&count)
	return count, err
}

func (s *Service) messageParentToolUseID(ctx context.Context, messageID string) (string, bool, error) {
	var parent sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT parent_tool_use_id FROM narrator_messages WHERE id = ?`, messageID).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return sqlNullString(parent), true, nil
}

func (s *Service) messageCatchUpInfo(ctx context.Context, messageID string) (string, string, bool, error) {
	var narratorID string
	var parent sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT narrator_id, parent_tool_use_id FROM narrator_messages WHERE id = ?`, messageID).Scan(&narratorID, &parent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return narratorID, sqlNullString(parent), true, nil
}

func (s *Service) parentMessageForToolUse(ctx context.Context, narratorID, toolUseID string) (string, bool, error) {
	messageID, _, ok, err := s.parentMessageRefForToolUse(ctx, narratorID, toolUseID)
	return messageID, ok, err
}

func (s *Service) parentMessageRefForToolUse(ctx context.Context, narratorID, toolUseID string) (string, int, bool, error) {
	var messageID string
	var seq int
	err := s.DB.QueryRowContext(ctx, `SELECT r.message_id, r.seq FROM narrator_tool_calls tc JOIN narrator_message_refs r ON r.message_id = tc.message_id WHERE r.narrator_id = ? AND tc.tool_use_id = ? ORDER BY r.seq ASC LIMIT 1`, narratorID, toolUseID).Scan(&messageID, &seq)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, nil
	}
	return messageID, seq, true, err
}

func (s *Service) refSeq(ctx context.Context, narratorID, messageID string) (int, bool, error) {
	var seq int
	err := s.DB.QueryRowContext(ctx, `SELECT seq FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ? LIMIT 1`, narratorID, messageID).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return -1, false, nil
	}
	return seq, true, err
}

func (s *Service) toolUseIDsForMessage(ctx context.Context, messageID string) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT tool_use_id FROM narrator_tool_calls WHERE message_id = ? ORDER BY created_at ASC`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var toolUseID string
		if err := rows.Scan(&toolUseID); err != nil {
			return nil, err
		}
		if strings.TrimSpace(toolUseID) != "" {
			ids = append(ids, toolUseID)
		}
	}
	return ids, rows.Err()
}

func (s *Service) resolveCatchUpChildAnchors(ctx context.Context, anchors []CatchUpChildAnchor) (map[string]resolvedChildAnchor, error) {
	resolved := map[string]resolvedChildAnchor{}
	for _, anchor := range anchors {
		parentToolUseID := strings.TrimSpace(anchor.ParentToolUseID)
		if parentToolUseID == "" {
			continue
		}
		narratorID := ""
		if anchor.NarratorID != nil {
			narratorID = strings.TrimSpace(*anchor.NarratorID)
		}
		lastMessageID := ""
		seq := -1
		if anchor.LastMessageID != nil && strings.TrimSpace(*anchor.LastMessageID) != "" {
			lastMessageID = strings.TrimSpace(*anchor.LastMessageID)
			childNarratorID, childParentToolUseID, found, err := s.messageCatchUpInfo(ctx, lastMessageID)
			if err != nil {
				return nil, err
			}
			if !found || childParentToolUseID != parentToolUseID {
				continue
			}
			if narratorID == "" {
				narratorID = childNarratorID
			}
			childSeq, ok, err := s.refSeq(ctx, narratorID, lastMessageID)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			seq = childSeq
		}
		existing, ok := resolved[parentToolUseID]
		if !ok || seq >= existing.Seq {
			resolved[parentToolUseID] = resolvedChildAnchor{ParentToolUseID: parentToolUseID, NarratorID: narratorID, LastMessageID: lastMessageID, Seq: seq}
		}
	}
	return resolved, nil
}

func (s *Service) isSubagentNarrator(ctx context.Context, narratorID string) (bool, error) {
	var variant, typ sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT variant, type FROM narrators WHERE id = ?`, narratorID).Scan(&variant, &typ); err != nil {
		return false, err
	}
	return (variant.Valid && strings.Contains(variant.String, "subagent")) || (typ.Valid && typ.String == "subagent"), nil
}

func parseSeqCursor(cursor string) (int, bool) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, false
	}
	seq, err := strconv.Atoi(cursor)
	if err != nil {
		return 0, false
	}
	return seq, true
}

func (s *Service) queryMessageRefs(ctx context.Context, where, order string, args ...any) ([]messageRef, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT r.narrator_id, r.message_id, r.seq FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE `+where+` ORDER BY `+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []messageRef{}
	for rows.Next() {
		var ref messageRef
		if err := rows.Scan(&ref.NarratorID, &ref.MessageID, &ref.Seq); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (s *Service) anchorRef(ctx context.Context, narratorID, messageID string) (messageRef, error) {
	var ref messageRef
	err := s.DB.QueryRowContext(ctx, `SELECT narrator_id, message_id, seq FROM narrator_message_refs WHERE narrator_id = ? AND message_id = ?`, narratorID, messageID).Scan(&ref.NarratorID, &ref.MessageID, &ref.Seq)
	return ref, err
}

func (s *Service) loadMessagesForRefs(ctx context.Context, narratorID string, refs []messageRef) ([]Message, error) {
	messages, err := s.loadMessagesByRefs(ctx, refs)
	if err != nil {
		return nil, err
	}
	return s.attachChildMessages(ctx, messages)
}

func (s *Service) loadMessagesByRefs(ctx context.Context, refs []messageRef) ([]Message, error) {
	if len(refs) == 0 {
		return []Message{}, nil
	}
	ids := make([]string, 0, len(refs))
	seqByID := map[string]int{}
	for _, ref := range refs {
		ids = append(ids, ref.MessageID)
		seqByID[ref.MessageID] = ref.Seq
	}
	placeholders, idArgs := inPlaceholders(ids)
	rows, err := s.DB.QueryContext(ctx, `SELECT id, narrator_id, sdk_message_uuid, parent_tool_use_id, role, content_json, content_text, model, commit_sha, command_text, created_by, created_at, 0 AS seq FROM narrator_messages WHERE id IN (`+placeholders+`)`, idArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]Message{}
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		if seq, ok := seqByID[msg.ID]; ok {
			msg.Seq = seq
		}
		byID[msg.ID] = msg
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	messages := make([]Message, 0, len(refs))
	for _, ref := range refs {
		if msg, ok := byID[ref.MessageID]; ok {
			messages = append(messages, msg)
		}
	}
	return s.attachToolCalls(ctx, messages)
}

func (s *Service) attachChildMessages(ctx context.Context, messages []Message) ([]Message, error) {
	toolUseIDs := collectMessageToolUseIDs(messages)
	if len(toolUseIDs) == 0 {
		return messages, nil
	}
	children, err := s.loadChildMessagesForToolUses(ctx, toolUseIDs)
	if err != nil {
		return nil, err
	}
	if len(children) == 0 {
		return messages, nil
	}
	children, err = s.attachToolCalls(ctx, children)
	if err != nil {
		return nil, err
	}
	return buildMessageTree(append(messages, children...)), nil
}

func collectMessageToolUseIDs(messages []Message) []string {
	ids := []string{}
	seen := map[string]bool{}
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			if call.ToolUseID != "" && !seen[call.ToolUseID] {
				seen[call.ToolUseID] = true
				ids = append(ids, call.ToolUseID)
			}
		}
	}
	return ids
}

func (s *Service) loadChildMessagesForToolUses(ctx context.Context, toolUseIDs []string) ([]Message, error) {
	placeholders, args := inPlaceholders(toolUseIDs)
	rows, err := s.DB.QueryContext(ctx, `SELECT id, narrator_id, sdk_message_uuid, parent_tool_use_id, role, content_json, content_text, model, commit_sha, command_text, created_by, created_at, 0 AS seq FROM narrator_messages WHERE parent_tool_use_id IN (`+placeholders+`) ORDER BY created_at ASC LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []Message{}
	for rows.Next() {
		msg, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

func buildMessageTree(flat []Message) []Message {
	toolUseIDToIndex := map[string]int{}
	messages := make([]Message, len(flat))
	for i, msg := range flat {
		msg.Children = []Message{}
		messages[i] = msg
		for _, call := range msg.ToolCalls {
			if call.ToolUseID != "" {
				toolUseIDToIndex[call.ToolUseID] = i
			}
		}
	}
	attached := make([]bool, len(messages))
	for i := range messages {
		msg := messages[i]
		if msg.ParentToolUseID == nil {
			continue
		}
		if parentIndex, ok := toolUseIDToIndex[*msg.ParentToolUseID]; ok {
			messages[parentIndex].Children = append(messages[parentIndex].Children, msg)
			attached[i] = true
		}
	}
	topLevel := []Message{}
	for i, msg := range messages {
		if !attached[i] && msg.ParentToolUseID == nil {
			topLevel = append(topLevel, msg)
		}
	}
	return topLevel
}

func reverseMessageRefs(refs []messageRef) {
	for i, j := 0, len(refs)-1; i < j; i, j = i+1, j-1 {
		refs[i], refs[j] = refs[j], refs[i]
	}
}

func stringPtr(value string) *string { return &value }

func (s *Service) LastUserText(ctx context.Context, narratorID string) (string, error) {
	ref, err := s.lastUserRef(ctx, narratorID)
	if err != nil {
		return "", err
	}
	messages, err := s.loadMessagesForRefs(ctx, narratorID, []messageRef{ref})
	if err != nil {
		return "", err
	}
	if len(messages) == 0 || messages[0].ContentText == nil || strings.TrimSpace(*messages[0].ContentText) == "" {
		return "", httpx.BadRequest("no previous user message to retry")
	}
	return *messages[0].ContentText, nil
}

func (s *Service) RetryLastUserMessage(ctx context.Context, narratorID string) (Message, []string, error) {
	narrator, err := s.Get(ctx, narratorID)
	if err != nil {
		return Message{}, nil, err
	}
	ref, err := s.lastUserRef(ctx, narratorID)
	if err != nil {
		return Message{}, nil, err
	}
	messages, err := s.loadMessagesForRefs(ctx, narratorID, []messageRef{ref})
	if err != nil {
		return Message{}, nil, err
	}
	if len(messages) == 0 || messages[0].ContentText == nil || strings.TrimSpace(*messages[0].ContentText) == "" {
		return Message{}, nil, httpx.BadRequest("no previous user message to retry")
	}
	deleted, err := s.deleteMessagesAfter(ctx, narratorID, ref.MessageID, false, true)
	if err != nil {
		return Message{}, nil, err
	}
	deletedIDs := deleted.DeletedMessageIDs
	if s.Hub != nil && len(deletedIDs) > 0 {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "messages_deleted", "narratorId": narratorID, "deletedMessageIds": deletedIDs})
	}
	if err := s.markRunStarted(ctx, narratorID, narrator); err != nil {
		return Message{}, nil, err
	}
	go s.runBasicAgent(context.Background(), narratorID)
	return messages[0], deletedIDs, nil
}

func (s *Service) lastUserRef(ctx context.Context, narratorID string) (messageRef, error) {
	isSubagent, err := s.isSubagentNarrator(ctx, narratorID)
	if err != nil {
		return messageRef{}, err
	}
	where := `r.narrator_id = ? AND m.role = 'user'`
	if !isSubagent {
		where += ` AND m.parent_tool_use_id IS NULL`
	}
	var ref messageRef
	err = s.DB.QueryRowContext(ctx, `SELECT r.message_id, r.seq FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE `+where+` ORDER BY r.seq DESC LIMIT 1`, narratorID).Scan(&ref.MessageID, &ref.Seq)
	if errors.Is(err, sql.ErrNoRows) {
		return ref, httpx.BadRequest("no previous user message to retry")
	}
	return ref, err
}

func (s *Service) deleteRefsAfterSeq(ctx context.Context, narratorID string, seq int) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT message_id FROM narrator_message_refs WHERE narrator_id = ? AND seq > ? ORDER BY seq ASC`, narratorID, seq)
	if err != nil {
		return nil, err
	}
	deletedIDs := []string{}
	for rows.Next() {
		var messageID string
		if err := rows.Scan(&messageID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		deletedIDs = append(deletedIDs, messageID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM narrator_message_refs WHERE narrator_id = ? AND seq > ?`, narratorID, seq); err != nil {
		return nil, err
	}
	now := db.NowISO()
	_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET message_count = (SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ?), message_version = message_version + 1, last_message_at = (SELECT MAX(m.created_at) FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ?), updated_at = ? WHERE id = ?`, narratorID, narratorID, now, narratorID)
	return deletedIDs, err
}

func (s *Service) AddUserMessage(ctx context.Context, narratorID, userID, text string) (Message, error) {
	content := []map[string]any{{"type": "text", "text": text}}
	return s.AddUserMessageWithContent(ctx, narratorID, userID, text, content)
}

func (s *Service) AddUserMessageWithContent(ctx context.Context, narratorID, userID, contentText string, contentBlocks any) (Message, error) {
	return s.AddUserMessageWithContentAndCommand(ctx, narratorID, userID, contentText, contentBlocks, "")
}

func (s *Service) AddUserMessageWithContentAndCommand(ctx context.Context, narratorID, userID, contentText string, contentBlocks any, commandText string) (Message, error) {
	if strings.TrimSpace(contentText) == "" {
		return Message{}, httpx.BadRequest("message is required")
	}
	narrator, err := s.Get(ctx, narratorID)
	if err != nil {
		return Message{}, err
	}
	return s.startUserMessageRun(ctx, narratorID, userID, contentText, contentBlocks, narrator, commandText)
}

func (s *Service) startUserMessageRun(ctx context.Context, narratorID, userID, contentText string, contentBlocks any, narrator Narrator, commandText ...string) (Message, error) {
	return s.startUserMessageRunWithParentToolUse(ctx, narratorID, userID, contentText, contentBlocks, narrator, "", commandText...)
}

func (s *Service) startUserMessageRunWithParentToolUse(ctx context.Context, narratorID, userID, contentText string, contentBlocks any, narrator Narrator, parentToolUseID string, commandText ...string) (Message, error) {
	cmd := ""
	if len(commandText) > 0 {
		cmd = commandText[0]
	}
	msg, err := s.addMessageWithBlocksWithCommandAndParentToolUse(ctx, narratorID, "user", contentText, contentBlocks, &userID, cmd, parentToolUseID)
	if err != nil {
		return Message{}, err
	}
	s.broadcastMessage(ctx, narratorID, msg)
	if err := s.startNarratorRun(ctx, narratorID, narrator); err != nil {
		return Message{}, err
	}
	return msg, nil
}

func (s *Service) persistUserMessageWithContent(ctx context.Context, narratorID, userID, contentText string, contentBlocks any) (Message, error) {
	msg, err := s.addMessageWithBlocks(ctx, narratorID, "user", contentText, contentBlocks, &userID)
	if err != nil {
		return Message{}, err
	}
	s.broadcastMessage(ctx, narratorID, msg)
	return msg, nil
}

func (s *Service) startNarratorRun(ctx context.Context, narratorID string, narrator Narrator) error {

	if err := s.markRunStarted(ctx, narratorID, narrator); err != nil {
		return err
	}
	go s.runBasicAgent(context.Background(), narratorID)
	return nil
}

const maxBufferedMessages = 50

func (s *Service) BufferUserMessage(ctx context.Context, narratorID, userID, text string, contentBlocks any, priority bool) (BufferedMessage, error) {
	if strings.TrimSpace(text) == "" {
		return BufferedMessage{}, httpx.BadRequest("message is required")
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrator_buffered_messages WHERE narrator_id = ?`, narratorID).Scan(&count); err != nil {
		return BufferedMessage{}, err
	}
	if count >= maxBufferedMessages {
		return BufferedMessage{}, httpx.BadRequest("Message queue is full")
	}
	bufferID := id.Must(8)
	now := db.NowISO()
	images, textFilePaths := bufferedPayloadJSON(contentBlocks)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return BufferedMessage{}, err
	}
	defer tx.Rollback()
	seq := count
	if priority {
		if _, err := tx.ExecContext(ctx, `UPDATE narrator_buffered_messages SET seq = seq + 1 WHERE narrator_id = ?`, narratorID); err != nil {
			return BufferedMessage{}, err
		}
		seq = 0
	} else {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), -1) + 1 FROM narrator_buffered_messages WHERE narrator_id = ?`, narratorID).Scan(&seq); err != nil {
			return BufferedMessage{}, fmt.Errorf("query next buffered message sequence: %w", err)
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO narrator_buffered_messages (id, narrator_id, text, images_json, command_text, created_by, creator_json, text_file_paths_json, priority, seq, buffered_at) VALUES (?, ?, ?, ?, NULL, ?, NULL, ?, ?, ?, ?)`, bufferID, narratorID, text, nullableJSONString(images), nullableTrimmedString(userID), nullableJSONString(textFilePaths), boolInt(priority), seq, now)
	if err != nil {
		return BufferedMessage{}, err
	}
	if err := tx.Commit(); err != nil {
		return BufferedMessage{}, err
	}
	buffered := BufferedMessage{ID: bufferID, NarratorID: narratorID, Text: text, ImagesJSON: images, CreatedBy: &userID, TextFilePathsJSON: textFilePaths, Priority: priority, Seq: seq, BufferedAt: now}
	s.broadcastBufferSet(ctx, narratorID)
	return buffered, nil
}

func narratorAcceptsBufferedMessages(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "working" || status == "waiting"
}

func bufferedPayloadJSON(contentBlocks any) ([]map[string]any, []map[string]any) {
	blocks, ok := contentBlocks.([]map[string]any)
	if !ok {
		return nil, nil
	}
	images := []map[string]any{}
	textFiles := []map[string]any{}
	for _, block := range blocks {
		switch stringFromAny(block["type"]) {
		case "image":
			images = append(images, block)
		case "text_file":
			textFiles = append(textFiles, block)
		}
	}
	return images, textFiles
}

func nullableJSONString(value any) any {
	if value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []map[string]any:
		if len(typed) == 0 {
			return nil
		}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return string(data)
}

func (s *Service) consumeNextBufferedMessage(ctx context.Context, narratorID string) (bool, error) {
	buffered, ok, err := s.nextBufferedMessage(ctx, narratorID)
	if err != nil || !ok {
		return false, err
	}
	narrator, err := s.Get(ctx, narratorID)
	if err != nil {
		return false, err
	}
	userID := "system"
	if buffered.CreatedBy != nil && strings.TrimSpace(*buffered.CreatedBy) != "" {
		userID = *buffered.CreatedBy
	}
	contentBlocks := buffered.ContentBlocks()
	msg, err := s.addMessageWithBlocksWithCommand(ctx, narratorID, "user", buffered.Text, contentBlocks, &userID, ptrString(buffered.CommandText))
	if err != nil {
		s.broadcastBufferSet(ctx, narratorID)
		return false, err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM narrator_buffered_messages WHERE id = ? AND narrator_id = ?`, buffered.ID, narratorID); err != nil {
		s.broadcastBufferSet(ctx, narratorID)
		return false, err
	}
	remaining, _ := s.loadBufferedMessages(ctx, narratorID, 200)
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "buffer_consumed", "narratorId": narratorID, "messageId": buffered.ID, "remaining": bufferSummaries(remaining)})
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "user_message", "narratorId": narratorID, "message": msg})
	}
	if err := s.startNarratorRun(ctx, narratorID, narrator); err != nil {
		s.broadcastBufferSet(ctx, narratorID)
		return false, err
	}
	return true, nil
}

func (s *Service) nextBufferedMessage(ctx context.Context, narratorID string) (BufferedMessage, bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, narrator_id, text, images_json, command_text, created_by, creator_json, text_file_paths_json, priority, seq, buffered_at FROM narrator_buffered_messages WHERE narrator_id = ? ORDER BY seq ASC, buffered_at ASC LIMIT 1`, narratorID)
	if err != nil {
		return BufferedMessage{}, false, err
	}
	defer rows.Close()
	items, err := scanBufferedRows(rows)
	if err != nil || len(items) == 0 {
		return BufferedMessage{}, false, err
	}
	return items[0], true, nil
}

func (s *Service) loadBufferedMessages(ctx context.Context, narratorID string, limit int) ([]BufferedMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, narrator_id, text, images_json, command_text, created_by, creator_json, text_file_paths_json, priority, seq, buffered_at FROM narrator_buffered_messages WHERE narrator_id = ? ORDER BY seq ASC, buffered_at ASC LIMIT ?`, narratorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBufferedRows(rows)
}

func scanBufferedRows(rows *sql.Rows) ([]BufferedMessage, error) {
	items := []BufferedMessage{}
	for rows.Next() {
		var item BufferedMessage
		var imagesRaw, commandText, createdBy, creatorRaw, textFilePathsRaw sql.NullString
		var priority int
		if err := rows.Scan(&item.ID, &item.NarratorID, &item.Text, &imagesRaw, &commandText, &createdBy, &creatorRaw, &textFilePathsRaw, &priority, &item.Seq, &item.BufferedAt); err != nil {
			return nil, err
		}
		item.ImagesJSON = decodeAny(imagesRaw)
		item.CommandText = nullStringPtr(commandText)
		item.CreatedBy = nullStringPtr(createdBy)
		item.CreatorJSON = decodeAny(creatorRaw)
		item.TextFilePathsJSON = decodeAny(textFilePathsRaw)
		item.Priority = priority != 0
		items = append(items, item)
	}
	return items, rows.Err()
}

func (m BufferedMessage) ContentBlocks() []map[string]any {
	blocks := []map[string]any{}
	blocks = appendJSONBlocks(blocks, m.ImagesJSON)
	blocks = appendJSONBlocks(blocks, m.TextFilePathsJSON)
	blocks = append(blocks, map[string]any{"type": "text", "text": m.Text})
	return blocks
}

func appendJSONBlocks(blocks []map[string]any, value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return blocks
	}
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if ok {
			blocks = append(blocks, entry)
		}
	}
	return blocks
}

func (s *Service) broadcastBufferSet(ctx context.Context, narratorID string) {
	if s.Hub == nil {
		return
	}
	items, err := s.loadBufferedMessages(ctx, narratorID, 200)
	if err != nil {
		return
	}
	s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "buffer_set", "narratorId": narratorID, "messages": bufferSummaries(items)})
}

func bufferSummaries(items []BufferedMessage) []map[string]any {
	summaries := []map[string]any{}
	for _, item := range items {
		summary := map[string]any{"id": item.ID, "text": item.Text, "bufferedAt": item.BufferedAt, "imageCount": bufferedImageCount(item.ImagesJSON)}
		if item.CreatorJSON != nil {
			summary["creator"] = item.CreatorJSON
		}
		if item.Priority {
			summary["priority"] = true
		}
		summaries = append(summaries, summary)
	}
	return summaries
}

func bufferedImageCount(value any) int {
	items, ok := value.([]any)
	if !ok {
		return 0
	}
	return len(items)
}

func (s *Service) skillCommandPrompt(ctx context.Context, narratorID, skillName, skillInput string) (string, bool, error) {
	root, err := s.skillRootForNarrator(ctx, narratorID)
	if err != nil {
		return "", false, err
	}
	content := simplecontent.SkillToolOutput(skillName, skillInput, root)
	if strings.HasPrefix(content, "Skill \"") && strings.Contains(content, " not found.") {
		_, _ = s.addMessage(ctx, narratorID, "assistant", "⚠️ "+content, nil)
		return "", false, nil
	}
	return "<command-name>" + strings.TrimSpace(skillName) + "</command-name>\n" + content, true, nil
}

func (s *Service) HandleBashCommand(ctx context.Context, narratorID, command, rawCommand, userID string) (map[string]any, error) {
	narrator, err := s.Get(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	cwd := ""
	if narrator.Cwd != nil {
		cwd = *narrator.Cwd
	}
	toolUseID := "toolu_bash_" + id.Must(8)
	input := map[string]any{"command": command, "description": command}
	userBlocks := []map[string]any{{"type": "bash_command", "command": command}}
	userMsg, err := s.addMessageWithBlocksWithCommand(ctx, narratorID, "user", rawCommand, userBlocks, &userID, rawCommand)
	if err != nil {
		return nil, err
	}
	s.broadcastMessage(ctx, narratorID, userMsg)
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "tool_started", "narratorId": narratorID, "toolUseId": toolUseID, "toolName": "Bash", "input": input})
	}
	argsJSON, _ := json.Marshal(input)
	started := time.Now()
	output := agentbasic.ExecuteTool(ctx, cwd, "Bash", string(argsJSON))
	isError := strings.Contains(output, "[exit error]") || strings.Contains(output, "[timeout]")
	toolUseBlock := map[string]any{"type": "tool_use", "id": toolUseID, "name": "Bash", "input": input, "streamStartedAt": started.UnixMilli()}
	toolResultBlock := map[string]any{"type": "tool_result", "tool_use_id": toolUseID, "content": output, "is_error": isError}
	assistantMsg, err := s.addMessageWithBlocks(ctx, narratorID, "assistant", "", []map[string]any{toolUseBlock, toolResultBlock}, nil)
	if err != nil {
		return nil, err
	}
	run := agentbasic.ToolRun{ID: toolUseID, Name: "Bash", Arguments: string(argsJSON), Output: output, Status: "completed"}
	if isError {
		run.Error = output
	}
	if err := s.persistToolRun(ctx, narratorID, assistantMsg.ID, run); err != nil {
		return nil, err
	}
	s.broadcastMessage(ctx, narratorID, assistantMsg)
	if s.Hub != nil {
		status := "success"
		if isError {
			status = "fail"
		}
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "tool_completed", "narratorId": narratorID, "toolUseId": toolUseID, "toolName": "Bash", "status": status, "output": output, "durationMs": time.Since(started).Milliseconds()})
	}
	return map[string]any{"type": "bash", "id": userMsg.ID, "output": output, "isError": isError}, nil
}

func (s *Service) markRunStarted(ctx context.Context, narratorID string, narrator Narrator) error {

	if s.Hub != nil {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "status", "narratorId": narratorID, "status": "working", "substatus": []string{"calling_model"}, "isBackground": narrator.IsBackground, "backgroundStatus": narrator.BackgroundStatus})
	}

	backgroundStatus := nullableTrimmedString("running")
	if !narrator.IsBackground {
		backgroundStatus = nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'working', substatus = '["calling_model"]', background_status = COALESCE(?, background_status), background_result = NULL, background_completed_at = NULL, error_message = NULL, updated_at = ? WHERE id = ?`, backgroundStatus, db.NowISO(), narratorID)
	return err
}

func nullableStringPtrValue(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	trimmed := strings.TrimSpace(value)
	return &trimmed
}

func (s *Service) executeBuiltInAgentTool(ctx context.Context, cwd, narratorID string, call agentbasic.ToolCall) string {
	if strings.TrimSpace(call.Function.Name) == "Browser" {
		ctx = agentbasic.ContextWithBrowserExecution(ctx, agentbasic.BrowserExecutionContext{NarratorID: narratorID, ToolUseID: call.ID})
	}
	return agentbasic.ExecuteTool(ctx, cwd, call.Function.Name, call.Function.Arguments)
}

func (s *Service) executeExternalTool(ctx context.Context, call agentbasic.ToolCall) (agentbasic.ToolRun, bool) {
	if s.ExternalTools == nil {
		return agentbasic.ToolRun{}, false
	}
	args := map[string]any{}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		args = map[string]any{"_raw": call.Function.Arguments}
	}
	run, handled := s.ExternalTools.ExecuteTool(ctx, call.Function.Name, args)
	if !handled {
		return agentbasic.ToolRun{}, false
	}
	run.ID = call.ID
	run.Name = call.Function.Name
	run.Arguments = call.Function.Arguments
	if strings.TrimSpace(run.Status) == "" {
		run.Status = "completed"
	}
	return run, true
}

func (s *Service) runBasicAgent(ctx context.Context, narratorID string) {
	narrator, err := s.Get(ctx, narratorID)
	if err != nil {
		return
	}
	history, err := s.agentHistory(ctx, narratorID, s.agentHistoryLimit())
	if err != nil {
		s.persistAgentError(ctx, narratorID, err, narrator.IsBackground)
		return
	}
	model := ""
	if narrator.Model != nil {
		model = *narrator.Model
	}
	cwd := ""
	if narrator.Cwd != nil {
		cwd = *narrator.Cwd
	}
	draftMessageID := ""
	pipeline := &agentPipelineState{}
	executor := func(execCtx context.Context, execCwd string, call agentbasic.ToolCall) (agentbasic.ToolRun, error) {
		run := agentbasic.ToolRun{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments, Status: "completed"}
		if specialRun, handled, err := s.executeNarratorTool(execCtx, narratorID, narrator, &draftMessageID, pipeline, call); handled || err != nil {
			pipeline.capture(&specialRun)
			return specialRun, err
		}
		if blocked, output := planModeToolRestriction(execCwd, narrator, call); blocked {
			run.Status = "denied"
			run.Output = output
			pipeline.capture(&run)
			return run, nil
		}
		permission, err := s.resolveToolPermission(execCtx, narrator, execCwd, call)
		if err != nil {
			return agentbasic.ToolRun{}, err
		}
		if permission.Action == "deny" {
			run.Status = "denied"
			run.Output = firstNonEmptyString(permission.Reason, "Permission denied.")
			pipeline.capture(&run)
			return run, nil
		}
		if shouldRunDangerReflectionBeforeExecution(s.Settings, narrator, permission, call) {
			decision, err := s.runDangerReflection(execCtx, narratorID, call)
			if err != nil {
				run.Status = "error"
				run.Output = "Danger reflection failed: " + err.Error()
				return run, nil
			}
			if decision.Action == "cancel" {
				run.Status = "denied"
				run.Output = firstNonEmptyString(decision.Feedback, "Danger reflection rejected this tool call.")
				return run, nil
			}
		}
		if permission.Action == "ask" {
			if draftMessageID == "" {
				draftMessageID, err = s.createAssistantDraft(execCtx, narratorID)
				if err != nil {
					return agentbasic.ToolRun{}, err
				}
			}
			run.Recorded = true
			if err := s.recordPendingToolCall(execCtx, narratorID, draftMessageID, narrator, call); err != nil {
				return agentbasic.ToolRun{}, err
			}
			if err := s.setNarratorRunSubstatus(execCtx, narratorID, narrator.IsBackground, []string{"awaiting_permission"}); err != nil {
				return agentbasic.ToolRun{}, err
			}
			decision, err := s.waitForPermissionDecision(execCtx, narratorID, call.ID, 5*time.Minute)
			if err != nil {
				return agentbasic.ToolRun{}, err
			}
			if toolDecisionRejected(decision.Status) {
				run.Status = decision.Status
				run.Output = decisionOutput(decision)
				if run.Output == "" {
					run.Output = "Permission denied."
				}
				if err := s.finishRecordedToolCall(execCtx, narratorID, call.ID, draftMessageID, run, decision, "fail"); err != nil {
					return agentbasic.ToolRun{}, err
				}
				_ = s.setNarratorRunSubstatus(execCtx, narratorID, narrator.IsBackground, []string{"calling_model"})
				return run, nil
			}
			if err := s.setNarratorRunSubstatus(execCtx, narratorID, narrator.IsBackground, []string{"executing_tool"}); err != nil {
				return agentbasic.ToolRun{}, err
			}
			if contextRun, handled, err := s.executeContextualAgentTool(execCtx, narratorID, narrator, call); handled || err != nil {
				run = contextRun
				if err != nil {
					return agentbasic.ToolRun{}, err
				}
			} else if externalRun, handled := s.executeExternalTool(execCtx, call); handled {
				run = externalRun
			} else {
				fileChange := prepareToolFileChange(execCwd, call)
				run.Output = s.executeBuiltInAgentTool(execCtx, execCwd, narratorID, call)
				completeToolFileChange(fileChange)
				run.FileChange = fileChange
			}
			pipeline.capture(&run)
			if err := s.finishRecordedToolCall(execCtx, narratorID, call.ID, draftMessageID, run, decision, "success"); err != nil {
				return agentbasic.ToolRun{}, err
			}
			_ = s.setNarratorRunSubstatus(execCtx, narratorID, narrator.IsBackground, []string{"calling_model"})
			return run, nil
		}
		if contextRun, handled, err := s.executeContextualAgentTool(execCtx, narratorID, narrator, call); handled || err != nil {
			run = contextRun
			if err != nil {
				return agentbasic.ToolRun{}, err
			}
		} else if externalRun, handled := s.executeExternalTool(execCtx, call); handled {
			run = externalRun
		} else {
			fileChange := prepareToolFileChange(cwd, call)
			run.Output = s.executeBuiltInAgentTool(execCtx, cwd, narratorID, call)
			completeToolFileChange(fileChange)
			run.FileChange = fileChange
		}
		pipeline.capture(&run)
		return run, nil
	}
	reasoningEffort := ""
	if narrator.ReasoningEffort != nil {
		reasoningEffort = *narrator.ReasoningEffort
	}
	toolScope := "normal"
	if meta, ok := s.reviewMetaForNarrator(ctx, narrator); ok && meta.Role == "review" {
		toolScope = "review"
	}
	externalTools := []agentbasic.ExternalToolSpec{}
	if s.ExternalTools != nil {
		externalTools = s.ExternalTools.AgentToolSpecs(ctx, externalToolContextFromNarrator(narrator))
	}
	runCtx, cancel := context.WithCancel(ctx)
	runToken := s.registerActiveRun(narratorID, cancel)
	defer func() {
		cancel()
		s.unregisterActiveRun(narratorID, runToken)
	}()

	stream, err := agentbasic.RunAgentLoop(runCtx, s.Settings, model, history, cwd,
		agentbasic.WithToolExecutor(executor),
		agentbasic.WithSystemPrompt(s.agentSystemPrompt(ctx, narrator)),
		agentbasic.WithMaxTurns(s.agentMaxTurns()),
		agentbasic.WithReasoningEffort(reasoningEffort),
		agentbasic.WithToolScope(toolScope),
		agentbasic.WithEnabledTools(anyStringSlice(narrator.EnabledTools)),
		agentbasic.WithDisabledTools(disabledToolsFromTraits(narrator.Traits)),
		agentbasic.WithExternalTools(externalTools),
		agentbasic.WithFirstTokenTimeout(s.agentFirstTokenTimeout()),
		agentbasic.WithToolInventoryEvent(true),
		agentbasic.WithStickySessionKey(narratorID),
	)
	if err != nil {
		if narrator.IsBackground && s.narratorRunCancelled(ctx, narratorID) {
			return
		}
		s.persistAgentError(ctx, narratorID, err, narrator.IsBackground)
		return
	}
	state := &agentRunState{narratorID: narratorID, narrator: narrator, draftMessageID: &draftMessageID, providerPrefix: providerPrefixFromModelValue(model)}
	for event := range stream {
		if err := s.handleAgentEvent(ctx, state, event); err != nil {
			if isCancelledError(err) || runCtx.Err() != nil {
				_ = s.finishAgentRunError(ctx, state, "Aborted")
				return
			}
			s.persistAgentError(ctx, narratorID, err, narrator.IsBackground)
			return
		}
	}
	if runCtx.Err() != nil && !state.completed {
		_ = s.finishAgentRunCancelled(ctx, state)
		return
	}
	if narrator.IsBackground && s.narratorRunCancelled(ctx, narratorID) {
		return
	}
	if !state.completed {
		_ = s.finishAgentRunError(ctx, state, "agent loop ended without completion")
		return
	}
}

func (s *Service) persistAgentError(ctx context.Context, narratorID string, err error, isBackground bool) error {
	message := "AI provider call failed: " + err.Error()
	msg, msgErr := s.addMessage(ctx, narratorID, "disp", message, nil)
	if msgErr != nil {
		s.broadcastAgentErrorPersistenceFailure(narratorID, "message", msgErr)
		return fmt.Errorf("persist agent error message: %w", msgErr)
	}
	var updateErr error
	if isBackground {
		_, updateErr = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["error"]', background_status = 'failed', background_result = ?, background_completed_at = ?, error_message = ?, updated_at = ? WHERE id = ?`, message, db.NowISO(), message, db.NowISO(), narratorID)
	} else {
		_, updateErr = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["error"]', error_message = ?, updated_at = ? WHERE id = ?`, message, db.NowISO(), narratorID)
	}
	if updateErr != nil {
		s.broadcastAgentErrorPersistenceFailure(narratorID, "status", updateErr)
		return fmt.Errorf("persist agent error status: %w", updateErr)
	}
	if isBackground {
		if narrator, getErr := s.Get(ctx, narratorID); getErr == nil && narrator.ParentNarratorID != nil {
			if err := s.finishBackgroundTaskRecord(ctx, *narrator.ParentNarratorID, narratorID, "failed", message); err != nil {
				s.broadcastAgentErrorPersistenceFailure(narratorID, "background_task", err)
				return fmt.Errorf("persist agent background task failure: %w", err)
			}
		}
	}
	s.broadcastMessage(ctx, narratorID, msg)
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "status", "narratorId": narratorID, "status": "idle", "substatus": []string{"error"}, "isBackground": isBackground, "backgroundStatus": nullableTrimmedString("failed")})
	}
	return nil
}

func (s *Service) broadcastAgentErrorPersistenceFailure(narratorID, stage string, err error) {
	if s.Hub == nil {
		return
	}
	s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "error", "narratorId": narratorID, "code": "AGENT_ERROR_PERSIST_FAILED", "stage": stage, "message": err.Error(), "fallback": true, "fatal": false})
}

func (s *Service) agentSystemPrompt(ctx context.Context, narrator Narrator) string {
	prompt := agentbasic.DefaultSystemPrompt
	if narrator.SystemPrompt != nil && strings.TrimSpace(*narrator.SystemPrompt) != "" {
		prompt = strings.TrimSpace(*narrator.SystemPrompt)
	}
	cwd := ""
	if narrator.Cwd != nil {
		cwd = strings.TrimSpace(*narrator.Cwd)
	}
	if cwd != "" {
		prompt = appendPromptSection(prompt, "Current Working Directory", "`"+cwd+"`\n\nAll tools already use this as their default working directory. Do NOT `cd` into it in Bash commands — it is redundant.")
		if projectPrompt := firstReadableFile(filepath.Join(cwd, "AGENT.md"), filepath.Join(cwd, "CLAUDE.md")); projectPrompt != "" {
			prompt = appendPromptSection(prompt, "Project Instructions", projectPrompt)
		}
	} else {
		prompt = appendPromptSection(prompt, "NarraFork Go Backend Fallback", "No narrator working directory is available, so project AGENT.md/CLAUDE.md instructions could not be loaded.")
	}
	if globalPrompt := firstReadableFile(globalPromptFileCandidates()...); globalPrompt != "" {
		prompt = appendPromptSection(prompt, "Global Instructions", truncateString(globalPrompt, 50000))
	}
	if narrator.ContextSummary != nil {
		if summary := strings.TrimSpace(*narrator.ContextSummary); summary != "" {
			prompt = appendPromptSection(prompt, "Conversation Context", summary)
		}
	}
	if narrator.PlanMode {
		planFile := ".narrafork/plan-<id>.md"
		if narrator.PlanFileID != nil && strings.TrimSpace(*narrator.PlanFileID) != "" {
			planFile = planFileRelativePath(strings.TrimSpace(*narrator.PlanFileID))
		}
		prompt = appendPromptSection(prompt, "Plan Mode", "You are currently in NarraFork plan mode. Explore and design an implementation plan first; do not modify implementation files until the plan is explicitly approved. Write/Edit are restricted to the designated plan file "+planFile+", or pass the complete plan directly to ExitPlanMode. If TS-style plan compact cannot be persisted, the Go backend will fall back to normal message history/compact context.")
	}
	_ = ctx
	return prompt
}

func appendPromptSection(prompt, title, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return prompt
	}
	sep := ""
	if strings.TrimSpace(prompt) != "" {
		sep = "\n\n"
	}
	return prompt + sep + "## " + title + "\n\n" + body
}

func firstReadableFile(paths ...string) string {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data)
		}
	}
	return ""
}

func globalPromptFileCandidates() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{}
	}
	return []string{filepath.Join(home, ".agents", "AGENT.md"), filepath.Join(home, ".claude", "CLAUDE.md")}
}

func truncateString(value string, maxChars int) string {
	if maxChars <= 0 || len(value) <= maxChars {
		return value
	}
	return value[:maxChars]
}

func (s *Service) agentMaxTurns() int {
	if s.Settings == nil {
		return 0
	}
	raw := s.Settings.Raw()
	agent, ok := raw["agent"].(map[string]any)
	if !ok {
		return 0
	}
	switch value := agent["maxTurns"].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		parsed, _ := strconv.Atoi(value.String())
		return parsed
	default:
		return 0
	}
}

func (s *Service) agentHistoryLimit() int {
	limit := s.agentIntSetting("historyLimit", 80)
	if limit <= 0 {
		limit = 80
	}
	if limit > 200 {
		limit = 200
	}
	return limit
}

func (s *Service) agentFirstTokenTimeout() time.Duration {
	value := s.agentIntSetting("firstTokenTimeoutMs", 60000)
	if value <= 0 {
		return 0
	}
	if value > 600000 {
		value = 600000
	}
	return time.Duration(value) * time.Millisecond
}

func (s *Service) agentIntSetting(key string, fallback int) int {
	if s.Settings == nil {
		return fallback
	}
	raw := s.Settings.Raw()
	agent, ok := raw["agent"].(map[string]any)
	if !ok {
		return fallback
	}
	switch value := agent[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		parsed, err := strconv.Atoi(value.String())
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func (s *Service) agentHistory(ctx context.Context, narratorID string, limit int) ([]agentbasic.Message, error) {
	if limit <= 0 {
		limit = 80
	}
	if limit > 200 {
		limit = 200
	}
	compactRef, hasCompact, err := s.lastCompactRef(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	conditions := []string{"r.narrator_id = ?", "r.segment_compact_id IS NULL"}
	args := []any{narratorID}
	if hasCompact {
		conditions = append(conditions, "r.seq > ?")
		args = append(args, compactRef.Seq)
	}
	isSubagent, err := s.isSubagentNarrator(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	if !isSubagent {
		conditions = append(conditions, "m.parent_tool_use_id IS NULL")
	}
	args = append(args, limit)
	refs, err := s.queryMessageRefs(ctx, strings.Join(conditions, " AND "), "r.seq DESC", args...)
	if err != nil {
		return nil, err
	}
	reverseMessageRefs(refs)
	messages, err := s.loadMessagesForRefs(ctx, narratorID, refs)
	if err != nil {
		return nil, err
	}
	history := []agentbasic.Message{}
	if hasCompact {
		if summary, err := s.compactSummary(ctx, compactRef.MessageID); err == nil && strings.TrimSpace(summary) != "" {
			history = append(history, agentbasic.Message{Role: "user", Content: "Prior compacted conversation summary:\n" + strings.TrimSpace(summary)})
		}
	}
	for _, msg := range messages {
		if msg.Role == "disp" || msg.Role == "system" {
			continue
		}
		history = append(history, agentMessagesFromStoredMessage(msg)...)
	}
	return history, nil
}

func (s *Service) lastCompactRef(ctx context.Context, narratorID string) (messageRef, bool, error) {
	var ref messageRef
	err := s.DB.QueryRowContext(ctx, `SELECT message_id, seq FROM narrator_message_refs WHERE narrator_id = ? AND is_compact = 1 ORDER BY seq DESC LIMIT 1`, narratorID).Scan(&ref.MessageID, &ref.Seq)
	if errors.Is(err, sql.ErrNoRows) {
		return ref, false, nil
	}
	return ref, err == nil, err
}

func (s *Service) compactSummary(ctx context.Context, messageID string) (string, error) {
	var contentText, contentJSON sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT content_text, content_json FROM narrator_messages WHERE id = ?`, messageID).Scan(&contentText, &contentJSON); err != nil {
		return "", err
	}
	if summary := compactSummaryFromContentJSON(contentJSON); strings.TrimSpace(summary) != "" {
		return summary, nil
	}
	if contentText.Valid && strings.TrimSpace(contentText.String) != "" {
		return contentText.String, nil
	}
	return "", nil
}

type agentPipelineCapture struct {
	Alias string
	Tool  string
	Text  string
}

type agentPipelineState struct {
	Active     bool
	Label      string
	MaxPreview int
	Captures   []agentPipelineCapture
}

func (p *agentPipelineState) start(label string, maxPreview int) string {
	if maxPreview <= 0 || maxPreview > 100 {
		maxPreview = 100
	}
	p.Active = true
	p.Label = strings.TrimSpace(label)
	p.MaxPreview = maxPreview
	p.Captures = nil
	if p.Label != "" {
		return "Pipeline capture started: " + p.Label
	}
	return "Pipeline capture started."
}

func (p *agentPipelineState) capture(run *agentbasic.ToolRun) {
	if p == nil || !p.Active || run == nil {
		return
	}
	if run.Name == "StartPipeline" || run.Name == "EndPipeline" {
		return
	}
	alias := fmt.Sprintf("p%d", len(p.Captures)+1)
	text := run.Output
	p.Captures = append(p.Captures, agentPipelineCapture{Alias: alias, Tool: run.Name, Text: text})
	preview := text
	if len(preview) > p.MaxPreview {
		preview = preview[:p.MaxPreview] + "..."
	}
	run.Output = fmt.Sprintf("Captured as %s (%d chars). Preview:\n%s", alias, len(text), preview)
}

func (p *agentPipelineState) end(rule string, aliases []string, format string, maxChars int) string {
	if p == nil || !p.Active {
		return "No active pipeline."
	}
	if maxChars <= 0 || maxChars > 50000 {
		maxChars = 12000
	}
	selected := p.selectCaptures(rule, aliases)
	lines := []string{}
	plain := strings.EqualFold(format, "plain")
	for _, capture := range selected {
		if plain {
			lines = append(lines, capture.Text)
		} else {
			lines = append(lines, fmt.Sprintf("## %s — %s\n%s", capture.Alias, capture.Tool, capture.Text))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "No captured pipeline output matched the rule.")
	}
	out := strings.Join(lines, "\n\n")
	out = applyPipelineRule(out, rule)
	if len(out) > maxChars {
		out = out[:maxChars] + "\n[truncated]"
	}
	p.Active = false
	p.Captures = nil
	return out
}

func (p *agentPipelineState) selectCaptures(rule string, aliases []string) []agentPipelineCapture {
	aliasSet := map[string]bool{}
	if strings.HasPrefix(strings.TrimSpace(rule), "from ") {
		left := strings.TrimSpace(rule)
		if idx := strings.Index(left, "|"); idx >= 0 {
			left = left[:idx]
		}
		for _, token := range strings.Fields(strings.TrimPrefix(left, "from")) {
			aliasSet[strings.TrimSpace(token)] = true
		}
	}
	for _, alias := range aliases {
		if strings.TrimSpace(alias) != "" {
			aliasSet[strings.TrimSpace(alias)] = true
		}
	}
	if len(aliasSet) == 0 {
		return p.Captures
	}
	out := []agentPipelineCapture{}
	for _, capture := range p.Captures {
		if aliasSet[capture.Alias] {
			out = append(out, capture)
		}
	}
	return out
}

func applyPipelineRule(input string, rule string) string {
	out := input
	for _, part := range strings.Split(rule, "|") {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, "from ") {
			continue
		}
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "grep":
			ignoreCase := len(fields) > 1 && fields[1] == "-i"
			patternIndex := 1
			if ignoreCase {
				patternIndex = 2
			}
			if len(fields) > patternIndex {
				out = pipelineGrep(out, strings.Join(fields[patternIndex:], " "), ignoreCase)
			}
		case "head":
			out = pipelineHeadTail(out, pipelineCount(fields, 20), true)
		case "tail":
			out = pipelineHeadTail(out, pipelineCount(fields, 20), false)
		}
	}
	return out
}

func pipelineGrep(input, pattern string, ignoreCase bool) string {
	pattern = strings.Trim(pattern, `"'`)
	if ignoreCase {
		pattern = strings.ToLower(pattern)
	}
	lines := []string{}
	for _, line := range strings.Split(input, "\n") {
		matchLine := line
		if ignoreCase {
			matchLine = strings.ToLower(line)
		}
		if strings.Contains(matchLine, pattern) {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func pipelineHeadTail(input string, count int, head bool) string {
	lines := strings.Split(input, "\n")
	if count < 0 {
		count = 0
	}
	if count > len(lines) {
		count = len(lines)
	}
	if head {
		return strings.Join(lines[:count], "\n")
	}
	return strings.Join(lines[len(lines)-count:], "\n")
}

func pipelineCount(fields []string, fallback int) int {
	for i, field := range fields {
		if field == "-n" && i+1 < len(fields) {
			if n, err := strconv.Atoi(fields[i+1]); err == nil {
				return n
			}
		}
	}
	return fallback
}

func (s *Service) reviewMetaForNarrator(ctx context.Context, narrator Narrator) (reviewChapterMeta, bool) {
	if narrator.ChapterID == nil || strings.TrimSpace(*narrator.ChapterID) == "" {
		return reviewChapterMeta{}, false
	}
	return s.reviewMetaForChapter(ctx, *narrator.ChapterID)
}

func (s *Service) reviewMetaForChapter(ctx context.Context, chapterID string) (reviewChapterMeta, bool) {
	var meta reviewChapterMeta
	err := s.DB.QueryRowContext(ctx, `SELECT id, role, review_status, review_source_chapter_id, worktree_path, start_commit_sha, head_commit_sha FROM chapters WHERE id = ?`, chapterID).Scan(&meta.ID, &meta.Role, &meta.ReviewStatus, &meta.SourceChapterID, &meta.WorktreePath, &meta.StartCommitSHA, &meta.HeadCommitSHA)
	if err != nil || meta.Role != "review" {
		return meta, false
	}
	return meta, true
}

func (s *Service) executeNarratorTool(ctx context.Context, narratorID string, narrator Narrator, draftMessageID *string, pipeline *agentPipelineState, call agentbasic.ToolCall) (agentbasic.ToolRun, bool, error) {
	run := agentbasic.ToolRun{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments, Status: "completed"}
	var args map[string]any
	if strings.TrimSpace(call.Function.Arguments) != "" {
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			run.Output = "invalid tool arguments: " + err.Error()
			return run, true, nil
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	switch call.Function.Name {
	case "StartPipeline":
		run.Output = pipeline.start(stringFromAny(args["label"]), anyInt(args["maxPreviewChars"], 100))
		return run, true, nil
	case "EndPipeline":
		run.Output = pipeline.end(stringFromAny(args["rule"]), anyStringSlice(args["aliases"]), stringFromAny(args["format"]), anyInt(args["maxChars"], 12000))
		return run, true, nil
	case "AskUserQuestion":
		askRun, err := s.executeAskUserQuestion(ctx, narratorID, narrator, draftMessageID, call)
		return askRun, true, err
	case "Agent":
		args["_tool_use_id"] = call.ID
		payload, err := s.executeAgentTool(ctx, narratorID, args)
		if err != nil {
			return run, true, err
		}
		run.Output = jsonToolOutput(payload)
		return run, true, nil
	case "Await":
		payload, err := s.awaitTool(ctx, narratorID, args)
		if err != nil {
			return run, true, err
		}
		run.Output = jsonToolOutput(payload)
		return run, true, nil
	case "TeamStatus":
		payload, err := s.teamStatusTool(ctx, narratorID, args)
		if err != nil {
			return run, true, err
		}
		run.Output = jsonToolOutput(payload)
		return run, true, nil
	case "Send":
		args["_tool_use_id"] = call.ID
		payload, err := s.sendTool(ctx, narratorID, args)
		if err != nil {
			return run, true, err
		}
		run.Output = jsonToolOutput(payload)
		return run, true, nil
	case "Skill":
		skillName := firstNonEmptyString(stringFromAny(args["skill"]), stringFromAny(args["name"]))
		root, _ := s.skillRootForNarrator(ctx, narratorID)
		run.Output = simplecontent.SkillToolOutput(skillName, stringFromAny(args["args"]), root)
		return run, true, nil
	case "LearningGuide":
		run.Output = simplecontent.LearningGuideOutput(stringFromAny(args["mode"]), stringFromAny(args["query"]), stringFromAny(args["id"]), stringFromAny(args["lang"]))
		return run, true, nil
	case "TaskCreate":
		payload, err := s.updateTodos(ctx, narratorID, call.ID, args["todos"])
		if err != nil {
			return run, true, err
		}
		run.Output = payload
		return run, true, nil
	case "ConcludeReview":
		output, err := s.concludeReviewTool(ctx, narratorID, narrator, args)
		if err != nil {
			run.Status = "error"
			run.Output = err.Error()
			return run, true, nil
		}
		run.Output = output
		return run, true, nil
	case "EnterPlanMode":
		payload, err := s.setNarratorPlanMode(ctx, narratorID, true)
		if err != nil {
			return run, true, err
		}
		run.Output = jsonToolOutput(payload)
		return run, true, nil
	case "ExitPlanMode":
		payload, err := s.exitPlanModeTool(ctx, narratorID, args)
		if err != nil {
			return run, true, err
		}
		run.Output = jsonToolOutput(payload)
		return run, true, nil
	case "GetGoals":
		goals, err := s.loadGoals(ctx, narratorID)
		if err != nil {
			return run, true, err
		}
		run.Output = jsonToolOutput(map[string]any{"goals": goals})
		return run, true, nil
	case "AddGoal":
		objective := strings.TrimSpace(stringFromAny(args["objective"]))
		if objective == "" {
			run.Output = "objective is required"
			return run, true, nil
		}
		payload, err := s.addGoal(ctx, narratorID, objective, "agent")
		if err != nil {
			return run, true, err
		}
		run.Output = jsonToolOutput(payload)
		return run, true, nil
	case "UpdateGoal":
		goalID := firstNonEmptyString(stringFromAny(args["goal_id"]), stringFromAny(args["goalId"]), stringFromAny(args["id"]))
		if goalID == "" {
			run.Output = "goal_id is required"
			return run, true, nil
		}
		broadcastTargetID := narratorID
		if narrator.ParentNarratorID != nil && strings.TrimSpace(*narrator.ParentNarratorID) != "" {
			broadcastTargetID = strings.TrimSpace(*narrator.ParentNarratorID)
		}
		reflectionMeta := &goalReflectionContext{
			ToolUseID:         call.ID,
			ToolName:          call.Function.Name,
			InputJSON:         args,
			BroadcastTargetID: broadcastTargetID,
		}
		payload, err := s.updateGoalWithReflectionContext(ctx, narratorID, goalID, stringFromAny(args["status"]), stringFromAny(args["objective"]), stringFromAny(args["evidence"]), stringFromAny(args["reflection"]), reflectionMeta)
		if err != nil {
			return run, true, err
		}
		run.Output = jsonToolOutput(payload)
		if strings.TrimSpace(stringFromAny(payload["error"])) != "" {
			run.Status = "denied"
			run.Error = strings.TrimSpace(stringFromAny(payload["error"]))
		}
		return run, true, nil
	default:
		return run, false, nil
	}
}

func (s *Service) checkAndResetReviewGitState(ctx context.Context, chapterID string) (bool, bool, string) {
	meta, ok := s.reviewMetaForChapter(ctx, chapterID)
	if !ok {
		return false, false, ""
	}
	if !meta.WorktreePath.Valid || strings.TrimSpace(meta.WorktreePath.String) == "" {
		return true, false, ""
	}
	expected := strings.TrimSpace(meta.StartCommitSHA.String)
	if expected == "" {
		expected = strings.TrimSpace(meta.HeadCommitSHA.String)
	}
	if expected == "" {
		return true, false, ""
	}
	gitSvc := gitx.NewService()
	status, statusErr := gitSvc.StatusSummary(ctx, meta.WorktreePath.String)
	currentHead, headErr := gitSvc.HeadCommit(ctx, meta.WorktreePath.String)
	if statusErr != nil || headErr != nil {
		return true, false, ""
	}
	hasChanges := status.Staged > 0 || status.Unstaged > 0 || status.Untracked > 0
	headMoved := strings.TrimSpace(currentHead) != expected
	if !hasChanges && !headMoved {
		return true, false, ""
	}
	if headMoved {
		_ = gitSvc.Reset(ctx, meta.WorktreePath.String, expected, "hard")
	}
	_ = gitSvc.DiscardAll(ctx, meta.WorktreePath.String)
	reasons := []string{}
	if hasChanges {
		reasons = append(reasons, "file modifications detected")
	}
	if headMoved {
		reasons = append(reasons, "HEAD commit was moved")
	}
	message := fmt.Sprintf("[System] Your working tree has been reset to the original state (%s). As a reviewer, you must not modify any files. Please re-examine the code and output your review conclusion based on the original source.", strings.Join(reasons, ", "))
	return true, true, message
}

func (s *Service) concludeReviewChapter(ctx context.Context, reviewChapterID string) error {
	meta, ok := s.reviewMetaForChapter(ctx, reviewChapterID)
	if !ok || strings.TrimSpace(meta.ReviewStatus.String) != "reviewing" || !meta.SourceChapterID.Valid || meta.SourceChapterID.String == "" {
		return nil
	}
	now := db.NowISO()
	if _, err := s.DB.ExecContext(ctx, `UPDATE chapters SET review_status = 'concluded', updated_at = ? WHERE id = ?`, now, reviewChapterID); err != nil {
		return err
	}
	return s.injectReviewFeedback(ctx, reviewChapterID, meta.SourceChapterID.String)
}

func (s *Service) injectReviewFeedback(ctx context.Context, reviewChapterID, sourceChapterID string) error {
	var verdict string
	var findingsRaw sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT verdict, findings_json FROM review_conclusions WHERE review_chapter_id = ? ORDER BY created_at DESC LIMIT 1`, reviewChapterID).Scan(&verdict, &findingsRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var sourceNarratorID string
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM narrators WHERE chapter_id = ? AND variant = 'primary' ORDER BY created_at ASC LIMIT 1`, sourceChapterID).Scan(&sourceNarratorID); err != nil {
		return nil
	}
	findings := []map[string]any{}
	if findingsRaw.Valid && findingsRaw.String != "" {
		_ = json.Unmarshal([]byte(findingsRaw.String), &findings)
	}
	text := buildReviewFeedbackText(verdict, findings)
	content := []map[string]any{{"type": "review_feedback", "verdict": verdict, "findings": findings, "reviewChapterId": reviewChapterID}}
	msg, err := s.addMessageWithBlocks(ctx, sourceNarratorID, "system", text, content, nil)
	if err != nil {
		return err
	}
	s.broadcastMessage(ctx, sourceNarratorID, msg)
	return nil
}

func buildReviewFeedbackText(verdict string, findings []map[string]any) string {
	labels := map[string]string{"approve": "Approved", "request_changes": "Changes Requested", "comment_only": "Comments Only"}
	icons := map[string]string{"critical": "🚨", "major": "⚠️", "minor": "📋", "suggestion": "💡"}
	label := labels[verdict]
	if label == "" {
		label = verdict
	}
	var builder strings.Builder
	builder.WriteString("## Code Review: ")
	builder.WriteString(label)
	builder.WriteString("\n\n")
	for _, finding := range findings {
		severity := stringFromAny(finding["severity"])
		builder.WriteString("- ")
		if icon := icons[severity]; icon != "" {
			builder.WriteString(icon)
			builder.WriteString(" ")
		}
		builder.WriteString("**[")
		builder.WriteString(severity)
		builder.WriteString("]** ")
		if file := stringFromAny(finding["file"]); file != "" {
			builder.WriteString("`")
			builder.WriteString(file)
			if line := anyInt(finding["line"], 0); line > 0 {
				builder.WriteString(fmt.Sprintf(":%d", line))
			}
			builder.WriteString("` — ")
		}
		builder.WriteString(stringFromAny(finding["message"]))
		builder.WriteString("\n")
	}
	if len(findings) > 0 {
		builder.WriteString("\n")
	}
	if verdict == "request_changes" {
		builder.WriteString("Please address the above findings before merging.\n")
	}
	return builder.String()
}

func (s *Service) concludeReviewTool(ctx context.Context, narratorID string, narrator Narrator, args map[string]any) (string, error) {
	_ = narratorID
	meta, ok := s.reviewMetaForNarrator(ctx, narrator)
	if !ok {
		return "", fmt.Errorf("Error: ConcludeReview can only be used in review chapters.")
	}
	if !meta.SourceChapterID.Valid || strings.TrimSpace(meta.SourceChapterID.String) == "" {
		return "", fmt.Errorf("Error: Review chapter has no source chapter.")
	}
	if strings.TrimSpace(meta.ReviewStatus.String) != "reviewing" {
		return "", fmt.Errorf("Error: Review is already %s.", firstNonEmptyString(meta.ReviewStatus.String, "in an invalid state"))
	}
	verdict := strings.TrimSpace(stringFromAny(args["verdict"]))
	if verdict != "approve" && verdict != "request_changes" && verdict != "comment_only" {
		return "", fmt.Errorf("verdict must be approve, request_changes, or comment_only")
	}
	findings, err := normalizeReviewFindings(args["findings"])
	if err != nil {
		return "", err
	}
	findingsJSON, _ := json.Marshal(findings)
	now := db.NowISO()
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO review_conclusions (id, review_chapter_id, source_chapter_id, verdict, findings_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`, id.New(), meta.ID, meta.SourceChapterID.String, verdict, string(findingsJSON), now); err != nil {
		return "", err
	}
	if err := s.concludeReviewChapter(ctx, meta.ID); err != nil {
		return "", err
	}
	counts := map[string]int{"critical": 0, "major": 0, "minor": 0, "suggestion": 0}
	for _, finding := range findings {
		counts[stringFromAny(finding["severity"])]++
	}
	parts := []string{}
	for _, severity := range []string{"critical", "major", "minor", "suggestion"} {
		if counts[severity] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[severity], severity))
		}
	}
	summary := ""
	if len(parts) > 0 {
		summary = " (" + strings.Join(parts, ", ") + ")"
	}
	return fmt.Sprintf("Review conclusion submitted successfully.\nVerdict: %s\nFindings: %d total%s", verdict, len(findings), summary), nil
}

func normalizeReviewFindings(raw any) ([]map[string]any, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("findings must be an array")
	}
	findings := make([]map[string]any, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("each finding must be an object")
		}
		severity := strings.TrimSpace(stringFromAny(m["severity"]))
		if severity != "critical" && severity != "major" && severity != "minor" && severity != "suggestion" {
			return nil, fmt.Errorf("finding severity must be critical, major, minor, or suggestion")
		}
		message := strings.TrimSpace(stringFromAny(m["message"]))
		if message == "" {
			return nil, fmt.Errorf("finding message is required")
		}
		finding := map[string]any{"severity": severity, "message": message}
		if file := strings.TrimSpace(stringFromAny(m["file"])); file != "" {
			finding["file"] = file
		}
		if line, ok := numericFindingLine(m["line"]); ok {
			finding["line"] = line
		}
		findings = append(findings, finding)
	}
	return findings, nil
}

func numericFindingLine(raw any) (int, bool) {
	switch v := raw.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case json.Number:
		parsed, err := strconv.Atoi(v.String())
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (s *Service) sendTool(ctx context.Context, callerID string, args map[string]any) (map[string]any, error) {
	message := strings.TrimSpace(stringFromAny(args["message"]))
	if message == "" {
		return map[string]any{"error": "message is required"}, nil
	}
	tokens := []string{}
	tokens = appendIfNonEmpty(tokens, stringFromAny(args["id"]))
	tokens = append(tokens, anyStringSlice(args["ids"])...)
	tokens = appendIfNonEmpty(tokens, stringFromAny(args["name"]))
	tokens = append(tokens, anyStringSlice(args["names"])...)
	if len(tokens) == 0 {
		return map[string]any{"error": "at least one target id, alias, or name is required"}, nil
	}
	children, err := s.accessibleSubagents(ctx, callerID)
	if err != nil {
		return nil, err
	}
	targets := []Narrator{}
	seen := map[string]bool{}
	notFound := []string{}
	for _, token := range tokens {
		child, ok := matchSubagent(children, token)
		if !ok {
			notFound = append(notFound, token)
			continue
		}
		if !seen[child.ID] {
			seen[child.ID] = true
			targets = append(targets, child)
		}
	}
	if anyBool(args["doInterrupt"]) {
		caller, err := s.Get(ctx, callerID)
		if err != nil {
			return nil, err
		}
		if caller.ParentNarratorID != nil && strings.TrimSpace(*caller.ParentNarratorID) != "" {
			return map[string]any{"ok": false, "error": "doInterrupt is only supported from a primary narrator to its child subagents"}, nil
		}
	}
	parentToolUseID := strings.TrimSpace(stringFromAny(args["_tool_use_id"]))
	results := []map[string]any{}
	for _, target := range targets {
		fresh, err := s.Get(ctx, target.ID)
		if err != nil {
			return nil, err
		}
		payload := subagentStatusPayload(fresh)
		if anyBool(args["doInterrupt"]) {
			if fresh.ParentNarratorID == nil || strings.TrimSpace(*fresh.ParentNarratorID) != callerID {
				payload["sent"] = false
				payload["error"] = "doInterrupt is only supported for this narrator's direct child subagents"
				results = append(results, payload)
				continue
			}
			payload["interrupted"] = s.CancelRun(fresh.ID)
		}
		if fresh.Status == "working" || fresh.Status == "waiting" || (fresh.IsBackground && ptrString(fresh.BackgroundStatus) == "running") {
			if fresh.IsBackground && ptrString(fresh.BackgroundStatus) == "running" && !s.runActive(fresh.ID) {
				if err := s.reconcileStaleBackgroundAgent(ctx, fresh); err != nil {
					return nil, err
				}
				fresh, _ = s.Get(ctx, fresh.ID)
			}
			if fresh.Status == "working" || fresh.Status == "waiting" || (fresh.IsBackground && ptrString(fresh.BackgroundStatus) == "running") {
				_, err := s.BufferUserMessage(ctx, fresh.ID, "agent", message, []map[string]any{{"type": "text", "text": message}}, anyBool(args["doInterrupt"]))
				if err != nil {
					return nil, err
				}
				payload = subagentStatusPayload(fresh)
				payload["sent"] = true
				payload["queued"] = true
				results = append(results, payload)
				continue
			}
		}
		_, err = s.startUserMessageRunWithParentToolUse(ctx, fresh.ID, "agent", message, []map[string]any{{"type": "text", "text": message}}, fresh, parentToolUseID)
		if err != nil {
			return nil, err
		}
		started, _ := s.Get(ctx, fresh.ID)
		payload = subagentStatusPayload(started)
		payload["sent"] = true
		payload["started"] = true
		results = append(results, payload)
	}
	if anyBool(args["await"]) {
		timeout := anyInt(args["timeout"], 30000)
		awaited := []map[string]any{}
		for _, target := range targets {
			payload, err := s.awaitTool(ctx, callerID, map[string]any{"type": "agent", "id": target.ID, "timeout": timeout})
			if err != nil {
				return nil, err
			}
			awaited = append(awaited, payload)
		}
		return map[string]any{"ok": len(notFound) == 0, "targets": results, "awaited": awaited, "notFound": notFound}, nil
	}
	return map[string]any{"ok": len(notFound) == 0, "targets": results, "notFound": notFound}, nil
}

func (s *Service) accessibleSubagents(ctx context.Context, callerID string) ([]Narrator, error) {
	caller, err := s.Get(ctx, callerID)
	if err != nil {
		return nil, err
	}
	if caller.ParentNarratorID != nil && strings.TrimSpace(*caller.ParentNarratorID) != "" {
		return s.childNarrators(ctx, *caller.ParentNarratorID)
	}
	return s.childNarrators(ctx, callerID)
}

func matchSubagent(children []Narrator, token string) (Narrator, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Narrator{}, false
	}
	for _, child := range children {
		if child.ID == token || strings.HasPrefix(child.ID, token) || childAlias(child) == token || ptrString(child.Title) == token {
			return child, true
		}
	}
	return Narrator{}, false
}

func appendIfNonEmpty(values []string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return values
	}
	return append(values, strings.TrimSpace(value))
}

func anyStringSlice(value any) []string {
	switch raw := value.(type) {
	case []any:
		out := []string{}
		for _, item := range raw {
			if text := strings.TrimSpace(stringFromAny(item)); text != "" {
				out = append(out, text)
			}
		}
		return out
	case []string:
		out := []string{}
		for _, item := range raw {
			if text := strings.TrimSpace(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func disabledToolsTrait(tools []string) string {
	payload, _ := json.Marshal(map[string]any{"version": 1, "tools": tools})
	return "custom-disabled-tools:" + base64.RawURLEncoding.EncodeToString(payload)
}

type customSubagentToolAccess struct {
	ToolAccess  string
	CustomTools []string
	Found       bool
}

var subagentKnownToolNames = []string{
	"Bash", "Read", "Write", "Edit", "Glob", "Grep", "WebFetch", "WebSearch",
	"ShareFile", "Browser", "Agent", "Await", "TeamStatus", "Send", "AskUserQuestion",
	"Skill", "LearningGuide", "TaskCreate", "StartPipeline", "EndPipeline", "EnterPlanMode",
	"ExitPlanMode", "GetGoals", "AddGoal", "UpdateGoal", "ConcludeReview",
}

func disabledToolsForSubagentType(subagentType string) []string {
	access := customSubagentToolAccessForType(subagentType)
	allowed := allowedToolsForSubagentType(subagentType, access)
	disabled := []string{}
	for _, tool := range subagentKnownToolNames {
		if !allowed[tool] {
			disabled = append(disabled, tool)
		}
	}
	return disabled
}

func allowedToolsForSubagentType(subagentType string, access customSubagentToolAccess) map[string]bool {
	explorePlan := []string{"Read", "Glob", "Grep", "WebSearch", "WebFetch", "Bash", "Write", "Edit", "TaskCreate", "TeamStatus", "Await", "Send"}
	general := append(append([]string{}, explorePlan...), "Skill")
	allowed := map[string]bool{}
	add := func(values []string) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" || value == "AskUserQuestion" || value == "Agent" {
				continue
			}
			allowed[value] = true
		}
	}
	if access.Found {
		switch access.ToolAccess {
		case "general":
			add(general)
		case "custom":
			add(access.CustomTools)
		default:
			add(explorePlan)
		}
		return allowed
	}
	switch subagentType {
	case "general":
		add(general)
	case "explore", "plan", "review":
		add(explorePlan)
	default:
		add(explorePlan)
	}
	return allowed
}

func customSubagentToolAccessForType(subagentType string) customSubagentToolAccess {
	subagentType = strings.TrimSpace(subagentType)
	if subagentType == "" || subagentType == "general" || subagentType == "explore" || subagentType == "plan" || subagentType == "review" {
		return customSubagentToolAccess{}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return customSubagentToolAccess{}
	}
	path := filepath.Join(home, ".narrafork", "subagents", subagentType, "SUBAGENT.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return customSubagentToolAccess{}
	}
	frontmatter := parseCustomSubagentFrontmatterForTools(string(data))
	access := strings.TrimSpace(frontmatter["toolAccess"])
	if access != "general" && access != "custom" {
		access = "readOnly"
	}
	return customSubagentToolAccess{ToolAccess: access, CustomTools: splitCustomTools(frontmatter["customTools"]), Found: true}
}

func parseCustomSubagentFrontmatterForTools(raw string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return out
	}
	currentArray := ""
	items := []string{}
	flushArray := func() {
		if currentArray != "" {
			out[currentArray] = strings.Join(items, "\n")
		}
		currentArray = ""
		items = nil
	}
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			flushArray()
			break
		}
		if currentArray != "" && strings.HasPrefix(trimmed, "-") {
			items = append(items, strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "-")), `"'`))
			continue
		}
		flushArray()
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if value == "" && key == "customTools" {
			currentArray = key
			items = []string{}
			continue
		}
		out[key] = strings.Trim(value, `"'`)
	}
	return out
}

func splitCustomTools(value string) []string {
	out := []string{}
	for _, line := range strings.Split(value, "\n") {
		if item := strings.TrimSpace(line); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func disabledToolsFromTraits(traits []string) []string {
	const prefix = "custom-disabled-tools:"
	for _, trait := range traits {
		if !strings.HasPrefix(trait, prefix) {
			continue
		}
		payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(trait, prefix))
		if err != nil {
			continue
		}
		var decoded struct {
			Version int      `json:"version"`
			Tools   []string `json:"tools"`
		}
		if err := json.Unmarshal(payload, &decoded); err != nil || decoded.Version != 1 {
			continue
		}
		return anyStringSlice(decoded.Tools)
	}
	return nil
}

func (s *Service) executeAgentTool(ctx context.Context, parentID string, args map[string]any) (map[string]any, error) {
	if stopID := strings.TrimSpace(stringFromAny(args["stop"])); stopID != "" {
		child, err := s.resolveChildNarrator(ctx, parentID, stopID)
		if err != nil {
			return map[string]any{"success": false, "status": "not_found", "id": stopID}, nil
		}
		now := db.NowISO()
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '[]', background_status = 'cancelled', background_completed_at = COALESCE(background_completed_at, ?), updated_at = ? WHERE id = ? AND parent_narrator_id = ?`, now, now, child.ID, parentID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"success": true, "status": "cancelled", "subagentId": child.ID, "alias": childAlias(child)}, nil
	}

	prompt := strings.TrimSpace(stringFromAny(args["prompt"]))
	if prompt == "" {
		return map[string]any{"error": "prompt is required when launching a subagent"}, nil
	}
	parent, err := s.Get(ctx, parentID)
	if err != nil {
		return nil, err
	}
	subagentType := normalizeSubagentType(stringFromAny(args["subagent_type"]))
	model := firstNonEmptyString(stringFromAny(args["model"]), ptrString(parent.Model), s.defaultNarratorModel())
	reasoningEffort := firstNonEmptyString(stringFromAny(args["reasoning_effort"]), ptrString(parent.ReasoningEffort))
	cwd, cwdErr := resolveSubagentCwd(ptrString(parent.Cwd), stringFromAny(args["workdir"]))
	if cwdErr != nil {
		return map[string]any{"error": cwdErr.Error()}, nil
	}
	alias := strings.TrimSpace(stringFromAny(args["alias"]))
	description := strings.TrimSpace(stringFromAny(args["description"]))
	title := description
	if title == "" {
		title = subagentType + " subagent"
	}
	runInBackground := anyBool(args["run_in_background"])
	child, err := s.createSubagentNarrator(ctx, parent, subagentType, title, model, reasoningEffort, cwd, alias, runInBackground)
	if err != nil {
		return nil, err
	}
	toolUseID := strings.TrimSpace(stringFromAny(args["_tool_use_id"]))
	if runInBackground {
		if err := s.createBackgroundAgentTask(ctx, parent.ID, child.ID, subagentType, title, toolUseID, alias); err != nil {
			return nil, err
		}
	}
	_, err = s.startUserMessageRunWithParentToolUse(ctx, child.ID, "agent", prompt, []map[string]any{{"type": "text", "text": prompt}}, child, toolUseID)
	if err != nil {
		if runInBackground {
			_ = s.finishBackgroundTaskRecord(ctx, parent.ID, child.ID, "failed", "failed to start background subagent: "+err.Error())
		}
		return nil, err
	}
	started := map[string]any{
		"subagentId":       child.ID,
		"alias":            alias,
		"subagentType":     subagentType,
		"status":           "started",
		"background":       runInBackground,
		"backgroundStatus": map[bool]string{true: "running", false: ""}[runInBackground],
	}
	if runInBackground {
		started["message"] = "Subagent started in background. Use Await with this subagentId or alias to inspect the result."
		return started, nil
	}
	timeout := anyInt(args["timeout"], 30000)
	awaited, err := s.awaitTool(ctx, parentID, map[string]any{"type": "agent", "id": child.ID, "timeout": timeout, "_parent_blocking": true, "_tool_use_id": toolUseID})
	if err != nil {
		return nil, err
	}
	if result, err := s.lastAssistantText(ctx, child.ID); err == nil && strings.TrimSpace(result) != "" {
		awaited["result"] = result
	}
	awaited["foreground"] = true
	awaited["subagentId"] = child.ID
	awaited["alias"] = alias
	return awaited, nil
}

func (s *Service) lastAssistantText(ctx context.Context, narratorID string) (string, error) {
	var text sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT m.content_text FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = ? AND m.role = 'assistant' ORDER BY r.seq DESC LIMIT 1`, narratorID).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return sqlNullString(text), nil
}

func (s *Service) createSubagentNarrator(ctx context.Context, parent Narrator, subagentType, title, model, reasoningEffort, cwd, alias string, isBackground bool) (Narrator, error) {
	now := db.NowISO()
	narratorID := id.New()
	permission := firstNonEmptyString(ptrString(parent.PermissionMode), "default")
	if subagentType == "explore" || subagentType == "review" {
		permission = "readOnly"
	}
	systemPrompt := subagentSystemPrompt(subagentType, cwd)
	traits := []string{"subagent", "subagent-type:" + subagentType}
	if disabled := disabledToolsForSubagentType(subagentType); len(disabled) > 0 {
		traits = append(traits, disabledToolsTrait(disabled))
	}
	if strings.TrimSpace(alias) != "" {
		traits = append(traits, "agent-alias:"+strings.TrimSpace(alias))
	}
	planMode := subagentType == "plan"
	if planMode {
		traits = append(traits, "plan")
	}
	traitsJSON, _ := json.Marshal(traits)
	backgroundStatus := any(nil)
	if isBackground {
		backgroundStatus = "pending"
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO narrators (
		id, chapter_id, type, subagent_type, title, inherit_mode, parent_narrator_id,
		model, system_prompt, permission_mode, reasoning_effort, fast_mode, relaxed_plan,
		plan_reflection_auto_approve_override, danger_reflection_override, status, substatus,
		plan_mode, cwd, prune_enabled, variant, traits, is_background, background_status,
		is_ask_in_passing, message_version, created_at, updated_at
	) VALUES (?, ?, 'subagent', ?, ?, 'fresh', ?, ?, ?, ?, ?, ?, ?, 'inherit', 'inherit', 'idle', '[]', ?, ?, 1, 'subagent', ?, ?, ?, 0, 0, ?, ?)`,
		narratorID, nullablePtr(parent.ChapterID), subagentType, nullableString(title), parent.ID,
		model, nullableString(systemPrompt), permission, nullableString(reasoningEffort), boolInt(parent.FastMode), boolInt(parent.RelaxedPlan),
		boolInt(planMode), nullableString(cwd), string(traitsJSON), boolInt(isBackground), backgroundStatus,
		now, now)
	if err != nil {
		return Narrator{}, err
	}
	return s.Get(ctx, narratorID)
}

const (
	backgroundTaskMaxOutputBytes = 512 * 1024
	backgroundTaskPreviewLength  = 200
	exitPlanFileMaxBytes         = 256 * 1024
)

func (s *Service) createBackgroundAgentTask(ctx context.Context, parentID, childID, subagentType, title, toolUseID, alias string) error {
	now := db.NowISO()
	_, err := s.DB.ExecContext(ctx, `INSERT OR REPLACE INTO background_tasks (
		id, parent_narrator_id, type, status, subagent_narrator_id, subagent_type,
		tool_use_id, alias, title, output, output_bytes, output_truncated, notified,
		started_at, created_at, updated_at
	) VALUES (?, ?, 'agent', 'running', ?, ?, ?, ?, ?, NULL, 0, 0, 0, ?, ?, ?)`,
		childID, parentID, childID, nullableString(subagentType), nullableString(toolUseID), nullableString(alias), nullableString(title), now, now, now)
	if err != nil {
		return err
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(parentID, map[string]any{"type": "background_task_started", "narratorId": parentID, "taskNarratorId": childID, "taskId": childID, "toolUseId": toolUseID, "subagentType": subagentType})
		s.broadcastBackgroundTaskStatus(parentID, childID, "running", nil, toolUseID)
	}
	return nil
}

func (s *Service) finishBackgroundTaskRecord(ctx context.Context, parentID, taskID, status, output string) error {
	storedOutput, outputBytes, truncated := backgroundTaskStoredOutput(output)
	now := db.NowISO()
	res, err := s.DB.ExecContext(ctx, `UPDATE background_tasks SET status = ?, output = ?, output_bytes = ?, output_truncated = ?, completed_at = COALESCE(completed_at, ?), updated_at = ? WHERE id = ? AND parent_narrator_id = ? AND status NOT IN ('completed', 'failed', 'cancelled', 'timeout')`, status, storedOutput, outputBytes, boolInt(truncated), now, now, taskID, parentID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows > 0 {
		theToolUseID, err := s.backgroundTaskToolUseID(ctx, parentID, taskID)
		if err != nil {
			if s.Hub != nil {
				s.Hub.BroadcastNarrator(parentID, map[string]any{"type": "background_task_status_changed", "narratorId": parentID, "taskId": taskID, "status": status, "output": nullableStringPtrValue(storedOutput), "degraded": true, "fallback": true, "fallbacks": []any{"background_task_tool_use_id_query_failed"}, "error": err.Error()})
			}
			return fmt.Errorf("query background task tool use id: %w", err)
		}
		if err := s.injectBackgroundSubagentResult(ctx, parentID, taskID, theToolUseID, status, storedOutput); err != nil {
			return err
		}
		s.broadcastBackgroundTaskStatus(parentID, taskID, status, nullableStringPtrValue(storedOutput), theToolUseID)
	}
	return nil
}

func (s *Service) injectBackgroundSubagentResult(ctx context.Context, parentID, taskID, toolUseID, status, output string) error {
	if strings.TrimSpace(parentID) == "" || strings.TrimSpace(toolUseID) == "" {
		return nil
	}
	dbStatus := "success"
	if status != "completed" {
		dbStatus = "error"
	}
	payload := map[string]any{
		"output":              output,
		"result":              output,
		"formatted":           formatSubagentResult(taskID, output),
		"subagentId":          taskID,
		"backgroundStatus":    status,
		"backgroundCompleted": status == "completed",
		"detached":            true,
	}
	outputJSON, _ := json.Marshal(payload)
	errorMessage := ""
	if dbStatus == "error" {
		errorMessage = output
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET output_json = ?, status = ?, completed_at = COALESCE(completed_at, ?), error_message = ? WHERE narrator_id = ? AND tool_use_id = ?`, string(outputJSON), dbStatus, db.NowISO(), nullableTrimmedString(errorMessage), parentID, toolUseID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows > 0 && s.Hub != nil {
		s.Hub.BroadcastNarrator(parentID, map[string]any{"type": "tool_completed", "narratorId": parentID, "toolUseId": toolUseID, "toolName": "Agent", "status": dbStatus, "output": output, "backgroundStatus": status, "subagentNarratorId": taskID})
	}
	return nil
}

func (s *Service) backgroundTaskToolUseID(ctx context.Context, parentID, taskID string) (string, error) {
	toolUseID := ""
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(tool_use_id, '') FROM background_tasks WHERE id = ? AND parent_narrator_id = ?`, taskID, parentID).Scan(&toolUseID); err != nil {
		return "", err
	}
	return toolUseID, nil
}

func (s *Service) broadcastBackgroundTaskStatus(parentID, taskID, status string, output *string, toolUseID string) {
	if s.Hub == nil || strings.TrimSpace(parentID) == "" || strings.TrimSpace(taskID) == "" {
		return
	}
	preview := ""
	if output != nil {
		preview = backgroundTaskPreview(*output)
	}
	switch status {
	case "completed":
		s.Hub.BroadcastNarrator(parentID, map[string]any{"type": "background_task_completed", "narratorId": parentID, "taskNarratorId": taskID, "toolUseId": toolUseID, "resultPreview": preview})
	case "failed":
		errText := preview
		if errText == "" {
			errText = "Unknown error"
		}
		s.Hub.BroadcastNarrator(parentID, map[string]any{"type": "background_task_failed", "narratorId": parentID, "taskNarratorId": taskID, "toolUseId": toolUseID, "error": errText})
	case "cancelled":
		s.Hub.BroadcastNarrator(parentID, map[string]any{"type": "background_task_cancelled", "narratorId": parentID, "taskNarratorId": taskID, "toolUseId": toolUseID})
	}
	var outputValue any
	if output != nil {
		outputValue = preview
	}
	s.Hub.BroadcastNarrator(parentID, map[string]any{"type": "background_task_status_changed", "narratorId": parentID, "taskId": taskID, "status": status, "output": outputValue})
}

func (s *Service) broadcastBackgroundTaskOutput(parentID, taskID string, outputBytes int) {
	if s.Hub == nil || strings.TrimSpace(parentID) == "" || strings.TrimSpace(taskID) == "" {
		return
	}
	s.Hub.BroadcastNarrator(parentID, map[string]any{"type": "background_task_output", "narratorId": parentID, "taskId": taskID, "outputBytes": outputBytes})
}

func backgroundTaskStoredOutput(output string) (string, int, bool) {
	outputBytes := len([]byte(output))
	if outputBytes <= backgroundTaskMaxOutputBytes {
		return output, outputBytes, false
	}
	limit := 0
	for idx := range output {
		if idx > backgroundTaskMaxOutputBytes {
			break
		}
		limit = idx
	}
	if limit <= 0 {
		limit = backgroundTaskMaxOutputBytes
	}
	return output[:limit], outputBytes, true
}

func backgroundTaskPreview(output string) string {
	if len([]rune(output)) <= backgroundTaskPreviewLength {
		return output
	}
	runes := []rune(output)
	return string(runes[:backgroundTaskPreviewLength]) + "…"
}

func (s *Service) awaitTool(ctx context.Context, parentID string, args map[string]any) (map[string]any, error) {
	kind := strings.TrimSpace(stringFromAny(args["type"]))
	if kind == "" {
		kind = "agent"
	}
	if kind != "agent" {
		return map[string]any{"status": "unsupported", "message": "Go backend Await currently supports type=agent only."}, nil
	}
	target := strings.TrimSpace(stringFromAny(args["id"]))
	if target == "" {
		return map[string]any{"status": "error", "message": "id is required"}, nil
	}
	parentBlocking := anyBool(args["_parent_blocking"])
	timeoutMs := anyInt(args["timeout"], 0)
	if timeoutMs < 0 {
		timeoutMs = 0
	}
	if timeoutMs > 30000 {
		timeoutMs = 30000
	}
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for {
		child, err := s.resolveChildNarrator(ctx, parentID, target)
		if err != nil {
			return map[string]any{"status": "not_found", "id": target}, nil
		}
		if parentBlocking && child.IsBackground && ptrString(child.BackgroundStatus) == "running" {
			payload := subagentStatusPayload(child)
			payload["status"] = "detached"
			payload["detached"] = true
			payload["background"] = true
			payload["backgroundStatus"] = "running"
			payload["result"] = formatBackgroundTaskResult(child.ID, fmt.Sprintf("Subagent detached to background. Use Await({ type: \"agent\", id: \"%s\" }) to get results, or Send({ id: \"%s\", message }) to continue.", firstNonEmptyString(childAlias(child), child.ID), firstNonEmptyString(childAlias(child), child.ID)))
			payload["output"] = payload["result"]
			payload["formatted"] = payload["result"]
			return payload, nil
		}
		if child.IsBackground && ptrString(child.BackgroundStatus) == "running" && !s.runActive(child.ID) {
			if err := s.reconcileStaleBackgroundAgent(ctx, child); err != nil {
				return nil, err
			}
			child, _ = s.resolveChildNarrator(ctx, parentID, target)
		}
		payload := subagentStatusPayload(child)
		if subagentDone(child) || timeoutMs == 0 || time.Now().After(deadline) {
			result := ptrString(child.BackgroundResult)
			if strings.TrimSpace(result) == "" {
				result, _ = s.lastAssistantText(ctx, child.ID)
			}
			if strings.TrimSpace(result) != "" {
				payload["result"] = result
				payload["output"] = result
				payload["formatted"] = formatSubagentResult(child.ID, result)
			}
			if !subagentDone(child) && timeoutMs > 0 {
				payload["timedOut"] = true
				payload["status"] = "timeout"
			}
			return payload, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (s *Service) teamStatusTool(ctx context.Context, narratorID string, args map[string]any) (map[string]any, error) {
	action := strings.TrimSpace(stringFromAny(args["action"]))
	if action == "" {
		action = "list"
	}
	members, err := s.accessibleSubagents(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	if action == "file_changes" {
		return s.teamFileChanges(ctx, members, stringFromAny(args["target_id"]), narratorID)
	}
	if action == "broadcast" {
		return s.teamMessage(ctx, members, narratorID, "", stringFromAny(args["message"]), true)
	}
	if action == "send" {
		return s.teamMessage(ctx, members, narratorID, stringFromAny(args["target_id"]), stringFromAny(args["message"]), false)
	}
	if action != "list" {
		return map[string]any{"action": action, "supported": false, "message": "Go backend TeamStatus currently supports action=list, file_changes, broadcast, and send."}, nil
	}
	items := make([]map[string]any, 0, len(members))
	for _, child := range members {
		items = append(items, subagentStatusPayload(child))
	}
	return map[string]any{"action": action, "subagents": items}, nil
}

func (s *Service) teamMessage(ctx context.Context, members []Narrator, callerID, targetID, message string, broadcast bool) (map[string]any, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return map[string]any{"action": map[bool]string{true: "broadcast", false: "send"}[broadcast], "sent": 0, "error": "message is required"}, nil
	}
	targets := []Narrator{}
	if broadcast {
		for _, member := range members {
			if member.ID != callerID {
				targets = append(targets, member)
			}
		}
	} else {
		if strings.TrimSpace(targetID) == "" {
			return map[string]any{"action": "send", "sent": 0, "error": "target_id is required"}, nil
		}
		if target, ok := matchSubagent(members, targetID); ok {
			targets = append(targets, target)
		} else {
			return map[string]any{"action": "send", "sent": 0, "error": "target subagent not found"}, nil
		}
	}
	items := []map[string]any{}
	for _, target := range targets {
		_, err := s.AddUserMessage(ctx, target.ID, "agent", message)
		if err != nil {
			return nil, err
		}
		items = append(items, subagentStatusPayload(target))
	}
	action := "send"
	if broadcast {
		action = "broadcast"
	}
	return map[string]any{"action": action, "sent": len(items), "targets": items}, nil
}

func (s *Service) teamFileChanges(ctx context.Context, members []Narrator, targetID string, callerID string) (map[string]any, error) {
	selected := members
	if strings.TrimSpace(targetID) != "" {
		if target, ok := matchSubagent(members, targetID); ok {
			selected = []Narrator{target}
		} else {
			return map[string]any{"action": "file_changes", "supported": true, "changes": []any{}, "message": "target subagent not found"}, nil
		}
	}
	ids := make([]string, 0, len(selected))
	for _, member := range selected {
		ids = append(ids, member.ID)
	}
	if len(ids) == 0 {
		return map[string]any{"action": "file_changes", "supported": true, "changes": []any{}}, nil
	}
	placeholders, args := inPlaceholders(ids)
	rows, err := s.DB.QueryContext(ctx, `SELECT narrator_id, file_path, MAX(created_at) FROM narrator_file_snapshots WHERE narrator_id IN (`+placeholders+`) GROUP BY narrator_id, file_path ORDER BY narrator_id ASC, file_path ASC LIMIT 1000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	filesByNarrator := map[string][]map[string]any{}
	for rows.Next() {
		var narratorID, filePath, createdAt string
		if err := rows.Scan(&narratorID, &filePath, &createdAt); err != nil {
			return nil, err
		}
		filesByNarrator[narratorID] = append(filesByNarrator[narratorID], map[string]any{"path": filePath, "lastChangedAt": createdAt})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	changes := []map[string]any{}
	for _, member := range selected {
		files := filesByNarrator[member.ID]
		if files == nil {
			files = []map[string]any{}
		}
		changes = append(changes, map[string]any{"narratorId": member.ID, "alias": childAlias(member), "title": ptrString(member.Title), "subagentType": ptrString(member.SubagentType), "isSelf": member.ID == callerID, "files": files, "fileCount": len(files)})
	}
	return map[string]any{"action": "file_changes", "supported": true, "changes": changes}, nil
}

func (s *Service) DetachSubagent(ctx context.Context, subagentID string) (map[string]any, error) {
	child, err := s.Get(ctx, subagentID)
	if err != nil {
		return nil, err
	}
	if child.Type != "subagent" && child.Variant != "subagent" {
		return map[string]any{"detached": false, "fallback": true, "error": "Not a subagent"}, nil
	}
	if child.ParentNarratorID == nil || strings.TrimSpace(*child.ParentNarratorID) == "" {
		return map[string]any{"detached": false, "fallback": true, "error": "Subagent has no parent narrator"}, nil
	}
	if !s.runActive(subagentID) && child.Status != "working" {
		return map[string]any{"detached": false, "fallback": true, "error": "Subagent is not running in foreground mode"}, nil
	}
	toolUseID, err := s.subagentParentToolUseID(ctx, subagentID)
	if err != nil {
		return map[string]any{"detached": false, "fallback": true, "error": "Cannot find parent tool_use"}, nil
	}
	parentID := strings.TrimSpace(*child.ParentNarratorID)
	alias, traitsJSON := ensureSubagentAliasTrait(child)
	now := db.NowISO()
	previousTraitsJSON, _ := json.Marshal(child.Traits)
	_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET is_background = 1, background_status = 'running', traits = ?, updated_at = ? WHERE id = ?`, traitsJSON, now, subagentID)
	if err != nil {
		return nil, err
	}
	subagentType := firstNonEmptyString(ptrString(child.SubagentType), normalizeSubagentType(ptrString(child.SubagentType)))
	if subagentType == "" {
		subagentType = "general"
	}
	if err := s.createBackgroundAgentTask(ctx, parentID, child.ID, subagentType, ptrString(child.Title), toolUseID, alias); err != nil {
		_, _ = s.DB.ExecContext(ctx, `UPDATE narrators SET is_background = ?, background_status = ?, traits = ?, updated_at = ? WHERE id = ?`, boolInt(child.IsBackground), nullableTrimmedString(ptrString(child.BackgroundStatus)), string(previousTraitsJSON), now, subagentID)
		return nil, err
	}
	updated, err := s.Get(ctx, subagentID)
	if err != nil {
		return nil, err
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(parentID, map[string]any{"type": "subagent_detached", "narratorId": parentID, "subagentNarratorId": subagentID, "toolUseId": toolUseID})
	}
	payload := subagentStatusPayload(updated)
	payload["detached"] = true
	payload["fallback"] = false
	payload["toolUseId"] = toolUseID
	payload["message"] = fmt.Sprintf("Subagent detached to background. Use Await({ type: \"agent\", id: \"%s\" }) to get results, or Send({ id: \"%s\", message }) to continue.", firstNonEmptyString(alias, subagentID), firstNonEmptyString(alias, subagentID))
	return payload, nil
}

func (s *Service) AttachSubagent(ctx context.Context, subagentID string) (map[string]any, error) {
	child, err := s.Get(ctx, subagentID)
	if err != nil {
		return nil, err
	}
	if child.Type != "subagent" && child.Variant != "subagent" {
		return map[string]any{"attached": false, "fallback": true, "error": "Not a subagent"}, nil
	}
	if !child.IsBackground {
		return map[string]any{"attached": false, "fallback": true, "error": "Subagent is not in background mode"}, nil
	}
	if ptrString(child.BackgroundStatus) == "running" && !s.runActive(subagentID) {
		if err := s.reconcileStaleBackgroundAgent(ctx, child); err != nil {
			return nil, err
		}
		updated, _ := s.Get(ctx, subagentID)
		payload := subagentStatusPayload(updated)
		payload["attached"] = false
		payload["fallback"] = true
		payload["staleRecovered"] = true
		payload["message"] = "Background subagent runtime was not active; marked failed for recovery."
		return payload, nil
	}
	now := db.NowISO()
	traits := removeStringTrait(child.Traits, "background")
	_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET is_background = 0, background_status = NULL, traits = ?, updated_at = ? WHERE id = ?`, traits, now, subagentID)
	if err != nil {
		return nil, err
	}
	updated, err := s.Get(ctx, subagentID)
	if err != nil {
		return nil, err
	}
	if s.Hub != nil && child.ParentNarratorID != nil {
		s.Hub.BroadcastNarrator(*child.ParentNarratorID, map[string]any{"type": "subagent_attached", "narratorId": *child.ParentNarratorID, "subagentNarratorId": subagentID})
	}
	payload := subagentStatusPayload(updated)
	payload["attached"] = true
	payload["fallback"] = false
	payload["parentBlocking"] = true
	payload["message"] = "Subagent attached to foreground. Await or Send(await=true) can block until this running subagent completes."
	return payload, nil
}

func (s *Service) reconcileStaleBackgroundAgent(ctx context.Context, child Narrator) error {
	if !child.IsBackground || ptrString(child.BackgroundStatus) != "running" || child.ParentNarratorID == nil || strings.TrimSpace(*child.ParentNarratorID) == "" || s.runActive(child.ID) {
		return nil
	}
	message := "Background subagent runtime was not active after restart/recovery"
	parentID := strings.TrimSpace(*child.ParentNarratorID)
	now := db.NowISO()
	if _, err := s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '["error"]', background_status = 'failed', background_result = ?, background_completed_at = ?, error_message = ?, updated_at = ? WHERE id = ?`, message, now, message, now, child.ID); err != nil {
		return err
	}
	if err := s.finishBackgroundTaskRecord(ctx, parentID, child.ID, "failed", message); err != nil {
		return err
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(child.ID, map[string]any{"type": "status", "narratorId": child.ID, "status": "idle", "substatus": []string{"error"}, "isBackground": true, "backgroundStatus": "failed"})
	}
	return nil
}

func (s *Service) UpdateSubagentConclusion(ctx context.Context, subagentID string) (map[string]any, error) {
	child, err := s.Get(ctx, subagentID)
	if err != nil {
		return nil, err
	}
	if child.Type != "subagent" && child.Variant != "subagent" {
		return map[string]any{"ok": false, "fallback": true, "error": "Not a subagent"}, nil
	}
	if child.ParentNarratorID == nil || strings.TrimSpace(*child.ParentNarratorID) == "" {
		return map[string]any{"ok": false, "fallback": true, "error": "No parent narrator"}, nil
	}
	toolUseID, err := s.subagentParentToolUseID(ctx, subagentID)
	if err != nil {
		return map[string]any{"ok": false, "fallback": true, "error": "Cannot find parent tool_use"}, nil
	}
	finalText, err := s.lastAssistantText(ctx, subagentID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(finalText) == "" {
		finalText = ptrString(child.BackgroundResult)
	}
	if strings.TrimSpace(finalText) == "" {
		finalText = "(no output)"
	}
	hasError := child.Status == "error" || containsString(child.Substatus, "error") || ptrString(child.BackgroundStatus) == "failed"
	outputJSON, _ := json.Marshal(map[string]any{"output": finalText, "subagentId": subagentID, "updatedConclusion": true, "hasError": hasError})
	status := "success"
	if hasError {
		status = "error"
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET output_json = ?, status = ?, completed_at = COALESCE(completed_at, ?), error_message = ? WHERE narrator_id = ? AND tool_use_id = ?`, string(outputJSON), status, db.NowISO(), nullableTrimmedString(map[bool]string{true: finalText, false: ""}[hasError]), strings.TrimSpace(*child.ParentNarratorID), toolUseID)
	if err != nil {
		return nil, err
	}
	rows, _ := res.RowsAffected()
	manualOverride := containsString(child.Substatus, "manual_override")
	if manualOverride {
		if _, err := s.DB.ExecContext(ctx, `UPDATE narrators SET substatus = '[]', updated_at = ? WHERE id = ?`, db.NowISO(), subagentID); err != nil {
			return nil, fmt.Errorf("clear subagent manual override: %w", err)
		}
	}
	return map[string]any{"ok": rows > 0, "toolUseId": toolUseID, "fallback": rows == 0 || manualOverride, "manualOverrideResolved": false, "manualOverrideCleared": manualOverride, "message": "Go backend updated the parent tool call conclusion when present; in-memory manual override waiters are not implemented, so any manual override is cleared as a fallback rather than resuming a blocked parent promise."}, nil
}

func (s *Service) subagentParentToolUseID(ctx context.Context, subagentID string) (string, error) {
	var toolUseID sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT parent_tool_use_id FROM narrator_messages WHERE narrator_id = ? AND role = 'user' AND parent_tool_use_id IS NOT NULL ORDER BY created_at ASC LIMIT 1`, subagentID).Scan(&toolUseID)
	if err != nil || !toolUseID.Valid || strings.TrimSpace(toolUseID.String) == "" {
		return "", httpx.NotFound("Parent tool use")
	}
	return toolUseID.String, nil
}

func (s *Service) runActive(narratorID string) bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.activeRuns != nil && s.activeRuns[narratorID] != nil
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func (s *Service) childNarrators(ctx context.Context, parentID string) ([]Narrator, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+narratorSelectColumns+` FROM narrators WHERE parent_narrator_id = ? ORDER BY created_at ASC LIMIT 200`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Narrator{}
	for rows.Next() {
		n, err := scanNarrator(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, n)
	}
	return items, rows.Err()
}

func (s *Service) resolveChildNarrator(ctx context.Context, parentID, idOrAlias string) (Narrator, error) {
	children, err := s.childNarrators(ctx, parentID)
	if err != nil {
		return Narrator{}, err
	}
	idOrAlias = strings.TrimSpace(idOrAlias)
	for _, child := range children {
		if child.ID == idOrAlias || childAlias(child) == idOrAlias {
			return child, nil
		}
	}
	return Narrator{}, httpx.NotFound("Subagent")
}

func childAlias(child Narrator) string {
	for _, trait := range child.Traits {
		if strings.HasPrefix(trait, "agent-alias:") {
			return strings.TrimSpace(strings.TrimPrefix(trait, "agent-alias:"))
		}
	}
	return ""
}

func ensureSubagentAliasTrait(child Narrator) (string, string) {
	alias := childAlias(child)
	traits := append([]string(nil), child.Traits...)
	if strings.TrimSpace(alias) == "" {
		alias = stableSubagentAlias(child)
		traits = append(traits, "agent-alias:"+alias)
	}
	if !containsString(traits, "background") {
		traits = append(traits, "background")
	}
	traitsJSON, _ := json.Marshal(traits)
	return alias, string(traitsJSON)
}

func stableSubagentAlias(child Narrator) string {
	base := strings.ToLower(strings.TrimSpace(ptrString(child.Title)))
	var builder strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		} else if r == '-' || r == '_' || r == ' ' {
			builder.WriteByte('-')
		}
		if builder.Len() >= 24 {
			break
		}
	}
	alias := strings.Trim(builder.String(), "-")
	if alias == "" {
		alias = "agent"
	}
	idPart := child.ID
	if len(idPart) > 8 {
		idPart = idPart[:8]
	}
	return alias + "-" + idPart
}

func formatSubagentResult(subagentID, output string) string {
	if strings.TrimSpace(output) == "" {
		output = "(no output)"
	}
	return "<subagent_id>" + subagentID + "</subagent_id>\n\n" + output
}

func formatBackgroundTaskResult(subagentID, output string) string {
	if strings.TrimSpace(output) == "" {
		output = "(no output)"
	}
	return "<background_task_id>" + subagentID + "</background_task_id>\n\n" + output
}

func subagentStatusPayload(child Narrator) map[string]any {
	payload := map[string]any{
		"id":                 child.ID,
		"title":              ptrString(child.Title),
		"subagentType":       ptrString(child.SubagentType),
		"status":             child.Status,
		"background":         child.IsBackground,
		"backgroundStatus":   ptrString(child.BackgroundStatus),
		"backgroundResult":   ptrString(child.BackgroundResult),
		"errorMessage":       ptrString(child.ErrorMessage),
		"completedAt":        ptrString(child.BackgroundCompletedAt),
		"createdAt":          child.CreatedAt,
		"updatedAt":          child.UpdatedAt,
		"alias":              childAlias(child),
		"messageVersion":     child.MessageVersion,
		"backgroundDone":     subagentDone(child),
		"parentNarratorId":   ptrString(child.ParentNarratorID),
		"subagentNarratorId": child.ID,
	}
	return payload
}

func subagentDone(child Narrator) bool {
	status := ptrString(child.BackgroundStatus)
	return status == "completed" || status == "failed" || status == "error" || status == "cancelled" || child.Status == "idle" || child.Status == "error" || child.Status == "archived"
}

func normalizeSubagentType(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "", "general":
		return "general"
	case "explore", "plan", "review":
		return value
	}
	cleaned := strings.Builder{}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			cleaned.WriteRune(r)
		}
		if cleaned.Len() >= 64 {
			break
		}
	}
	if strings.Trim(cleaned.String(), "-_") == "" {
		return "general"
	}
	return cleaned.String()
}

func subagentSystemPrompt(subagentType, cwd string) string {
	base := "You are a NarraFork subagent. Work on the delegated task only and report a concise conclusion."
	switch subagentType {
	case "explore":
		base += " You are in explore mode: inspect and summarize; avoid modifying files."
	case "plan":
		base += " You are in plan mode: produce an implementation plan before edits."
	case "review":
		base += " You are in review mode: focus on correctness, regressions, security, and maintainability; avoid modifying files."
	default:
		base += " This is a custom subagent type; Go backend stores the custom type but does not load SUBAGENT.md prompts yet, so follow the caller prompt exactly."
	}
	if strings.TrimSpace(cwd) != "" {
		base += " Working directory: " + strings.TrimSpace(cwd)
	}
	return base
}

func resolveSubagentCwd(parentCwd, requested string) (string, error) {
	parentCwd = strings.TrimSpace(parentCwd)
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return parentCwd, nil
	}
	if parentCwd == "" {
		return requested, nil
	}
	candidate := requested
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(parentCwd, candidate)
	}
	absParent, err := filepath.Abs(parentCwd)
	if err != nil {
		return "", err
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absParent, absCandidate)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("workdir must stay within parent narrator cwd")
	}
	return absCandidate, nil
}

func ptrString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func anySliceValue(value any) []any {
	if items, ok := value.([]any); ok {
		return items
	}
	return nil
}

func anyBool(value any) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return false
}

func anyInt(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := strconv.Atoi(typed.String())
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func (s *Service) narratorRunCancelled(ctx context.Context, narratorID string) bool {
	var status sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT background_status FROM narrators WHERE id = ?`, narratorID).Scan(&status); err != nil {
		return false
	}
	return status.Valid && status.String == "cancelled"
}

func (s *Service) executeAskUserQuestion(ctx context.Context, narratorID string, narrator Narrator, draftMessageID *string, call agentbasic.ToolCall) (agentbasic.ToolRun, error) {
	run := agentbasic.ToolRun{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments, Status: "completed", Recorded: true}
	if draftMessageID == nil {
		local := ""
		draftMessageID = &local
	}
	if *draftMessageID == "" {
		messageID, err := s.createAssistantDraft(ctx, narratorID)
		if err != nil {
			return agentbasic.ToolRun{}, err
		}
		*draftMessageID = messageID
	}
	if err := s.recordPendingToolCall(ctx, narratorID, *draftMessageID, narrator, call); err != nil {
		return agentbasic.ToolRun{}, err
	}
	s.scheduleAskUserQuestionReflection(ctx, narratorID, call.ID)
	if err := s.setNarratorRunSubstatus(ctx, narratorID, narrator.IsBackground, []string{"awaiting_permission"}); err != nil {
		return agentbasic.ToolRun{}, err
	}
	decision, err := s.waitForPermissionDecision(ctx, narratorID, call.ID, 5*time.Minute)
	if err != nil {
		return agentbasic.ToolRun{}, err
	}
	if toolDecisionRejected(decision.Status) {
		run.Status = decision.Status
		run.Output = decisionOutput(decision)
		if run.Output == "" {
			run.Output = "Question was cancelled."
		}
		if err := s.finishRecordedToolCall(ctx, narratorID, call.ID, *draftMessageID, run, decision, "fail"); err != nil {
			return agentbasic.ToolRun{}, err
		}
		_ = s.setNarratorRunSubstatus(ctx, narratorID, narrator.IsBackground, []string{"calling_model"})
		return run, nil
	}
	answers, err := s.askUserQuestionAnswers(ctx, narratorID, call.ID)
	if err != nil {
		return agentbasic.ToolRun{}, err
	}
	run.Output = formatAskUserAnswers(answers)
	if err := s.finishRecordedToolCall(ctx, narratorID, call.ID, *draftMessageID, run, decision, "success"); err != nil {
		return agentbasic.ToolRun{}, err
	}
	_ = s.setNarratorRunSubstatus(ctx, narratorID, narrator.IsBackground, []string{"calling_model"})
	return run, nil
}

const maxQuestionReflectionContextChars = 24000

func (s *Service) questionReflectionEnabled() bool {
	if s.Settings == nil {
		return false
	}
	return s.Settings.Core().Agent.QuestionReflectionEnabled
}

func (s *Service) questionReflectionTimeout() time.Duration {
	value := 300000
	if s.Settings != nil {
		value = s.Settings.Core().Agent.QuestionReflectionTimeoutMs
	}
	if value < 10000 {
		value = 10000
	}
	if value > 3600000 {
		value = 3600000
	}
	return time.Duration(value) * time.Millisecond
}

func (s *Service) scheduleAskUserQuestionReflection(ctx context.Context, narratorID, toolUseID string) {
	if !s.questionReflectionEnabled() || !s.askQuestionAutoReflectionAllowed(ctx, narratorID, toolUseID) {
		return
	}
	timeout := s.questionReflectionTimeout()
	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		reflectCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = s.ReflectPendingAskUserQuestion(reflectCtx, narratorID, toolUseID, true)
	}()
}

func (s *Service) askQuestionAutoReflectionAllowed(ctx context.Context, narratorID, toolUseID string) bool {
	if !s.questionReflectionEnabled() {
		return false
	}
	var permissionMode sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT permission_mode FROM narrators WHERE id = ?`, narratorID).Scan(&permissionMode); err != nil {
		return false
	}
	if strings.TrimSpace(permissionMode.String) != "bypassPermissions" {
		return false
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = ? AND tool_name = 'AskUserQuestion' AND status = 'pending' AND permission_decided_at IS NULL`, narratorID, toolUseID).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

func (s *Service) askUserQuestionAnswers(ctx context.Context, narratorID, toolUseID string) (map[string]any, error) {
	var inputRaw sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT input_json FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = ? ORDER BY created_at DESC LIMIT 1`, narratorID, toolUseID).Scan(&inputRaw)
	if err != nil {
		return nil, err
	}
	input := map[string]any{}
	if inputRaw.Valid && strings.TrimSpace(inputRaw.String) != "" {
		_ = json.Unmarshal([]byte(inputRaw.String), &input)
	}
	answers, _ := input["answers"].(map[string]any)
	if answers == nil {
		return map[string]any{}, nil
	}
	return answers, nil
}

func (s *Service) askQuestionReflectionSystemPrompt(mode string) string {
	if mode == "suggest" {
		return "You are a senior software engineering advisor. The user is being asked one or more questions by an AI coding assistant during a conversation. You will receive the conversation context in <conversation> tags and the questions in <questions> tags. For each question, suggest the best-practice answer considering the specific project context and conversation history. If options are provided, pick from them; otherwise give a concise free-text answer. Reply with ONLY a valid JSON object mapping each question key to your recommended answer string. No explanation, no markdown fences."
	}
	return "You are NarraFork's question reflection gate. An AI coding assistant asked the user one or more questions, but the session is allowed to proceed automatically. Use the conversation context and project intent to answer as a careful user would: choose the safest practical default, prefer the assistant's recommended option when it is reasonable, and avoid adding new requirements. If options are provided, pick option labels exactly; for multi-select questions, return comma-separated option labels. For free-text questions, give a concise answer. Reply with ONLY a valid JSON object mapping each question key to your answer string. No explanation, no markdown fences."
}

func parseAskQuestions(raw any) []askQuestionInput {
	if text, ok := raw.(string); ok {
		var decoded any
		if err := json.Unmarshal([]byte(text), &decoded); err != nil {
			return nil
		}
		raw = decoded
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	questions := []askQuestionInput{}
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		question := strings.TrimSpace(stringFromAny(entry["question"]))
		header := strings.TrimSpace(stringFromAny(entry["header"]))
		if question == "" || header == "" {
			continue
		}
		options := []askQuestionOption{}
		for _, optRaw := range anySliceValue(entry["options"]) {
			opt, ok := optRaw.(map[string]any)
			if !ok {
				continue
			}
			label := strings.TrimSpace(stringFromAny(opt["label"]))
			if label == "" {
				continue
			}
			options = append(options, askQuestionOption{Label: label, Description: stringFromAny(opt["description"]), Preview: stringFromAny(opt["preview"])})
		}
		questions = append(questions, askQuestionInput{Question: question, Header: header, Options: options, MultiSelect: anyBool(entry["multiSelect"])})
	}
	return questions
}

func (s *Service) buildQuestionReflectionContext(ctx context.Context, narratorID string) string {
	history, err := s.agentHistory(ctx, narratorID, 80)
	if err != nil {
		return ""
	}
	lines := []string{}
	total := 0
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			continue
		}
		role := "System"
		if msg.Role == "assistant" {
			role = "Assistant"
		} else if msg.Role == "user" {
			role = "User"
		}
		line := "[" + role + "]: " + text
		lines = append(lines, line)
		total += len(line)
		if total >= maxQuestionReflectionContextChars {
			break
		}
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	contextText := strings.Join(lines, "\n\n")
	if len(contextText) > maxQuestionReflectionContextChars {
		contextText = contextText[len(contextText)-maxQuestionReflectionContextChars:]
	}
	return contextText
}

func buildQuestionReflectionQuestionsText(questions []askQuestionInput) string {
	parts := []string{}
	for _, q := range questions {
		optionText := "\n(free-text, no predefined options)"
		if len(q.Options) > 0 {
			opts := []string{}
			for _, option := range q.Options {
				opts = append(opts, option.Label+" — "+option.Description)
			}
			optionText = "\nOptions: " + strings.Join(opts, "; ")
		}
		parts = append(parts, "Key: \""+q.Question+"\"\nQuestion: "+q.Header+optionText)
	}
	return strings.Join(parts, "\n\n")
}

func parseQuestionReflectionJSON(raw string) map[string]any {
	cleaned := strings.TrimSpace(strings.ReplaceAll(raw, "```json", "```"))
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)
	out := map[string]any{}
	_ = json.Unmarshal([]byte(cleaned), &out)
	return out
}

func normalizeQuestionReflectionAnswer(question askQuestionInput, raw any) string {
	labels := []string{}
	for _, option := range question.Options {
		labels = append(labels, option.Label)
	}
	fallback := "No special preference; please use your best judgment."
	if len(labels) > 0 {
		fallback = labels[0]
	}
	parts := []string{}
	switch value := raw.(type) {
	case []any:
		for _, item := range value {
			parts = append(parts, splitAnswerParts(fmt.Sprint(item))...)
		}
	case nil:
		return fallback
	default:
		parts = append(parts, splitAnswerParts(fmt.Sprint(value))...)
	}
	if len(parts) == 0 {
		return fallback
	}
	if len(labels) == 0 {
		return strings.Join(parts, ", ")
	}
	labelSet := map[string]bool{}
	for _, label := range labels {
		labelSet[label] = true
	}
	if question.MultiSelect {
		matched := []string{}
		for _, part := range parts {
			if labelSet[part] {
				matched = append(matched, part)
			}
		}
		if len(matched) > 0 {
			return strings.Join(matched, ", ")
		}
		return strings.Join(parts, ", ")
	}
	for _, part := range parts {
		if labelSet[part] {
			return part
		}
	}
	return parts[0]
}

func splitAnswerParts(value string) []string {
	out := []string{}
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (s *Service) GenerateAskUserQuestionAnswers(ctx context.Context, narratorID string, questions []askQuestionInput, mode string) (map[string]string, error) {
	if len(questions) == 0 {
		return map[string]string{}, nil
	}
	narrator, err := s.Get(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	model := ptrString(narrator.Model)
	if model == "" && s.Settings != nil {
		model = s.Settings.Core().Agent.DefaultModel
	}
	contextText := s.buildQuestionReflectionContext(ctx, narratorID)
	questionsText := buildQuestionReflectionQuestionsText(questions)
	userMessage := questionsText
	if strings.TrimSpace(contextText) != "" {
		userMessage = "<conversation>\n" + contextText + "\n</conversation>\n\n<questions>\n" + questionsText + "\n</questions>"
	}
	result, err := agentbasic.Generate(ctx, s.Settings, model, []agentbasic.Message{{Role: "user", Content: userMessage}}, "", agentbasic.WithSystemPrompt(s.askQuestionReflectionSystemPrompt(mode)), agentbasic.WithReasoningEffort("none"), agentbasic.WithMaxTurns(1), agentbasic.WithToolScope("reflection"), agentbasic.WithDisabledTools([]string{"DangerConfirm", "DangerCancel", "ExitPlanConfirm", "ExitPlanRevise", "GoalCompleteConfirm", "GoalCompleteRevise"}))
	if err != nil {
		return nil, err
	}
	parsed := parseQuestionReflectionJSON(result.Text)
	answers := map[string]string{}
	for _, question := range questions {
		answers[question.Question] = normalizeQuestionReflectionAnswer(question, parsed[question.Question])
	}
	return answers, nil
}

func questionReflectionSuggestions(status, requestID, reason string) []map[string]any {
	suggestion := map[string]any{"type": "question_reflection", "status": status, "requestId": requestID}
	if status != "running" {
		suggestion["resolvedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if strings.TrimSpace(reason) != "" {
		suggestion["reason"] = strings.TrimSpace(reason)
	}
	return []map[string]any{suggestion}
}

func (s *Service) markQuestionReflectionStatus(ctx context.Context, narratorID, requestID, toolUseID, status, reason string, input any) {
	suggestionsJSON, _ := json.Marshal(questionReflectionSuggestions(status, requestID, reason))
	_, _ = s.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET permission_suggestions = ?, permission_decision_reason = COALESCE(?, permission_decision_reason) WHERE id = ?`, string(suggestionsJSON), nullableTrimmedString(reason), requestID)
	if s.Hub == nil {
		return
	}
	if status == "running" {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "question_reflection_started", "narratorId": narratorID, "requestId": requestID, "toolUseId": toolUseID, "toolName": "AskUserQuestion", "inputJson": input, "reason": strings.TrimSpace(reason)})
		return
	}
	decision := "deny"
	if status == "confirmed" {
		decision = "allow"
	} else if status == "awaiting_user" || status == "aborted" {
		decision = "aborted"
	}
	s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "question_reflection_resolved", "narratorId": narratorID, "requestId": requestID, "toolUseId": toolUseID, "decision": decision, "reason": strings.TrimSpace(reason)})
}

func (s *Service) pendingAskQuestionInput(ctx context.Context, narratorID, toolUseID string) (string, string, map[string]any, bool, error) {
	var requestID, inputRaw string
	err := s.DB.QueryRowContext(ctx, `SELECT id, input_json FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = ? AND tool_name = 'AskUserQuestion' AND status = 'pending' AND permission_decided_at IS NULL ORDER BY created_at DESC LIMIT 1`, narratorID, toolUseID).Scan(&requestID, &inputRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", nil, false, nil
		}
		return "", "", nil, false, err
	}
	input := map[string]any{}
	if strings.TrimSpace(inputRaw) != "" {
		_ = json.Unmarshal([]byte(inputRaw), &input)
	}
	return requestID, inputRaw, input, true, nil
}

func mergeAnswerStringsIntoInput(input map[string]any, answers map[string]string) map[string]any {
	merged := map[string]any{}
	for key, value := range input {
		merged[key] = value
	}
	answerMap := map[string]any{}
	for key, value := range answers {
		answerMap[key] = value
	}
	merged["answers"] = answerMap
	return merged
}

func (s *Service) ReflectPendingAskUserQuestion(ctx context.Context, narratorID, toolUseID string, automatic bool) (map[string]string, error) {
	if automatic && !s.askQuestionAutoReflectionAllowed(ctx, narratorID, toolUseID) {
		return nil, errors.New("question reflection is not enabled for this permission mode")
	}
	requestID, _, input, ok, err := s.pendingAskQuestionInput(ctx, narratorID, toolUseID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, sql.ErrNoRows
	}
	questions := parseAskQuestions(input["questions"])
	if len(questions) == 0 {
		return nil, errors.New("no AskUserQuestion questions found")
	}
	s.markQuestionReflectionStatus(ctx, narratorID, requestID, toolUseID, "running", "Question reflection is answering AskUserQuestion", input)
	answers, err := s.GenerateAskUserQuestionAnswers(ctx, narratorID, questions, "reflection")
	if err != nil {
		s.markQuestionReflectionStatus(ctx, narratorID, requestID, toolUseID, "awaiting_user", err.Error(), input)
		return nil, err
	}
	if automatic && !s.askQuestionAutoReflectionAllowed(ctx, narratorID, toolUseID) {
		return answers, errors.New("question was already answered")
	}
	mergedInput := mergeAnswerStringsIntoInput(input, answers)
	inputJSON, _ := json.Marshal(mergedInput)
	now := db.NowISO()
	result, err := s.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET input_json = ?, status = 'running', permission_decided_by = 'reflection', permission_decided_at = ?, permission_decision_reason = ?, permission_suggestions = ? WHERE id = ? AND status = 'pending' AND permission_decided_at IS NULL`, string(inputJSON), now, "Question reflection answered automatically", mustJSON(questionReflectionSuggestions("confirmed", requestID, "Question reflection answered automatically")), requestID)
	if err != nil {
		return nil, err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return answers, errors.New("question was already answered")
	}
	s.markQuestionReflectionStatus(ctx, narratorID, requestID, toolUseID, "confirmed", "Question reflection answered automatically", mergedInput)
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "permission_resolved", "narratorId": narratorID, "requestId": requestID, "toolUseId": toolUseID, "updatedInput": mergedInput, "decision": "allow"})
	}
	return answers, nil
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func formatAskUserAnswers(answers map[string]any) string {
	if len(answers) == 0 {
		return "No answers were provided by the user."
	}
	keys := make([]string, 0, len(answers))
	for key := range answers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := []string{"User answered:"}
	for _, key := range keys {
		switch value := answers[key].(type) {
		case []any:
			parts := []string{}
			for _, item := range value {
				parts = append(parts, fmt.Sprint(item))
			}
			lines = append(lines, "- "+key+": "+strings.Join(parts, ", "))
		default:
			lines = append(lines, "- "+key+": "+fmt.Sprint(value))
		}
	}
	return strings.Join(lines, "\n")
}

func (s *Service) skillRootForNarrator(ctx context.Context, narratorID string) (string, error) {
	var gitPath, cwd sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT p.git_path, n.cwd FROM narrators n LEFT JOIN chapters c ON c.id = n.chapter_id LEFT JOIN projects p ON p.id = c.project_id WHERE n.id = ?`, narratorID).Scan(&gitPath, &cwd)
	if err != nil {
		return "", err
	}
	if gitPath.Valid && strings.TrimSpace(gitPath.String) != "" {
		return gitPath.String, nil
	}
	if cwd.Valid && strings.TrimSpace(cwd.String) != "" {
		if root := findGitRoot(strings.TrimSpace(cwd.String)); root != "" {
			return root, nil
		}
		return strings.TrimSpace(cwd.String), nil
	}
	return "", nil
}

func findGitRoot(startDir string) string {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func (s *Service) updateTodos(ctx context.Context, narratorID, toolUseID string, rawTodos any) (string, error) {
	todosRaw, ok := rawTodos.([]any)
	if !ok {
		return "todos must be an array", nil
	}
	todos := []map[string]any{}
	counts := map[string]int{"pending": 0, "in_progress": 0, "completed": 0}
	for _, item := range todosRaw {
		m, ok := item.(map[string]any)
		if !ok {
			return "each todo must be an object", nil
		}
		idValue := strings.TrimSpace(stringFromAny(m["id"]))
		content := strings.TrimSpace(stringFromAny(m["content"]))
		status := strings.TrimSpace(stringFromAny(m["status"]))
		priority := strings.TrimSpace(stringFromAny(m["priority"]))
		if idValue == "" || content == "" {
			return "each todo requires id and content", nil
		}
		if status != "pending" && status != "in_progress" && status != "completed" {
			return "todo status must be pending, in_progress, or completed", nil
		}
		if priority == "" {
			priority = "medium"
		}
		if priority != "high" && priority != "medium" && priority != "low" {
			return "todo priority must be high, medium, or low", nil
		}
		counts[status]++
		todos = append(todos, map[string]any{"id": idValue, "content": content, "status": status, "priority": priority})
	}
	if strings.TrimSpace(toolUseID) == "" {
		toolUseID = id.New()
	}
	data, err := json.Marshal(todos)
	if err != nil {
		return "", err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET todos_json = ?, todos_tool_use_id = ?, updated_at = ? WHERE id = ?`, string(data), toolUseID, db.NowISO(), narratorID)
	if err != nil {
		return "", err
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "todos_updated", "narratorId": narratorID, "todos": todos, "toolUseId": toolUseID})
	}
	return fmt.Sprintf("Todos updated: %d total, %d completed, %d in progress, %d pending.", len(todos), counts["completed"], counts["in_progress"], counts["pending"]), nil
}

type reflectionDecision struct {
	Action     string
	Evidence   string
	Reflection string
	Feedback   string
	NextSteps  string
	ToolName   string
}

type goalReflectionContext struct {
	RequestID         string
	ToolUseID         string
	ToolName          string
	InputJSON         map[string]any
	ActiveGoal        map[string]any
	BroadcastTargetID string
	StartedAt         time.Time
}

func (meta *goalReflectionContext) ensure(narratorID string) {
	if meta == nil {
		return
	}
	if meta.StartedAt.IsZero() {
		meta.StartedAt = time.Now()
	}
	if strings.TrimSpace(meta.ToolName) == "" {
		meta.ToolName = "UpdateGoal"
	}
	if meta.InputJSON == nil {
		meta.InputJSON = map[string]any{}
	}
	if strings.TrimSpace(meta.BroadcastTargetID) == "" {
		meta.BroadcastTargetID = narratorID
	}
	if strings.TrimSpace(meta.RequestID) == "" {
		if strings.TrimSpace(meta.ToolUseID) != "" {
			meta.RequestID = "goal_complete_" + strings.TrimSpace(meta.ToolUseID)
		} else {
			meta.RequestID = "goal_complete_" + id.Must(8)
		}
	}
}

func goalReflectionStatusReason(status, reason, nextSteps string) string {
	if strings.TrimSpace(reason) != "" && strings.TrimSpace(nextSteps) != "" {
		return strings.TrimSpace(reason) + "\n\nNext steps: " + strings.TrimSpace(nextSteps)
	}
	if strings.TrimSpace(reason) != "" {
		return strings.TrimSpace(reason)
	}
	switch status {
	case "running":
		return "Goal completion reflection is checking the active goal"
	case "confirmed":
		return "Goal completion reflection confirmed the active goal"
	case "cancelled":
		return "Goal completion reflection requested more work"
	case "aborted":
		return "Goal completion reflection aborted"
	default:
		return "Goal completion reflection updated"
	}
}

func goalReflectionSuggestions(meta *goalReflectionContext, status, reason, nextSteps string) []map[string]any {
	meta.ensure("")
	suggestion := map[string]any{
		"type":      "goal_reflection",
		"status":    status,
		"requestId": meta.RequestID,
		"startedAt": meta.StartedAt.UTC().Format(time.RFC3339Nano),
	}
	if meta.ActiveGoal != nil {
		suggestion["activeGoal"] = meta.ActiveGoal
	}
	if status == "confirmed" || status == "cancelled" || status == "aborted" {
		suggestion["resolvedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if strings.TrimSpace(reason) != "" {
		suggestion["reason"] = strings.TrimSpace(reason)
	}
	if strings.TrimSpace(nextSteps) != "" {
		suggestion["nextSteps"] = strings.TrimSpace(nextSteps)
	}
	return []map[string]any{suggestion}
}

func (s *Service) markGoalReflectionStatus(ctx context.Context, narratorID string, meta *goalReflectionContext, status, reason, nextSteps string) {
	if meta == nil {
		return
	}
	meta.ensure(narratorID)
	message := goalReflectionStatusReason(status, reason, nextSteps)
	suggestions := goalReflectionSuggestions(meta, status, reason, nextSteps)
	inputJSON, _ := json.Marshal(meta.InputJSON)
	suggestionsJSON, _ := json.Marshal(suggestions)
	dbStatus := "fail"
	switch status {
	case "running":
		dbStatus = "pending"
	case "confirmed":
		dbStatus = "running"
	}
	startedAt := any(nil)
	if status == "running" {
		startedAt = db.NowISO()
	}
	decidedAt := any(nil)
	if status != "running" {
		decidedAt = db.NowISO()
	}
	errorMessage := any(nil)
	if status == "cancelled" || status == "aborted" {
		errorMessage = message
	}
	if strings.TrimSpace(meta.ToolUseID) != "" {
		if toolCallID, err := s.recordedToolCallID(ctx, narratorID, meta.ToolUseID); err == nil && toolCallID.Valid {
			_, _ = s.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET status = ?, input_json = ?, permission_started_at = COALESCE(permission_started_at, ?), permission_decision_reason = ?, permission_suggestions = ?, permission_decided_at = COALESCE(?, permission_decided_at), error_message = ? WHERE id = ?`, dbStatus, string(inputJSON), startedAt, message, string(suggestionsJSON), decidedAt, errorMessage, toolCallID.String)
		}
	}
	if s.Hub == nil {
		return
	}
	if status == "running" {
		s.Hub.BroadcastNarrator(meta.BroadcastTargetID, map[string]any{"type": "goal_reflection_started", "narratorId": meta.BroadcastTargetID, "requestId": meta.RequestID, "toolUseId": meta.ToolUseID, "toolName": meta.ToolName, "inputJson": meta.InputJSON, "activeGoal": meta.ActiveGoal, "reason": message})
		return
	}
	decision := "deny"
	if status == "confirmed" {
		decision = "allow"
	} else if status == "aborted" {
		decision = "aborted"
	}
	s.Hub.BroadcastNarrator(meta.BroadcastTargetID, map[string]any{"type": "goal_reflection_resolved", "narratorId": meta.BroadcastTargetID, "requestId": meta.RequestID, "toolUseId": meta.ToolUseID, "decision": decision, "reason": strings.TrimSpace(reason), "nextSteps": strings.TrimSpace(nextSteps)})
}

func (s *Service) runGoalCompletionReflection(ctx context.Context, narratorID, goalID, evidence, reflection string, meta *goalReflectionContext) (reflectionDecision, error) {
	goals, err := s.loadGoals(ctx, narratorID)
	if err != nil {
		return reflectionDecision{}, err
	}
	goal := findGoalByID(goals, goalID)
	if goal == nil {
		return reflectionDecision{}, errors.New("goal not found")
	}
	if meta != nil {
		meta.ActiveGoal = goal
		s.markGoalReflectionStatus(ctx, narratorID, meta, "running", "", "")
	}
	prompt := fmt.Sprintf(`Review whether this goal is truly complete.

Goal:
%s

Evidence provided by the agent:
%s

Agent reflection:
%s

If every material requirement is satisfied and no required work remains, call GoalCompleteConfirm with confirm=true and concrete evidence. If anything is missing or uncertain, call GoalCompleteRevise with confirm=true, feedback explaining why completion is blocked, and nextSteps describing the concrete next action before trying UpdateGoal again.`, stringFromAny(goal["objective"]), evidence, reflection)
	return s.runReflection(ctx, narratorID, prompt, map[string]bool{"GoalCompleteConfirm": true, "GoalCompleteRevise": true})
}

func (s *Service) runExitPlanReflection(ctx context.Context, narratorID, plan string, allowedPrompts any) (reflectionDecision, error) {
	allowedJSON, _ := json.MarshalIndent(allowedPrompts, "", "  ")
	prompt := fmt.Sprintf(`Review this ExitPlanMode plan before it is submitted for user approval.

Plan:
%s

Allowed command prompt categories:
%s

If the plan is concrete, actionable, and has no obvious gaps, call ExitPlanConfirm with confirm=true. If it needs more detail, has unresolved decisions, or should be revised first, call ExitPlanRevise with confirm=true and actionable feedback.`, plan, string(allowedJSON))
	return s.runReflection(ctx, narratorID, prompt, map[string]bool{"ExitPlanConfirm": true, "ExitPlanRevise": true})
}

func shouldRunDangerReflection(settings *config.Store, narrator Narrator) bool {
	switch strings.ToLower(strings.TrimSpace(narrator.DangerReflectionOverride)) {
	case "on":
		return true
	case "off":
		return false
	}
	if settings == nil {
		return false
	}
	agent, _ := settings.Raw()["agent"].(map[string]any)
	return anyBool(agent["dangerReflectionEnabled"])
}

func shouldRunDangerReflectionBeforeExecution(settings *config.Store, narrator Narrator, permission toolPermissionDecision, call agentbasic.ToolCall) bool {
	if !shouldRunDangerReflection(settings, narrator) {
		return false
	}
	if permission.Action == "ask" {
		return true
	}
	if permission.Action != "allow" {
		return false
	}
	mode := normalizePermissionMode(ptrString(narrator.PermissionMode), "default")
	if mode != "bypassPermissions" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(call.Function.Name)) {
	case "bash", "write", "edit":
		return true
	default:
		return false
	}
}

func (s *Service) runDangerReflection(ctx context.Context, narratorID string, call agentbasic.ToolCall) (reflectionDecision, error) {
	prompt := fmt.Sprintf(`Review this potentially dangerous tool call before it is executed.

Tool: %s
Input JSON:
%s

If this tool call is acceptable, call DangerConfirm with confirm=true. If it should not run, call DangerCancel with confirm=true and actionable feedback.`, call.Function.Name, call.Function.Arguments)
	return s.runReflection(ctx, narratorID, prompt, map[string]bool{"DangerConfirm": true, "DangerCancel": true})
}

func (s *Service) runReflection(ctx context.Context, narratorID, prompt string, allowed map[string]bool) (reflectionDecision, error) {
	narrator, err := s.Get(ctx, narratorID)
	if err != nil {
		return reflectionDecision{}, err
	}
	model := ptrString(narrator.Model)
	cwd := ptrString(narrator.Cwd)
	decision := reflectionDecision{}
	executor := func(execCtx context.Context, execCwd string, call agentbasic.ToolCall) (agentbasic.ToolRun, error) {
		run := agentbasic.ToolRun{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments, Status: "completed"}
		if !allowed[call.Function.Name] {
			run.Status = "error"
			run.Output = call.Function.Name + " is not allowed in this reflection loop."
			return run, nil
		}
		parsed, output := reflectionDecisionFromToolCall(call.Function.Name, call.Function.Arguments)
		if parsed.Action == "" {
			run.Status = "error"
			run.Output = output
			return run, nil
		}
		decision = parsed
		run.Output = output
		return run, nil
	}
	stream, err := agentbasic.RunAgentLoop(ctx, s.Settings, model, []agentbasic.Message{{Role: "user", Content: prompt}}, cwd,
		agentbasic.WithToolScope("reflection"),
		agentbasic.WithMaxTurns(3),
		agentbasic.WithToolExecutor(executor),
		agentbasic.WithSystemPrompt("You are a strict NarraFork reflection gate. You must call exactly one allowed reflection tool; do not answer in plain text."),
	)
	if err != nil {
		return reflectionDecision{}, fmt.Errorf("reflection failed: %w", err)
	}
	var loopErr error
	for event := range stream {
		switch event.Type {
		case "error", "retryable_error", "invalid_state":
			loopErr = errors.New(firstNonEmptyString(event.Message, event.Reason, "reflection loop failed"))
		}
	}
	if decision.Action != "" {
		return decision, nil
	}
	if loopErr != nil {
		return reflectionDecision{}, fmt.Errorf("reflection failed: %w", loopErr)
	}
	return reflectionDecision{}, errors.New("reflection did not call a confirm or revise tool")
}

func reflectionDecisionFromToolCall(name, rawArgs string) (reflectionDecision, string) {
	args := map[string]any{}
	_ = json.Unmarshal([]byte(rawArgs), &args)
	if !anyBool(args["confirm"]) {
		return reflectionDecision{}, "confirm=true is required for reflection tools"
	}
	switch name {
	case "GoalCompleteConfirm":
		evidence := strings.TrimSpace(stringFromAny(args["evidence"]))
		if !goalCompletionEvidenceValid(evidence) {
			return reflectionDecision{}, "The evidence parameter must contain concrete completion evidence."
		}
		return reflectionDecision{Action: "confirm", ToolName: name, Evidence: evidence, Reflection: strings.TrimSpace(stringFromAny(args["reflection"]))}, "Goal-completion reflection confirmed."
	case "GoalCompleteRevise":
		feedback := strings.TrimSpace(stringFromAny(args["feedback"]))
		if feedback == "" {
			return reflectionDecision{}, "feedback is required"
		}
		nextSteps := strings.TrimSpace(stringFromAny(args["nextSteps"]))
		if nextSteps == "" {
			return reflectionDecision{}, "nextSteps is required"
		}
		return reflectionDecision{Action: "revise", ToolName: name, Feedback: feedback, NextSteps: nextSteps}, "Goal-completion reflection requested revision."
	case "ExitPlanConfirm":
		return reflectionDecision{Action: "confirm", ToolName: name, Reflection: strings.TrimSpace(stringFromAny(args["reflection"]))}, "ExitPlanMode reflection confirmed."
	case "ExitPlanRevise":
		feedback := strings.TrimSpace(stringFromAny(args["feedback"]))
		if feedback == "" {
			return reflectionDecision{}, "feedback is required"
		}
		return reflectionDecision{Action: "revise", ToolName: name, Feedback: feedback}, "ExitPlanMode reflection requested revision."
	case "DangerConfirm":
		return reflectionDecision{Action: "confirm", ToolName: name, Reflection: strings.TrimSpace(stringFromAny(args["reflection"]))}, "Danger reflection confirmed."
	case "DangerCancel":
		feedback := strings.TrimSpace(firstNonEmptyString(stringFromAny(args["feedback"]), stringFromAny(args["reason"])))
		if feedback == "" {
			feedback = "Danger reflection rejected this tool call."
		}
		return reflectionDecision{Action: "cancel", ToolName: name, Feedback: feedback}, "Danger reflection cancelled the tool call."
	default:
		return reflectionDecision{}, name + " is not supported by this reflection loop"
	}
}

func (s *Service) exitPlanModeTool(ctx context.Context, narratorID string, args map[string]any) (map[string]any, error) {
	narrator, err := s.Get(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	if !narrator.PlanMode {
		return map[string]any{"ok": true, "planMode": false, "message": "Narrator is already outside plan mode."}, nil
	}
	plan, planSource, err := s.resolveExitPlanModePlan(narrator, args)
	if err != nil {
		return map[string]any{"ok": false, "planMode": true, "error": err.Error()}, nil
	}
	if !exitPlanTextValid(plan) {
		planFile := ".narrafork/plan-<id>.md"
		if narrator.PlanFileID != nil && strings.TrimSpace(*narrator.PlanFileID) != "" {
			planFile = planFileRelativePath(strings.TrimSpace(*narrator.PlanFileID))
		}
		return map[string]any{"ok": false, "planMode": true, "error": "ExitPlanMode requires a complete plan of at least 40 characters before leaving plan mode. Provide a non-empty plan parameter or write it to " + planFile + "."}, nil
	}
	decision, err := s.runExitPlanReflection(ctx, narratorID, plan, args["allowedPrompts"])
	if err != nil {
		return map[string]any{"ok": false, "planMode": true, "error": err.Error()}, nil
	}
	if decision.Action != "confirm" {
		return map[string]any{"ok": false, "planMode": true, "feedback": decision.Feedback, "reflectionDecision": decision.Action}, nil
	}
	payload, err := s.setNarratorPlanMode(ctx, narratorID, false)
	if err != nil {
		return nil, err
	}
	payload["plan"] = plan
	payload["planSource"] = planSource
	payload["allowedPrompts"] = args["allowedPrompts"]
	payload["reflectionDecision"] = decision.Action
	if decision.Reflection != "" {
		payload["reflection"] = decision.Reflection
	}
	if msg, err := s.persistPlanCompact(ctx, narratorID, plan); err == nil {
		payload["planCompact"] = map[string]any{"ok": true, "messageId": msg.ID}
		if s.Hub != nil {
			s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "message", "narratorId": narratorID, "message": msg})
		}
	} else {
		payload["planCompact"] = map[string]any{"ok": false, "fallback": "Go backend exited plan mode and restored permissions, but could not persist the TS-style plan compact; future context will rely on the normal message history/compact fallback.", "error": err.Error()}
	}
	return payload, nil
}

func (s *Service) resolveExitPlanModePlan(narrator Narrator, args map[string]any) (string, string, error) {
	cwd := ""
	if narrator.Cwd != nil {
		cwd = *narrator.Cwd
	}
	if narrator.PlanFileID != nil && strings.TrimSpace(*narrator.PlanFileID) != "" {
		path := planFileRelativePath(strings.TrimSpace(*narrator.PlanFileID))
		resolved := path
		if cwd != "" && !filepath.IsAbs(path) {
			resolved = filepath.Join(cwd, path)
		}
		if text, err := readExitPlanFile(resolved); err == nil && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text), "planFile", nil
		} else if err != nil && !os.IsNotExist(err) {
			plan := strings.TrimSpace(stringFromAny(args["plan"]))
			if plan != "" {
				return plan, "argument", nil
			}
			return "", "", err
		}
	}
	plan := strings.TrimSpace(stringFromAny(args["plan"]))
	if plan != "" {
		return plan, "argument", nil
	}
	return "", "", nil
}

func readExitPlanFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("ExitPlanMode plan file is a directory: %s", path)
	}
	if info.Size() > exitPlanFileMaxBytes {
		return "", fmt.Errorf("ExitPlanMode plan file is too large (%d bytes; max %d bytes)", info.Size(), exitPlanFileMaxBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, exitPlanFileMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > exitPlanFileMaxBytes {
		return "", fmt.Errorf("ExitPlanMode plan file is too large (>%d bytes)", exitPlanFileMaxBytes)
	}
	return string(data), nil
}

func (s *Service) persistPlanCompact(ctx context.Context, narratorID, plan string) (Message, error) {
	content := []map[string]any{{"type": "compact", "status": "compacted", "subtype": "plan", "summary": plan}}
	contentText := "[Plan] " + plan
	if len(contentText) > 220 {
		contentText = contentText[:220] + "..."
	}
	msg, err := s.addMessageWithBlocks(ctx, narratorID, "system", contentText, content, nil)
	if err != nil {
		return Message{}, err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE narrator_message_refs SET is_compact = 1 WHERE narrator_id = ? AND message_id = ?`, narratorID, msg.ID)
	if err != nil {
		return Message{}, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE narrators SET context_summary = ?, api_conversation_id = NULL, updated_at = ? WHERE id = ?`, plan, db.NowISO(), narratorID); err != nil {
		return Message{}, err
	}
	return msg, nil
}

func exitPlanTextValid(plan string) bool {
	return len(strings.TrimSpace(plan)) >= 40
}

func (s *Service) setNarratorPlanMode(ctx context.Context, narratorID string, enabled bool) (map[string]any, error) {
	narrator, err := s.Get(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	traits := removeStringTrait(narrator.Traits, "plan")
	now := db.NowISO()
	planFileID := ""
	previousPermission := "default"
	if narrator.PermissionMode != nil && strings.TrimSpace(*narrator.PermissionMode) != "" {
		previousPermission = strings.TrimSpace(*narrator.PermissionMode)
	}
	if narrator.PreviousPermissionMode != nil && strings.TrimSpace(*narrator.PreviousPermissionMode) != "" {
		previousPermission = strings.TrimSpace(*narrator.PreviousPermissionMode)
	}
	if enabled {
		traits = addStringTrait(narrator.Traits, "plan")
		if narrator.PlanFileID != nil {
			planFileID = strings.TrimSpace(*narrator.PlanFileID)
		}
		if planFileID == "" {
			planFileID = id.Must(8)
		}
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET plan_mode = 1, permission_mode = 'readOnly', previous_permission_mode = ?, plan_file_id = ?, traits = ?, updated_at = ? WHERE id = ?`, previousPermission, planFileID, traits, now, narratorID)
	} else {
		_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET plan_mode = 0, permission_mode = ?, previous_permission_mode = NULL, plan_file_id = NULL, traits = ?, updated_at = ? WHERE id = ?`, previousPermission, traits, now, narratorID)
	}
	if err != nil {
		return nil, err
	}
	updated, err := s.Get(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "plan_mode_changed", "narratorId": narratorID, "planMode": enabled, "traits": updated.Traits})
	}
	payload := map[string]any{"ok": true, "planMode": enabled, "narrator": updated, "traits": updated.Traits}
	if enabled {
		payload["planFileId"] = planFileID
		payload["planFile"] = planFileRelativePath(planFileID)
		payload["previousPermissionMode"] = previousPermission
	}
	return payload, nil
}

func (s *Service) loadGoals(ctx context.Context, narratorID string) ([]map[string]any, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, narrator_id, objective, status, sort_order, tokens_used, time_used_seconds, created_by, completed_at, created_at, updated_at FROM narrator_goals WHERE narrator_id = ? ORDER BY sort_order ASC, created_at ASC`, narratorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var idValue, narratorIDValue, objective, status, createdAt, updatedAt string
		var sortOrder, tokensUsed, timeUsedSeconds int
		var createdBy, completedAt sql.NullString
		if err := rows.Scan(&idValue, &narratorIDValue, &objective, &status, &sortOrder, &tokensUsed, &timeUsedSeconds, &createdBy, &completedAt, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": idValue, "narratorId": narratorIDValue, "objective": objective, "status": status, "sortOrder": sortOrder, "tokensUsed": tokensUsed, "timeUsedSeconds": timeUsedSeconds, "createdBy": sqlNullString(createdBy), "completedAt": sqlNullString(completedAt), "createdAt": createdAt, "updatedAt": updatedAt})
	}
	return items, rows.Err()
}

func (s *Service) broadcastGoalsSet(narratorID string, goals []map[string]any) {
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "goals_set", "narratorId": narratorID, "goals": goals})
	}
}

func (s *Service) addGoal(ctx context.Context, narratorID, objective, createdBy string) (map[string]any, error) {
	var sortOrder int
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order), -1) + 1 FROM narrator_goals WHERE narrator_id = ?`, narratorID).Scan(&sortOrder); err != nil {
		return nil, fmt.Errorf("query next goal sort order: %w", err)
	}
	status := "pending"
	var activeOrPaused int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM narrator_goals WHERE narrator_id = ? AND status IN ('active', 'paused')`, narratorID).Scan(&activeOrPaused); err != nil {
		return nil, fmt.Errorf("query active narrator goals: %w", err)
	}
	if activeOrPaused == 0 {
		status = "active"
	}
	goalID := id.New()
	now := db.NowISO()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO narrator_goals (id, narrator_id, objective, status, sort_order, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, goalID, narratorID, objective, status, sortOrder, nullableTrimmedString(createdBy), now, now)
	if err != nil {
		return nil, err
	}
	goals, err := s.loadGoals(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	s.broadcastGoalsSet(narratorID, goals)
	return map[string]any{"goal": findGoalByID(goals, goalID), "goals": goals, "created": true}, nil
}

func (s *Service) updateGoal(ctx context.Context, narratorID, goalID, status, objective, evidence, reflection string) (map[string]any, error) {
	return s.updateGoalWithReflectionContext(ctx, narratorID, goalID, status, objective, evidence, reflection, nil)
}

func (s *Service) updateGoalWithReflectionContext(ctx context.Context, narratorID, goalID, status, objective, evidence, reflection string, reflectionMeta *goalReflectionContext) (map[string]any, error) {
	sets := []string{}
	args := []any{}
	if strings.TrimSpace(objective) != "" {
		sets = append(sets, "objective = ?")
		args = append(args, strings.TrimSpace(objective))
	}
	completionEvidence := ""
	completionReflection := strings.TrimSpace(reflection)
	if strings.TrimSpace(status) != "" {
		status = normalizeGoalStatus(status)
		if status == "" {
			return map[string]any{"error": "status must be pending, active, paused, complete, or cancelled"}, nil
		}
		if status == "complete" {
			completionEvidence = strings.TrimSpace(evidence)
			if !goalCompletionEvidenceValid(completionEvidence) {
				return map[string]any{"error": "Completing a goal requires concrete evidence of at least 20 characters."}, nil
			}
			decision, err := s.runGoalCompletionReflection(ctx, narratorID, goalID, completionEvidence, completionReflection, reflectionMeta)
			if err != nil {
				s.markGoalReflectionStatus(ctx, narratorID, reflectionMeta, "aborted", err.Error(), "")
				return map[string]any{"error": err.Error()}, nil
			}
			if decision.Action != "confirm" {
				s.markGoalReflectionStatus(ctx, narratorID, reflectionMeta, "cancelled", decision.Feedback, decision.NextSteps)
				return map[string]any{"error": "Goal completion reflection requested more work.", "feedback": decision.Feedback, "nextSteps": decision.NextSteps, "reflectionDecision": decision.Action}, nil
			}
			s.markGoalReflectionStatus(ctx, narratorID, reflectionMeta, "confirmed", firstNonEmptyString(decision.Reflection, decision.Evidence), "")
			if strings.TrimSpace(decision.Evidence) != "" {
				completionEvidence = strings.TrimSpace(decision.Evidence)
			}
			if strings.TrimSpace(decision.Reflection) != "" {
				completionReflection = strings.TrimSpace(decision.Reflection)
			}
		}
		sets = append(sets, "status = ?")
		args = append(args, status)
		if status == "complete" {
			sets = append(sets, "completed_at = ?")
			args = append(args, db.NowISO())
		}
	}
	if len(sets) == 0 {
		goals, err := s.loadGoals(ctx, narratorID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"goal": findGoalByID(goals, goalID), "goals": goals}, nil
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, db.NowISO(), goalID, narratorID)
	res, err := s.DB.ExecContext(ctx, `UPDATE narrator_goals SET `+strings.Join(sets, ", ")+` WHERE id = ? AND narrator_id = ?`, args...)
	if err != nil {
		return nil, err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return map[string]any{"error": "goal not found"}, nil
	}
	goals, err := s.loadGoals(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	s.broadcastGoalsSet(narratorID, goals)
	payload := map[string]any{"goal": findGoalByID(goals, goalID), "goals": goals}
	if completionEvidence != "" {
		payload["completionEvidence"] = completionEvidence
		if completionReflection != "" {
			payload["reflection"] = completionReflection
		}
	}
	return payload, nil
}

func (s *Service) resumeGoal(ctx context.Context, narratorID string) (map[string]any, error) {
	goals, err := s.loadGoals(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	for _, goal := range goals {
		if goal["status"] == "paused" {
			return s.updateGoal(ctx, narratorID, stringFromAny(goal["id"]), "active", "", "", "")
		}
	}
	for _, goal := range goals {
		if goal["status"] == "pending" {
			return s.updateGoal(ctx, narratorID, stringFromAny(goal["id"]), "active", "", "", "")
		}
	}
	return map[string]any{"goal": nil, "goals": goals}, nil
}

func (s *Service) completeActiveGoalBasic(ctx context.Context, narratorID string) (map[string]any, error) {
	goals, err := s.loadGoals(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	var active map[string]any
	for _, goal := range goals {
		if goal["status"] == "active" {
			active = goal
			break
		}
	}
	if active == nil {
		return map[string]any{"completed": nil, "active": nil, "goals": goals}, nil
	}
	now := db.NowISO()
	if _, err := s.DB.ExecContext(ctx, `UPDATE narrator_goals SET status = 'complete', completed_at = ?, updated_at = ? WHERE id = ? AND narrator_id = ?`, now, now, active["id"], narratorID); err != nil {
		return nil, err
	}
	updated, err := s.resumeGoal(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	goals, err = s.loadGoals(ctx, narratorID)
	if err != nil {
		return nil, err
	}
	s.broadcastGoalsSet(narratorID, goals)
	return map[string]any{"completed": active, "active": updated["goal"], "goals": goals}, nil
}

func goalCompletionEvidenceValid(evidence string) bool {
	return len(strings.TrimSpace(evidence)) >= 20
}

func normalizeGoalStatus(status string) string {
	switch strings.TrimSpace(status) {
	case "pending", "active", "paused", "complete", "cancelled":
		return strings.TrimSpace(status)
	case "in_progress":
		return "active"
	case "completed":
		return "complete"
	default:
		return ""
	}
}

func findGoalByID(goals []map[string]any, goalID string) map[string]any {
	for _, goal := range goals {
		if goal["id"] == goalID {
			return goal
		}
	}
	return nil
}

func jsonToolOutput(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *Service) attachToolCalls(ctx context.Context, messages []Message) ([]Message, error) {
	for i := range messages {
		calls, err := s.loadToolCalls(ctx, messages[i].ID)
		if err != nil {
			return nil, err
		}
		messages[i].ToolCalls = calls
	}
	return messages, nil
}

func (s *Service) loadToolCalls(ctx context.Context, messageID string) ([]ToolCallRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, error_message, permission_decision_reason, permission_suggestions, created_at FROM narrator_tool_calls WHERE message_id = ? ORDER BY created_at ASC`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	calls := []ToolCallRecord{}
	for rows.Next() {
		var call ToolCallRecord
		var inputRaw, outputRaw, errorMessage, decisionReason, suggestionsRaw sql.NullString
		if err := rows.Scan(&call.ID, &call.NarratorID, &call.MessageID, &call.ToolUseID, &call.ToolName, &inputRaw, &outputRaw, &call.Status, &errorMessage, &decisionReason, &suggestionsRaw, &call.CreatedAt); err != nil {
			return nil, err
		}
		call.InputJSON = decodeAny(inputRaw)
		call.OutputJSON = decodeAny(outputRaw)
		call.ErrorMessage = nullStringPtr(errorMessage)
		call.PermissionDecisionReason = nullStringPtr(decisionReason)
		call.PermissionSuggestions = decodeAny(suggestionsRaw)
		calls = append(calls, call)
	}
	return calls, rows.Err()
}

func (s *Service) persistToolRun(ctx context.Context, narratorID, messageID string, run agentbasic.ToolRun) error {
	now := db.NowISO()
	status := toolRunDBStatus(run.Status, strings.TrimSpace(run.Error) != "")
	outputJSON, _ := json.Marshal(map[string]any{"output": run.Output})
	toolCallID := id.New()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, output_json, status, execution_started_at, completed_at, result_message_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, toolCallID, narratorID, messageID, run.ID, run.Name, run.Arguments, string(outputJSON), status, now, now, messageID, now)
	if err != nil {
		return err
	}
	return s.persistFileSnapshot(ctx, narratorID, messageID, toolCallID, run)
}

func (s *Service) addAssistantMessage(ctx context.Context, narratorID, text string, runs []agentbasic.ToolRun, draftMessageID string) (Message, error) {
	blocks := []map[string]any{}
	for _, run := range runs {
		var input any = map[string]any{}
		_ = json.Unmarshal([]byte(run.Arguments), &input)
		blocks = append(blocks, map[string]any{"type": "tool_use", "id": run.ID, "name": run.Name, "input": input})
	}
	blocks = append(blocks, map[string]any{"type": "text", "text": text})
	var msg Message
	var err error
	if draftMessageID != "" {
		if err = s.updateAssistantDraft(ctx, narratorID, draftMessageID, text, blocks); err == nil {
			msg, err = s.messageByID(ctx, narratorID, draftMessageID)
		}
	}
	if draftMessageID == "" || err != nil {
		msg, err = s.addMessageWithBlocks(ctx, narratorID, "assistant", text, blocks, nil)
		if err != nil {
			return Message{}, err
		}
	}
	for _, run := range runs {
		if run.Recorded {
			continue
		}
		_ = s.persistToolRun(ctx, narratorID, msg.ID, run)
	}
	msg.ToolCalls, _ = s.loadToolCalls(ctx, msg.ID)
	return msg, nil
}

func (s *Service) addMessage(ctx context.Context, narratorID, role, text string, createdBy *string) (Message, error) {
	content := []map[string]any{{"type": "text", "text": text}}
	return s.addMessageWithBlocks(ctx, narratorID, role, text, content, createdBy)
}

func (s *Service) parentToolUseIDForNewMessage(ctx context.Context, narratorID, explicit string) (string, error) {
	if value := strings.TrimSpace(explicit); value != "" {
		return value, nil
	}
	var typ, variant sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT type, variant FROM narrators WHERE id = ?`, narratorID).Scan(&typ, &variant); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	if !(typ.Valid && typ.String == "subagent") && !(variant.Valid && strings.Contains(variant.String, "subagent")) {
		return "", nil
	}
	var parentToolUseID sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT parent_tool_use_id FROM narrator_messages WHERE narrator_id = ? AND parent_tool_use_id IS NOT NULL AND TRIM(parent_tool_use_id) != '' ORDER BY created_at ASC LIMIT 1`, narratorID).Scan(&parentToolUseID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(sqlNullString(parentToolUseID)), nil
}

func (s *Service) broadcastMessage(ctx context.Context, narratorID string, msg Message) {
	if s.Hub == nil {
		return
	}
	s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "message", "narratorId": narratorID, "message": msg})
	if msg.ParentToolUseID == nil || strings.TrimSpace(*msg.ParentToolUseID) == "" {
		return
	}
	var parentNarratorID sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT parent_narrator_id FROM narrators WHERE id = ?`, msg.NarratorID).Scan(&parentNarratorID); err != nil || !parentNarratorID.Valid || strings.TrimSpace(parentNarratorID.String) == "" || parentNarratorID.String == narratorID {
		return
	}
	s.Hub.BroadcastNarrator(parentNarratorID.String, map[string]any{"type": "message", "narratorId": parentNarratorID.String, "message": msg, "childNarratorId": msg.NarratorID, "parentToolUseId": *msg.ParentToolUseID})
}

func (s *Service) addMessageWithBlocksWithCommand(ctx context.Context, narratorID, role, text string, content any, createdBy *string, commandText string) (Message, error) {
	return s.addMessageWithBlocksWithCommandAndParentToolUse(ctx, narratorID, role, text, content, createdBy, commandText, "")
}

func (s *Service) addMessageWithBlocksWithCommandAndParentToolUse(ctx context.Context, narratorID, role, text string, content any, createdBy *string, commandText string, explicitParentToolUseID string) (Message, error) {
	now := db.NowISO()
	msgID := id.New()
	refID := id.New()
	contentJSON, _ := json.Marshal(content)
	parentToolUseID, err := s.parentToolUseIDForNewMessage(ctx, narratorID, explicitParentToolUseID)
	if err != nil {
		return Message{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO narrator_messages (id, narrator_id, parent_tool_use_id, role, content_json, content_text, command_text, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, msgID, narratorID, nullableTrimmedString(parentToolUseID), role, string(contentJSON), text, nullableTrimmedString(commandText), nullablePtr(createdBy), now); err != nil {
		return Message{}, err
	}
	var seq int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM narrator_message_refs WHERE narrator_id = ?`, narratorID).Scan(&seq); err != nil {
		return Message{}, fmt.Errorf("query next narrator message sequence: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES (?, ?, ?, ?, 0)`, refID, narratorID, msgID, seq); err != nil {
		return Message{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE narrators SET message_count = COALESCE(message_count, 0) + 1, message_version = message_version + 1, last_message_at = ?, updated_at = ? WHERE id = ?`, now, now, narratorID); err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	msg := Message{ID: msgID, NarratorID: narratorID, Role: role, ContentJSON: content, ContentText: &text, CreatedBy: createdBy, CreatedAt: now, Seq: seq, ToolCalls: []ToolCallRecord{}, Children: []Message{}}
	if parentToolUseID != "" {
		msg.ParentToolUseID = stringPtr(parentToolUseID)
	}
	if strings.TrimSpace(commandText) != "" {
		msg.CommandText = stringPtr(strings.TrimSpace(commandText))
	}
	return msg, nil
}

func (s *Service) addMessageWithBlocks(ctx context.Context, narratorID, role, text string, content any, createdBy *string) (Message, error) {
	return s.addMessageWithBlocksAtSeq(ctx, narratorID, role, text, content, createdBy, nil, false)
}

func (s *Service) addMessageWithBlocksAtSeq(ctx context.Context, narratorID, role, text string, content any, createdBy *string, insertSeq *int, isCompact bool) (Message, error) {
	now := db.NowISO()
	msgID := id.New()
	refID := id.New()
	contentJSON, _ := json.Marshal(content)
	parentToolUseID, err := s.parentToolUseIDForNewMessage(ctx, narratorID, "")
	if err != nil {
		return Message{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO narrator_messages (id, narrator_id, parent_tool_use_id, role, content_json, content_text, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, msgID, narratorID, nullableTrimmedString(parentToolUseID), role, string(contentJSON), text, nullablePtr(createdBy), now); err != nil {
		return Message{}, err
	}
	var seq int
	if insertSeq != nil {
		seq = *insertSeq
		if _, err := tx.ExecContext(ctx, `UPDATE narrator_message_refs SET seq = seq + 1 WHERE narrator_id = ? AND seq >= ?`, narratorID, seq); err != nil {
			return Message{}, err
		}
	} else {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM narrator_message_refs WHERE narrator_id = ?`, narratorID).Scan(&seq); err != nil {
			return Message{}, fmt.Errorf("query next narrator message sequence: %w", err)
		}
	}
	compactValue := 0
	if isCompact {
		compactValue = 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES (?, ?, ?, ?, ?)`, refID, narratorID, msgID, seq, compactValue); err != nil {
		return Message{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE narrators SET message_count = COALESCE(message_count, 0) + 1, message_version = message_version + 1, last_message_at = ?, updated_at = ? WHERE id = ?`, now, now, narratorID); err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	msg := Message{ID: msgID, NarratorID: narratorID, Role: role, ContentJSON: content, ContentText: &text, CreatedBy: createdBy, CreatedAt: now, Seq: seq, ToolCalls: []ToolCallRecord{}, Children: []Message{}}
	if parentToolUseID != "" {
		msg.ParentToolUseID = stringPtr(parentToolUseID)
	}
	return msg, nil
}

func (s *Service) updateAssistantDraft(ctx context.Context, narratorID, messageID, text string, blocks []map[string]any) error {
	data, _ := json.Marshal(blocks)
	res, err := s.DB.ExecContext(ctx, `UPDATE narrator_messages SET content_json = ?, content_text = ? WHERE id = ? AND narrator_id = ?`, string(data), text, messageID, narratorID)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return httpx.NotFound("Assistant message")
	}
	return nil
}

func (s *Service) messageByID(ctx context.Context, narratorID, messageID string) (Message, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, narrator_id, sdk_message_uuid, parent_tool_use_id, role, content_json, content_text, model, commit_sha, command_text, created_by, created_at, 0 AS seq FROM narrator_messages WHERE id = ? AND narrator_id = ?`, messageID, narratorID)
	msg, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, httpx.NotFound("Message")
	}
	return msg, err
}

type permissionDecision struct {
	Status    string
	Reason    string
	Message   string
	DecidedBy string
	DecidedAt string
}

func (s *Service) createAssistantDraft(ctx context.Context, narratorID string) (string, error) {
	msg, err := s.addMessageWithBlocks(ctx, narratorID, "assistant", "", []map[string]any{}, nil)
	if err != nil {
		return "", err
	}
	return msg.ID, nil
}

func (s *Service) recordPendingToolCall(ctx context.Context, narratorID, messageID string, narrator Narrator, call agentbasic.ToolCall) error {
	input := any(map[string]any{})
	if err := json.Unmarshal([]byte(call.Function.Arguments), &input); err != nil {
		input = map[string]any{"raw": call.Function.Arguments}
	}
	inputJSON, _ := json.Marshal(input)
	suggestions := []map[string]any{{"type": "awaiting_user", "status": "pending", "toolName": call.Function.Name}}
	suggestionsJSON, _ := json.Marshal(suggestions)
	isBackground := 0
	if narrator.IsBackground {
		isBackground = 1
	}
	requestID := id.New()
	now := db.NowISO()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO narrator_tool_calls (id, narrator_id, message_id, tool_use_id, tool_name, input_json, status, permission_started_at, permission_suggestions, is_background, created_at) VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?)`, requestID, narratorID, messageID, call.ID, call.Function.Name, string(inputJSON), now, string(suggestionsJSON), isBackground, now)
	if err != nil {
		return err
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "permission_request", "narratorId": narratorID, "request": map[string]any{"id": requestID, "toolName": call.Function.Name, "toolUseId": call.ID, "inputJson": input, "decisionReason": "", "suggestions": suggestions}})
	}
	return nil
}

func (s *Service) finishRecordedToolCall(ctx context.Context, narratorID, toolUseID, messageID string, run agentbasic.ToolRun, decision permissionDecision, status string) error {
	now := db.NowISO()
	outputJSON, _ := json.Marshal(map[string]any{"output": run.Output})
	status = toolCallDBStatus(status)
	errMessage := ""
	if status != "success" {
		errMessage = run.Output
		if strings.TrimSpace(errMessage) == "" {
			errMessage = decisionOutput(decision)
		}
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE narrator_tool_calls SET status = ?, output_json = ?, execution_started_at = COALESCE(execution_started_at, ?), completed_at = ?, error_message = ?, permission_decided_by = COALESCE(permission_decided_by, ?), permission_decided_at = COALESCE(permission_decided_at, ?), permission_deny_message = COALESCE(permission_deny_message, ?), permission_decision_reason = COALESCE(permission_decision_reason, ?), result_message_id = ? WHERE narrator_id = ? AND tool_use_id = ?`, status, string(outputJSON), now, now, nullableTrimmedString(errMessage), nullableTrimmedString(decision.DecidedBy), nullableTrimmedString(decision.DecidedAt), nullableTrimmedString(decision.Message), nullableTrimmedString(decision.Reason), messageID, narratorID, toolUseID)
	if err != nil {
		return err
	}
	toolCallID, queryErr := s.recordedToolCallID(ctx, narratorID, toolUseID)
	if queryErr != nil && !errors.Is(queryErr, sql.ErrNoRows) {
		payload := map[string]any{"type": "permission_resolved", "narratorId": narratorID, "requestId": nil, "toolUseId": toolUseID, "decision": map[bool]string{true: "deny", false: "allow"}[status != "success"], "degraded": true, "fallback": true, "fallbacks": []any{"permission_resolved_request_id_query_failed"}, "error": queryErr.Error()}
		if s.Hub != nil {
			s.Hub.BroadcastNarrator(narratorID, payload)
		}
		return fmt.Errorf("query recorded tool call: %w", queryErr)
	}
	if s.Hub != nil {
		s.Hub.BroadcastNarrator(narratorID, map[string]any{"type": "permission_resolved", "narratorId": narratorID, "requestId": sqlNullString(toolCallID), "toolUseId": toolUseID, "decision": map[bool]string{true: "deny", false: "allow"}[status != "success"]})
	}
	return s.persistFileSnapshot(ctx, narratorID, messageID, sqlNullString(toolCallID), run)
}

func (s *Service) recordedToolCallID(ctx context.Context, narratorID, toolUseID string) (sql.NullString, error) {
	var toolCallID sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = ? ORDER BY created_at DESC LIMIT 1`, narratorID, toolUseID).Scan(&toolCallID)
	return toolCallID, err
}

func prepareToolFileChange(cwd string, call agentbasic.ToolCall) *agentbasic.FileChange {
	name := strings.ToLower(strings.TrimSpace(call.Function.Name))
	if name != "write" && name != "edit" {
		return nil
	}
	args := map[string]any{}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return nil
	}
	path := strings.TrimSpace(stringFromAny(args["file_path"]))
	if path == "" {
		return nil
	}
	resolved := resolveToolFilePath(cwd, path)
	before, exists := readToolTextFile(resolved)
	change := &agentbasic.FileChange{FilePath: resolved, Operation: name}
	if exists {
		change.BeforeContent = &before
	}
	return change
}

func completeToolFileChange(change *agentbasic.FileChange) {
	if change == nil || strings.TrimSpace(change.FilePath) == "" {
		return
	}
	after, exists := readToolTextFile(change.FilePath)
	if exists {
		change.AfterContent = &after
	}
}

func (s *Service) persistFileSnapshot(ctx context.Context, narratorID, messageID, toolCallID string, run agentbasic.ToolRun) error {
	change := run.FileChange
	if change == nil || strings.TrimSpace(change.FilePath) == "" {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO narrator_file_snapshots (id, narrator_id, file_path, original_content, created_at) VALUES (?, ?, ?, ?, ?)`, id.New(), narratorID, change.FilePath, nullableStringPtr(change.BeforeContent), db.NowISO())
	return err
}

func resolveToolFilePath(cwd, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	base := strings.TrimSpace(cwd)
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = home
		} else {
			base = "."
		}
	}
	return filepath.Clean(filepath.Join(base, path))
}

func readToolTextFile(path string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err != nil {
		return "", false
	}
	text := string(data)
	if len(text) > 1024*1024 {
		text = text[:1024*1024] + "\n[truncated]"
	}
	return text, true
}

func stringFromAny(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func nullableStringPtr(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func (s *Service) waitForPermissionDecision(ctx context.Context, narratorID, toolUseID string, timeout time.Duration) (permissionDecision, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		decision, ok, err := s.permissionDecisionFromDB(ctx, narratorID, toolUseID)
		if err != nil {
			return permissionDecision{}, err
		}
		if ok {
			return decision, nil
		}
		select {
		case <-ctx.Done():
			return permissionDecision{}, ctx.Err()
		case <-deadline.C:
			return permissionDecision{Status: "cancelled", Message: "Permission request timed out."}, nil
		case <-ticker.C:
		}
	}
}

func (s *Service) permissionDecisionFromDB(ctx context.Context, narratorID, toolUseID string) (permissionDecision, bool, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT status, permission_decision_reason, permission_deny_message, permission_decided_by, permission_decided_at FROM narrator_tool_calls WHERE narrator_id = ? AND tool_use_id = ? ORDER BY created_at DESC LIMIT 1`, narratorID, toolUseID)
	var status, reason, denyMessage, decidedBy, decidedAt sql.NullString
	if err := row.Scan(&status, &reason, &denyMessage, &decidedBy, &decidedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return permissionDecision{}, false, nil
		}
		return permissionDecision{}, false, err
	}
	state := strings.ToLower(strings.TrimSpace(status.String))
	decided := strings.TrimSpace(decidedAt.String) != ""
	if !decided || state == "" || state == "pending" || state == "initializing" {
		return permissionDecision{}, false, nil
	}
	logicalStatus := state
	if state == "running" || state == "success" {
		logicalStatus = "approved"
	} else if state == "fail" {
		logicalStatus = "denied"
	}
	return permissionDecision{Status: logicalStatus, Reason: sqlNullString(reason), Message: sqlNullString(denyMessage), DecidedBy: sqlNullString(decidedBy), DecidedAt: sqlNullString(decidedAt)}, true, nil
}

func (s *Service) setNarratorRunSubstatus(ctx context.Context, narratorID string, isBackground bool, substatus []string) error {
	data, _ := json.Marshal(substatus)
	backgroundStatus := any(nil)
	if isBackground {
		backgroundStatus = "running"
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE narrators SET substatus = ?, background_status = COALESCE(?, background_status), updated_at = ? WHERE id = ?`, string(data), backgroundStatus, db.NowISO(), narratorID)
	if err != nil {
		return err
	}
	if s.Hub != nil {
		payload := map[string]any{"type": "status", "narratorId": narratorID, "status": "working", "substatus": substatus, "isBackground": isBackground}
		if isBackground {
			payload["backgroundStatus"] = "running"
		}
		s.Hub.BroadcastNarrator(narratorID, payload)
	}
	return nil
}

func planModeToolRestriction(cwd string, narrator Narrator, call agentbasic.ToolCall) (bool, string) {
	if !narrator.PlanMode || narrator.RelaxedPlan {
		return false, ""
	}
	name := strings.TrimSpace(call.Function.Name)
	if name == "" {
		return false, ""
	}
	lowerName := strings.ToLower(name)
	if lowerName != "write" && lowerName != "edit" {
		return false, ""
	}
	planFile := ""
	if narrator.PlanFileID != nil {
		planFile = strings.TrimSpace(*narrator.PlanFileID)
	}
	if planFile == "" {
		return true, "[PLAN MODE] Write/Edit is blocked because no designated plan file is available. Call EnterPlanMode again or provide the plan directly to ExitPlanMode."
	}
	args := map[string]any{}
	_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
	filePath := strings.TrimSpace(stringFromAny(args["file_path"]))
	planRelPath := planFileRelativePath(planFile)
	if filePath != "" && toolPathsEqual(cwd, filePath, planRelPath) {
		return false, ""
	}
	return true, fmt.Sprintf("[PLAN MODE] Write/Edit is only allowed to the designated plan file %q. Write your plan there or pass it to ExitPlanMode; implementation changes are blocked until plan mode exits.", planRelPath)
}

func planFileRelativePath(planFileID string) string {
	return filepath.ToSlash(filepath.Join(".narrafork", "plan-"+planFileID+".md"))
}

func toolPathsEqual(cwd, requested, expectedRel string) bool {
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	resolve := func(path string) string {
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return filepath.Clean(path)
		}
		return filepath.Clean(abs)
	}
	return resolve(requested) == resolve(expectedRel)
}

type toolPermissionDecision struct {
	Action string
	Reason string
}

type permissionPathRule struct {
	Path  string
	Level string
}

type permissionCommandRule struct {
	Pattern    string
	DenyPrompt string
}

func toolRequiresApproval(permissionMode *string, toolName string) bool {
	mode := normalizePermissionMode(ptrString(permissionMode), "default")
	name := strings.ToLower(strings.TrimSpace(toolName))
	if mode == "bypassPermissions" || mode == "dontAsk" || mode == "readOnly" {
		return false
	}
	if mode == "acceptEdits" {
		return name == "bash" || name == "terminal"
	}
	return name == "bash" || name == "write" || name == "edit" || name == "terminal"
}

func (s *Service) resolveToolPermission(ctx context.Context, narrator Narrator, cwd string, call agentbasic.ToolCall) (toolPermissionDecision, error) {
	rawName := strings.TrimSpace(call.Function.Name)
	name := strings.ToLower(rawName)
	if name == "" {
		return toolPermissionDecision{Action: "allow"}, nil
	}
	args := map[string]any{}
	_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
	ownerID := narrator.ID
	if narrator.ParentNarratorID != nil && strings.TrimSpace(*narrator.ParentNarratorID) != "" && narrator.Variant != "primary" {
		ownerID = strings.TrimSpace(*narrator.ParentNarratorID)
	}
	whitelistDirs, blacklistDirs, whitelistCmds, blacklistCmds, err := s.loadToolPermissionRules(ctx, narrator, ownerID)
	if err != nil {
		return toolPermissionDecision{}, err
	}
	if s.ExternalTools != nil && strings.HasPrefix(rawName, "mcp__") {
		mode := normalizePermissionMode(ptrString(narrator.PermissionMode), "default")
		if action, reason, handled := s.ExternalTools.ResolveToolPermission(rawName, mode); handled {
			return toolPermissionDecision{Action: firstNonEmptyString(action, "allow"), Reason: reason}, nil
		}
	}
	if name == "terminal" {
		action := strings.ToLower(strings.TrimSpace(stringFromAny(args["action"])))
		if action == "list" || action == "read" {
			return toolPermissionDecision{Action: "allow"}, nil
		}
		mode := normalizePermissionMode(ptrString(narrator.PermissionMode), "default")
		switch mode {
		case "bypassPermissions":
			return toolPermissionDecision{Action: "allow"}, nil
		case "dontAsk":
			return toolPermissionDecision{Action: "deny", Reason: "Permission mode dontAsk denied this terminal tool call."}, nil
		case "readOnly":
			return toolPermissionDecision{Action: "deny", Reason: "Permission mode readOnly denies terminal mutation and input actions."}, nil
		default:
			return toolPermissionDecision{Action: "ask"}, nil
		}
	}
	operation := toolOperation(name)
	paths := toolPermissionPaths(cwd, name, args)
	if reason := blacklistedPathReason(cwd, paths, blacklistDirs, operation); reason != "" {
		return toolPermissionDecision{Action: "deny", Reason: reason}, nil
	}
	if name == "bash" {
		command := strings.TrimSpace(stringFromAny(args["command"]))
		if match := commandBlacklistMatch(command, blacklistCmds); match != nil {
			reason := "Command is blocked by narrator command blacklist: " + match.Pattern
			if strings.TrimSpace(match.DenyPrompt) != "" {
				reason = match.DenyPrompt
			}
			return toolPermissionDecision{Action: "deny", Reason: reason}, nil
		}
		if commandWhitelistCovered(command, whitelistCmds) {
			return toolPermissionDecision{Action: "allow"}, nil
		}
		if len(paths) > 0 && allPathsWhitelisted(cwd, paths, whitelistDirs, "full") {
			return toolPermissionDecision{Action: "allow"}, nil
		}
	}
	if name == "read" || name == "glob" || name == "grep" {
		return toolPermissionDecision{Action: "allow"}, nil
	}
	if (name == "write" || name == "edit") && len(paths) > 0 && allPathsWhitelisted(cwd, paths, whitelistDirs, "readWrite") {
		return toolPermissionDecision{Action: "allow"}, nil
	}
	mode := normalizePermissionMode(ptrString(narrator.PermissionMode), "default")
	switch mode {
	case "bypassPermissions":
		return toolPermissionDecision{Action: "allow"}, nil
	case "dontAsk":
		return toolPermissionDecision{Action: "deny", Reason: "Permission mode dontAsk denied this tool call."}, nil
	case "readOnly":
		if name == "bash" || name == "write" || name == "edit" {
			return toolPermissionDecision{Action: "deny", Reason: "Permission mode readOnly denies write and shell tools."}, nil
		}
		return toolPermissionDecision{Action: "allow"}, nil
	case "acceptEdits":
		if name == "write" || name == "edit" {
			return toolPermissionDecision{Action: "allow"}, nil
		}
		if name == "bash" {
			return toolPermissionDecision{Action: "ask"}, nil
		}
		return toolPermissionDecision{Action: "allow"}, nil
	default:
		if name == "bash" || name == "write" || name == "edit" {
			return toolPermissionDecision{Action: "ask"}, nil
		}
		return toolPermissionDecision{Action: "allow"}, nil
	}
}

func (s *Service) loadToolPermissionRules(ctx context.Context, narrator Narrator, ownerID string) ([]permissionPathRule, []permissionPathRule, []permissionCommandRule, []permissionCommandRule, error) {
	globalWhitelistDirs, globalBlacklistDirs, globalWhitelistCmds, globalBlacklistCmds := s.globalToolPermissionRules()
	projectWhitelistDirs, projectBlacklistDirs, projectWhitelistCmds, projectBlacklistCmds, err := s.projectToolPermissionRules(ctx, narrator, ownerID)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	narratorWhitelistDirs, err := s.loadPathRules(ctx, `SELECT path, access_level FROM narrator_whitelist_dirs WHERE narrator_id = ? AND enabled = 1`, ownerID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	narratorBlacklistDirs, err := s.loadPathRules(ctx, `SELECT path, deny_level FROM narrator_blacklist_dirs WHERE narrator_id = ? AND enabled = 1`, ownerID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	narratorWhitelistCmds, err := s.loadCommandRules(ctx, `SELECT pattern, '' FROM narrator_whitelist_cmds WHERE narrator_id = ? AND enabled = 1`, ownerID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	narratorBlacklistCmds, err := s.loadCommandRules(ctx, `SELECT pattern, COALESCE(deny_prompt, '') FROM narrator_blacklist_cmds WHERE narrator_id = ? AND enabled = 1`, ownerID)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	whitelistDirs := append(append(globalWhitelistDirs, projectWhitelistDirs...), narratorWhitelistDirs...)
	blacklistDirs := append(append(globalBlacklistDirs, projectBlacklistDirs...), narratorBlacklistDirs...)
	whitelistCmds := append(append(globalWhitelistCmds, projectWhitelistCmds...), narratorWhitelistCmds...)
	blacklistCmds := append(append(globalBlacklistCmds, projectBlacklistCmds...), narratorBlacklistCmds...)
	return whitelistDirs, blacklistDirs, whitelistCmds, blacklistCmds, nil
}

func (s *Service) globalToolPermissionRules() ([]permissionPathRule, []permissionPathRule, []permissionCommandRule, []permissionCommandRule) {
	if s == nil || s.Settings == nil {
		return nil, nil, nil, nil
	}
	raw := s.Settings.Raw()
	agent, _ := raw["agent"].(map[string]any)
	return permissionRulesFromConfigMap(agent)
}

func (s *Service) projectToolPermissionRules(ctx context.Context, narrator Narrator, ownerID string) ([]permissionPathRule, []permissionPathRule, []permissionCommandRule, []permissionCommandRule, error) {
	chapterID := ptrString(narrator.ChapterID)
	if strings.TrimSpace(chapterID) == "" && strings.TrimSpace(ownerID) != "" && ownerID != narrator.ID {
		var parentChapter sql.NullString
		err := s.DB.QueryRowContext(ctx, `SELECT chapter_id FROM narrators WHERE id = ?`, ownerID).Scan(&parentChapter)
		if err != nil {
			if err == sql.ErrNoRows || isSQLiteMissingTableError(err) || isSQLiteMissingColumnError(err) {
				return nil, nil, nil, nil, nil
			}
			return nil, nil, nil, nil, err
		}
		if parentChapter.Valid {
			chapterID = parentChapter.String
		}
	}
	chapterID = strings.TrimSpace(chapterID)
	if chapterID == "" {
		return nil, nil, nil, nil, nil
	}
	var rawSettings sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(p.chapter_settings, '') FROM chapters c JOIN projects p ON p.id = c.project_id WHERE c.id = ?`, chapterID).Scan(&rawSettings)
	if err != nil {
		if err == sql.ErrNoRows || isSQLiteMissingTableError(err) || isSQLiteMissingColumnError(err) {
			return nil, nil, nil, nil, nil
		}
		return nil, nil, nil, nil, err
	}
	if !rawSettings.Valid || strings.TrimSpace(rawSettings.String) == "" {
		return nil, nil, nil, nil, nil
	}
	settings := map[string]any{}
	if err := json.Unmarshal([]byte(rawSettings.String), &settings); err != nil {
		return nil, nil, nil, nil, nil
	}
	whitelistDirs, blacklistDirs, whitelistCmds, blacklistCmds := permissionRulesFromConfigMap(settings)
	return whitelistDirs, blacklistDirs, whitelistCmds, blacklistCmds, nil
}

func permissionRulesFromConfigMap(raw map[string]any) ([]permissionPathRule, []permissionPathRule, []permissionCommandRule, []permissionCommandRule) {
	if len(raw) == 0 {
		return nil, nil, nil, nil
	}
	return pathRulesFromConfig(raw["whitelistDirs"], "accessLevel"),
		pathRulesFromConfig(raw["blacklistDirs"], "denyLevel"),
		commandRulesFromConfig(raw["commandWhitelist"], false),
		commandRulesFromConfig(raw["commandBlacklist"], true)
}

func pathRulesFromConfig(value any, levelKey string) []permissionPathRule {
	rules := []permissionPathRule{}
	for _, item := range anySliceValue(value) {
		entry, _ := item.(map[string]any)
		if len(entry) == 0 || !permissionRuleEnabled(entry["enabled"]) {
			continue
		}
		rule := permissionPathRule{
			Path:  strings.TrimSpace(stringFromAny(entry["path"])),
			Level: strings.TrimSpace(stringFromAny(entry[levelKey])),
		}
		if rule.Path != "" && rule.Level != "" {
			rules = append(rules, rule)
		}
	}
	return rules
}

func commandRulesFromConfig(value any, includeDenyPrompt bool) []permissionCommandRule {
	rules := []permissionCommandRule{}
	for _, item := range anySliceValue(value) {
		entry, _ := item.(map[string]any)
		if len(entry) == 0 || !permissionRuleEnabled(entry["enabled"]) {
			continue
		}
		rule := permissionCommandRule{Pattern: strings.TrimSpace(stringFromAny(entry["pattern"]))}
		if includeDenyPrompt {
			rule.DenyPrompt = stringFromAny(entry["denyPrompt"])
		}
		if rule.Pattern != "" {
			rules = append(rules, rule)
		}
	}
	return rules
}

func permissionRuleEnabled(value any) bool {
	if value == nil {
		return true
	}
	if b, ok := value.(bool); ok {
		return b
	}
	return false
}

func isSQLiteMissingTableError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "no such table")
}

func isSQLiteMissingColumnError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "no such column")
}

func (s *Service) loadPathRules(ctx context.Context, query, narratorID string) ([]permissionPathRule, error) {
	rows, err := s.DB.QueryContext(ctx, query, narratorID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	rules := []permissionPathRule{}
	for rows.Next() {
		var rule permissionPathRule
		if err := rows.Scan(&rule.Path, &rule.Level); err != nil {
			return nil, err
		}
		if strings.TrimSpace(rule.Path) != "" {
			rules = append(rules, rule)
		}
	}
	return rules, rows.Err()
}

func (s *Service) loadCommandRules(ctx context.Context, query, narratorID string) ([]permissionCommandRule, error) {
	rows, err := s.DB.QueryContext(ctx, query, narratorID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	rules := []permissionCommandRule{}
	for rows.Next() {
		var rule permissionCommandRule
		if err := rows.Scan(&rule.Pattern, &rule.DenyPrompt); err != nil {
			return nil, err
		}
		if strings.TrimSpace(rule.Pattern) != "" {
			rules = append(rules, rule)
		}
	}
	return rules, rows.Err()
}

func toolOperation(name string) string {
	switch name {
	case "write", "edit", "bash":
		return "write"
	default:
		return "read"
	}
}

func toolPermissionPaths(cwd, name string, args map[string]any) []string {
	switch name {
	case "read", "write", "edit":
		if path := strings.TrimSpace(stringFromAny(args["file_path"])); path != "" {
			return []string{path}
		}
	case "glob", "grep":
		if path := strings.TrimSpace(stringFromAny(args["path"])); path != "" {
			return []string{path}
		}
		return []string{"."}
	case "bash":
		if workdir := strings.TrimSpace(stringFromAny(args["workdir"])); workdir != "" {
			return []string{workdir}
		}
		return []string{"."}
	}
	return nil
}

func blacklistedPathReason(cwd string, paths []string, rules []permissionPathRule, operation string) string {
	for _, path := range paths {
		absPath := resolvePermissionPath(cwd, path)
		for _, rule := range rules {
			level := strings.TrimSpace(rule.Level)
			if !pathInsideOrEqual(resolvePermissionPath(cwd, rule.Path), absPath) {
				continue
			}
			if level == "denyAll" || operation == "write" {
				return fmt.Sprintf("Path is blocked by narrator blacklist: %s (%s)", rule.Path, level)
			}
		}
	}
	return ""
}

func allPathsWhitelisted(cwd string, paths []string, rules []permissionPathRule, required string) bool {
	if len(paths) == 0 || len(rules) == 0 {
		return false
	}
	for _, path := range paths {
		absPath := resolvePermissionPath(cwd, path)
		allowed := false
		for _, rule := range rules {
			if pathInsideOrEqual(resolvePermissionPath(cwd, rule.Path), absPath) && permissionLevelCovers(rule.Level, required) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return true
}

func permissionLevelCovers(level, required string) bool {
	order := map[string]int{"readOnly": 1, "readWrite": 2, "full": 3}
	return order[strings.TrimSpace(level)] >= order[strings.TrimSpace(required)]
}

func resolvePermissionPath(cwd, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func pathInsideOrEqual(base, target string) bool {
	base = filepath.Clean(base)
	target = filepath.Clean(target)
	if base == target {
		return true
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func commandBlacklistMatch(command string, rules []permissionCommandRule) *permissionCommandRule {
	commands := splitShellCommands(command)
	for _, tokens := range commands {
		for i := range rules {
			if matchCommandPattern(tokens, rules[i].Pattern) {
				return &rules[i]
			}
		}
	}
	return nil
}

func commandWhitelistCovered(command string, rules []permissionCommandRule) bool {
	commands := splitShellCommands(command)
	if len(commands) == 0 || len(rules) == 0 {
		return false
	}
	for _, tokens := range commands {
		matched := false
		for _, rule := range rules {
			if matchCommandPattern(tokens, rule.Pattern) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func splitShellCommands(command string) [][]string {
	command = strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n").Replace(command)
	out := [][]string{}
	for _, part := range strings.Split(command, "\n") {
		tokens := strings.Fields(strings.TrimSpace(part))
		if len(tokens) > 0 {
			out = append(out, tokens)
		}
	}
	return out
}

func matchCommandPattern(tokens []string, pattern string) bool {
	parts := strings.Fields(strings.TrimSpace(pattern))
	if len(parts) == 0 || len(parts) > len(tokens) {
		return false
	}
	for i, part := range parts {

		if ok, _ := filepath.Match(part, tokens[i]); !ok {
			return false
		}
	}
	return true
}

func normalizePermissionMode(value, fallback string) string {
	if normalized, ok := normalizePermissionModeStrict(value); ok {
		return normalized
	}
	if normalized, ok := normalizePermissionModeStrict(fallback); ok {
		return normalized
	}
	return "default"
}

func normalizePermissionModeStrict(value string) (string, bool) {
	switch strings.TrimSpace(value) {
	case "default", "acceptEdits", "bypassPermissions", "readOnly", "dontAsk":
		return strings.TrimSpace(value), true
	case "allowByDefault":
		return "acceptEdits", true
	case "denyByDefault":
		return "dontAsk", true
	case "danger-full-access", "unrestricted", "bypass", "trust-all":
		return "bypassPermissions", true
	case "plan":
		return "default", true
	default:
		return "", false
	}
}

func toolDecisionRejected(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "denied", "cancelled", "rejected", "fail", "failed":
		return true
	default:
		return false
	}
}

func toolRunDBStatus(status string, isError bool) string {
	if isError {
		return "fail"
	}
	return toolCallDBStatus(status)
}

func toolCallDBStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "completed", "complete", "success", "succeeded", "approved":
		return "success"
	case "running", "pending", "initializing":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "fail"
	}
}

func permissionDecisionDBStatus(decision string) string {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "allow", "approved", "approve":
		return "running"
	default:
		return "fail"
	}
}

func decisionOutput(decision permissionDecision) string {
	if strings.TrimSpace(decision.Message) != "" {
		return decision.Message
	}
	if strings.TrimSpace(decision.Reason) != "" {
		return decision.Reason
	}
	if strings.TrimSpace(decision.Status) != "" {
		return "Permission " + decision.Status + "."
	}
	return "Permission denied."
}

func boolPtrValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func normalizeBooleanOverride(value string) string {
	switch strings.TrimSpace(value) {
	case "on", "off":
		return strings.TrimSpace(value)
	default:
		return "inherit"
	}
}

func nullableTrimmedString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

const narratorSelectColumns = `id, chapter_id, api_conversation_id, fork_message_id, type, subagent_type, title,

inherit_mode, parent_narrator_id, context_summary, model, system_prompt, permission_mode,
previous_permission_mode, plan_file_id, reasoning_effort, fast_mode, relaxed_plan, plan_reflection_auto_approve_override,
danger_reflection_override, COALESCE(message_count, 0), COALESCE(total_cost_usd, 0),
last_message_at, status, substatus, plan_mode, cwd, error_message, todos_json, todos_tool_use_id,
enabled_tools, variant, traits, is_background, background_status, background_result, background_completed_at,
is_ask_in_passing, message_version, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanNarrator(row rowScanner) (Narrator, error) {
	var n Narrator
	var chapterID, apiConversationID, forkMessageID, subagentType, title, parentNarratorID sql.NullString
	var contextSummary, model, systemPrompt, permissionMode, previousPermissionMode, planFileID, reasoningEffort sql.NullString
	var lastMessageAt, cwd, errorMessage, todosJSON, todosToolUseID sql.NullString
	var enabledTools, backgroundStatus, backgroundResult, backgroundCompletedAt sql.NullString
	var substatusJSON, traitsJSON sql.NullString
	var fastMode, relaxedPlan, planMode, isBackground, isAskInPassing int
	err := row.Scan(&n.ID, &chapterID, &apiConversationID, &forkMessageID, &n.Type, &subagentType, &title,
		&n.InheritMode, &parentNarratorID, &contextSummary, &model, &systemPrompt, &permissionMode,
		&previousPermissionMode, &planFileID, &reasoningEffort, &fastMode, &relaxedPlan, &n.PlanReflectionAutoApproveOverride,

		&n.DangerReflectionOverride, &n.MessageCount, &n.TotalCostUSD, &lastMessageAt, &n.Status,
		&substatusJSON, &planMode, &cwd, &errorMessage, &todosJSON, &todosToolUseID,
		&enabledTools, &n.Variant, &traitsJSON, &isBackground, &backgroundStatus, &backgroundResult,
		&backgroundCompletedAt, &isAskInPassing, &n.MessageVersion, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return Narrator{}, err
	}
	n.ChapterID = nullStringPtr(chapterID)
	n.APIConversationID = nullStringPtr(apiConversationID)
	n.ForkMessageID = nullStringPtr(forkMessageID)
	n.SubagentType = nullStringPtr(subagentType)
	n.Title = nullStringPtr(title)
	n.ParentNarratorID = nullStringPtr(parentNarratorID)
	n.ContextSummary = nullStringPtr(contextSummary)
	n.Model = nullStringPtr(model)
	n.SystemPrompt = nullStringPtr(systemPrompt)
	n.PermissionMode = nullStringPtr(permissionMode)
	n.PreviousPermissionMode = nullStringPtr(previousPermissionMode)
	n.PlanFileID = nullStringPtr(planFileID)
	n.ReasoningEffort = nullStringPtr(reasoningEffort)
	n.LastMessageAt = nullStringPtr(lastMessageAt)
	n.Cwd = nullStringPtr(cwd)
	n.ErrorMessage = nullStringPtr(errorMessage)
	n.TodosJSON = jsondb.ArrayOrNil(todosJSON)
	n.TodosToolUseID = nullStringPtr(todosToolUseID)
	n.EnabledTools = jsondb.ArrayOrNil(enabledTools)
	n.BackgroundStatus = nullStringPtr(backgroundStatus)
	n.BackgroundResult = nullStringPtr(backgroundResult)
	n.BackgroundCompletedAt = nullStringPtr(backgroundCompletedAt)
	n.FastMode = fastMode != 0
	n.RelaxedPlan = relaxedPlan != 0
	n.PlanMode = planMode != 0
	n.IsBackground = isBackground != 0
	n.IsAskInPassing = isAskInPassing != 0
	n.Substatus = jsondb.StringArray(substatusJSON)
	n.Traits = jsondb.StringArray(traitsJSON)
	return n, nil
}

func scanMessage(row rowScanner) (Message, error) {
	var msg Message
	var messageUUID, parentToolUseID, contentText, model, commitSHA, commandText, createdBy sql.NullString
	var raw string
	if err := row.Scan(&msg.ID, &msg.NarratorID, &messageUUID, &parentToolUseID, &msg.Role, &raw, &contentText, &model, &commitSHA, &commandText, &createdBy, &msg.CreatedAt, &msg.Seq); err != nil {
		return Message{}, err
	}
	msg.MessageUUID = nullStringPtr(messageUUID)
	msg.ParentToolUseID = nullStringPtr(parentToolUseID)
	msg.ContentText = nullStringPtr(contentText)
	msg.Model = nullStringPtr(model)
	msg.CommitSHA = nullStringPtr(commitSHA)
	msg.CommandText = nullStringPtr(commandText)
	msg.CreatedBy = nullStringPtr(createdBy)
	var content any
	if err := json.Unmarshal([]byte(raw), &content); err != nil {
		content = raw
	}
	msg.ContentJSON = content
	msg.ToolCalls = []ToolCallRecord{}
	msg.Children = []Message{}
	return msg, nil
}

func decodeAny(raw sql.NullString) any {
	return jsondb.DecodeNull(raw)
}

func nullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func nullablePtr(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}
