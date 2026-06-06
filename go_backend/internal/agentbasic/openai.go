package agentbasic

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"narrafork-go/internal/config"
	"narrafork-go/internal/id"
)

type Message struct {
	Role          string           `json:"role"`
	Content       string           `json:"content,omitempty"`
	ToolCallID    string           `json:"tool_call_id,omitempty"`
	ToolCalls     []ToolCall       `json:"tool_calls,omitempty"`
	ContentBlocks []map[string]any `json:"-"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
	StreamStartedAt int64 `json:"-"`
	OutputIndex     *int  `json:"-"`
}

type Provider struct {
	Prefix                 string
	Kind                   string
	APIKey                 string
	BaseURL                string
	Model                  string
	OfficialAPI            bool
	APIMode                string
	AccountID              string
	CredentialID           string
	PromptCacheKey         string
	CredentialPool         []Provider
	LoadBalancingMode      string
	KiroCredentialsPath    string
	KiroConfigPath         string
	PreviousResponseID     string
	UseWebSocket           *bool
	DefaultReasoningEffort string
	ProxyURL               string
	TLSRejectUnauthorized  *bool
}

type Result struct {
	Text     string
	ToolRuns []ToolRun
}

type ToolRun struct {
	ID         string
	Name       string
	Arguments  string
	Output     string
	Status     string
	Error      string
	Recorded   bool
	FileChange *FileChange
}

type FileChange struct {
	FilePath      string
	Operation     string
	BeforeContent *string
	AfterContent  *string
}

type shareDiskMetadata struct {
	ID           string `json:"id"`
	OriginalName string `json:"originalName"`
	FileName     string `json:"fileName"`
	SizeBytes    int64  `json:"sizeBytes"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	CreatedBy    string `json:"createdBy,omitempty"`
}

type ToolExecutor func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error)

type BeforeTurnHook func(turnIndex int) error

type ShouldStopHook func() bool

type ExternalToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any
	Source      string
	Metadata    map[string]any
}

const (
	DefaultSystemPrompt         = "You are NarraFork Go backend's coding assistant. Use available tools when needed, keep answers concise, and explain important changes."
	defaultMaxTurns             = 4
	maxAllowedTurns             = 200
	defaultMaxChatRetries       = 3
	defaultEmptyResponseRetries = 3
	maxToolWriteBytes           = 2 * 1024 * 1024
	maxToolEditFileBytes        = 2 * 1024 * 1024
	maxToolShareSourceBytes     = 512 * 1024 * 1024
	defaultToolShareExpiry      = 24 * time.Hour
)

var safeShareIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type customRetryRule struct {
	Domain     string
	StatusCode int
	Keyword    string
	Disabled   bool
}

type generateOptions struct {
	toolExecutor                      ToolExecutor
	systemPrompt                      string
	maxTurns                          int
	reasoningEffort                   string
	toolScope                         string
	enabledTools                      []string
	disabledTools                     []string
	emitToolInventory                 bool
	externalTools                     []ExternalToolSpec
	firstTokenTimeout                 time.Duration
	maxChatRetries                    int
	emptyResponseRetries              int
	retryBaseDelay                    time.Duration
	customRetryRules                  []customRetryRule
	stickySessionKey                  string
	sideCarCollector                  SideCarCollector
	sideCarInitialCompletedToolCount  int
	onSideCarCompletedToolCount       func(int)
	silentToolCallThreshold           int
	silentToolCallThresholdConfigured bool
	silentToolCallCount               int
	beforeTurnHook                    BeforeTurnHook
	shouldStopHook                    ShouldStopHook
}

type SideCarRequest struct {
	Phase              string
	ToolName           string
	ToolUseID          string
	ToolInput          map[string]any
	CompletedToolCount int
}

type SideCarCollector func(ctx context.Context, request SideCarRequest) []AgentSideCar

type GenerateOption func(*generateOptions)

func WithToolExecutor(executor ToolExecutor) GenerateOption {
	return func(opts *generateOptions) {
		opts.toolExecutor = executor
	}
}

func WithSystemPrompt(prompt string) GenerateOption {
	return func(opts *generateOptions) {
		opts.systemPrompt = strings.TrimSpace(prompt)
	}
}

func WithMaxTurns(maxTurns int) GenerateOption {
	return func(opts *generateOptions) {
		opts.maxTurns = maxTurns
	}
}

func WithReasoningEffort(reasoningEffort string) GenerateOption {
	return func(opts *generateOptions) {
		opts.reasoningEffort = strings.TrimSpace(reasoningEffort)
	}
}

func WithToolScope(scope string) GenerateOption {
	return func(opts *generateOptions) {
		opts.toolScope = strings.TrimSpace(scope)
	}
}

func WithMaxChatRetries(maxRetries int) GenerateOption {
	return func(opts *generateOptions) {
		opts.maxChatRetries = maxRetries
	}
}

func WithRetryBaseDelay(delay time.Duration) GenerateOption {
	return func(opts *generateOptions) {
		opts.retryBaseDelay = delay
	}
}

func withCustomRetryRules(rules []customRetryRule) GenerateOption {
	return func(opts *generateOptions) {
		opts.customRetryRules = normalizeCustomRetryRules(rules)
	}
}

func WithExternalTools(tools []ExternalToolSpec) GenerateOption {
	return func(opts *generateOptions) {
		opts.externalTools = normalizeExternalTools(tools)
	}
}

func WithFirstTokenTimeout(timeout time.Duration) GenerateOption {
	return func(opts *generateOptions) {
		opts.firstTokenTimeout = timeout
	}
}

func WithEmptyResponseRetries(maxRetries int) GenerateOption {
	return func(opts *generateOptions) {
		opts.emptyResponseRetries = maxRetries
	}
}

func WithStickySessionKey(sessionKey string) GenerateOption {
	return func(opts *generateOptions) {
		opts.stickySessionKey = strings.TrimSpace(sessionKey)
	}
}

func WithSideCarCollector(initialCompletedToolCount int, collector SideCarCollector, onCompletedToolCount func(int)) GenerateOption {
	return func(opts *generateOptions) {
		opts.sideCarInitialCompletedToolCount = initialCompletedToolCount
		opts.sideCarCollector = collector
		opts.onSideCarCompletedToolCount = onCompletedToolCount
	}
}

func WithSilentToolCallThreshold(threshold int) GenerateOption {
	return func(opts *generateOptions) {
		opts.silentToolCallThreshold = threshold
		opts.silentToolCallThresholdConfigured = true
	}
}

func WithBeforeTurnHook(hook BeforeTurnHook) GenerateOption {
	return func(opts *generateOptions) {
		opts.beforeTurnHook = hook
	}
}

func WithShouldStop(hook ShouldStopHook) GenerateOption {
	return func(opts *generateOptions) {
		opts.shouldStopHook = hook
	}
}

func Generate(ctx context.Context, settings *config.Store, model string, history []Message, cwd string, opts ...GenerateOption) (Result, error) {
	stream, err := RunAgentLoop(ctx, settings, model, history, cwd, opts...)
	if err != nil {
		return Result{}, err
	}
	return collectAgentResult(stream)
}

func normalizeGenerateOptions(opts ...GenerateOption) generateOptions {
	options := generateOptions{
		systemPrompt:            DefaultSystemPrompt,
		maxTurns:                defaultMaxTurns,
		toolScope:               "normal",
		maxChatRetries:          defaultMaxChatRetries,
		emptyResponseRetries:    defaultEmptyResponseRetries,
		retryBaseDelay:          200 * time.Millisecond,
		silentToolCallThreshold: 20,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if strings.TrimSpace(options.systemPrompt) == "" {
		options.systemPrompt = DefaultSystemPrompt
	}
	if options.silentToolCallThreshold < -1 {
		options.silentToolCallThreshold = -1
	}
	if options.maxTurns <= 0 {
		options.maxTurns = defaultMaxTurns
	}
	if options.maxTurns > maxAllowedTurns {
		options.maxTurns = maxAllowedTurns
	}
	options.reasoningEffort = normalizeReasoningEffort(options.reasoningEffort)
	options.toolScope = normalizeToolScopeName(options.toolScope)
	options.enabledTools = normalizedToolNames(options.enabledTools)
	options.disabledTools = normalizedToolNames(options.disabledTools)
	options.externalTools = normalizeExternalTools(options.externalTools)
	if options.firstTokenTimeout < 0 {
		options.firstTokenTimeout = 0
	}
	if options.maxChatRetries < 0 {
		options.maxChatRetries = 0
	}
	if options.emptyResponseRetries < 0 {
		options.emptyResponseRetries = 0
	}
	if options.retryBaseDelay < 0 {
		options.retryBaseDelay = 0
	}
	return options
}

func normalizeExternalTools(tools []ExternalToolSpec) []ExternalToolSpec {
	seen := map[string]bool{}
	out := []ExternalToolSpec{}
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		params := tool.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		source := strings.TrimSpace(tool.Source)
		if source == "" {
			source = "external"
		}
		out = append(out, ExternalToolSpec{
			Name:        name,
			Description: strings.TrimSpace(tool.Description),
			Parameters:  params,
			Source:      source,
			Metadata:    cloneMetadata(tool.Metadata),
		})
	}
	return out
}

func cloneMetadata(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func normalizeReasoningEffort(value string) string {
	switch strings.TrimSpace(value) {
	case "none", "low", "medium", "high", "xhigh":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func defaultToolExecutor(execCtx context.Context, cwd string, call ToolCall) (ToolRun, error) {
	return ToolRun{
		ID:        call.ID,
		Name:      call.Function.Name,
		Arguments: call.Function.Arguments,
		Output:    ExecuteTool(execCtx, cwd, call.Function.Name, call.Function.Arguments),
		Status:    "completed",
	}, nil
}

func normalizeToolRun(run ToolRun, call ToolCall) ToolRun {
	if strings.TrimSpace(run.ID) == "" {
		run.ID = call.ID
	}
	if strings.TrimSpace(run.Name) == "" {
		run.Name = call.Function.Name
	}
	if strings.TrimSpace(run.Arguments) == "" {
		run.Arguments = call.Function.Arguments
	}
	if strings.TrimSpace(run.Status) == "" {
		run.Status = "completed"
	}
	return run
}

func toolDefinitions(scope ...string) []map[string]any {
	toolScope := "normal"
	if len(scope) > 0 && strings.TrimSpace(scope[0]) != "" {
		toolScope = strings.TrimSpace(scope[0])
	}
	return openAIToolDefinitionsFromSpecs(visibleToolSpecs(toolScope))
}

func openAIToolDefinitionsFromSpecs(specs []toolSpec) []map[string]any {
	tools := []map[string]any{}
	for _, spec := range specs {
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        spec.Name,
				"description": spec.Description,
				"parameters":  spec.Parameters,
			},
		})
	}
	return tools
}

func anthropicToolDefinitions(scope ...string) []map[string]any {
	toolScope := "normal"
	if len(scope) > 0 && strings.TrimSpace(scope[0]) != "" {
		toolScope = strings.TrimSpace(scope[0])
	}
	return anthropicToolDefinitionsFromSpecs(visibleToolSpecs(toolScope))
}

func anthropicToolDefinitionsFromSpecs(specs []toolSpec) []map[string]any {
	tools := []map[string]any{}
	for _, spec := range specs {
		tools = append(tools, map[string]any{
			"name":         spec.Name,
			"description":  spec.Description,
			"input_schema": spec.Parameters,
		})
	}
	return tools
}

func kiroToolDefinitionsFromSpecs(specs []toolSpec) []map[string]any {
	tools := []map[string]any{}
	for _, spec := range specs {
		toolSpec := map[string]any{"name": spec.Name, "description": spec.Description}
		toolSpec["inputSchema"] = map[string]any{"json": ensureNonEmptySchema(spec.Parameters)}
		tools = append(tools, map[string]any{"toolSpecification": toolSpec})
	}
	return tools
}

func formatToolsForAdapter(adapter ProviderAdapter, opts ToolInventoryOptions) []map[string]any {
	opts.Scope = normalizeToolScopeName(opts.Scope)
	opts.EnabledTools = normalizedToolNames(opts.EnabledTools)
	opts.DisabledTools = normalizedToolNames(opts.DisabledTools)
	opts.ExternalTools = normalizeExternalTools(opts.ExternalTools)
	if len(opts.ExternalTools) == 0 {
		return adapter.FormatTools(encodedToolScope(opts.Scope, opts.EnabledTools, opts.DisabledTools))
	}
	specs := resolveToolSpecs(opts)
	switch typed := adapter.(type) {
	case *AnthropicMessagesAdapter:
		return anthropicToolDefinitionsFromSpecs(specs)
	case *KiroProviderAdapter:
		return kiroToolDefinitionsFromSpecs(specs)
	case *NugProviderAdapter:
		return formatToolsForAdapter(typed.delegate(), opts)
	default:
		return openAIToolDefinitionsFromSpecs(specs)
	}
}

type toolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any
	Visibility  string
}

func (s toolSpec) visibleIn(scope string) bool {
	visibility := s.Visibility
	if visibility == "" {
		visibility = "normal"
	}
	if scope == "review" {
		return visibility == "normal" || visibility == "review"
	}
	return visibility == scope
}

func visibleToolSpecs(scope string) []toolSpec {
	return resolveToolSpecs(toolInventoryOptionsFromScope(scope))
}

func reflectionToolSpecs() []toolSpec {
	return visibleToolSpecs("reflection")
}

func reflectionToolSpec(name, description string) toolSpec {
	stringProp := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	props := map[string]any{"confirm": map[string]any{"type": "boolean", "description": "Must be true."}}
	required := []string{"confirm"}
	switch name {
	case "GoalCompleteConfirm":
		props["evidence"] = stringProp("Concrete evidence that every material requirement is satisfied")
		props["reflection"] = stringProp("Optional explanation of why completion is safe")
		required = append(required, "evidence")
	case "GoalCompleteRevise":
		props["feedback"] = stringProp("Concise feedback describing why the goal cannot be completed yet")
		props["nextSteps"] = stringProp("Concrete next action for the main narrator before trying UpdateGoal again")
		required = append(required, "feedback", "nextSteps")
	case "ExitPlanConfirm":
		props["reflection"] = stringProp("Optional explanation of why the plan is ready")
	case "ExitPlanRevise":
		props["feedback"] = stringProp("Actionable feedback describing what must be revised")
		required = append(required, "feedback")
	case "DangerConfirm":
		props["reflection"] = stringProp("Optional explanation of why the dangerous operation is justified")
	case "DangerCancel":
		props["reason"] = stringProp("Reason for cancelling the dangerous operation")
	}
	return toolSpec{
		Name:        name,
		Description: description,
		Visibility:  "reflection",
		Parameters: map[string]any{
			"type":       "object",
			"properties": props,
			"required":   required,
		},
	}
}

func toolSpecs() []toolSpec {
	stringProp := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	return []toolSpec{
		{
			Name:        "Bash",
			Description: "Run a shell command in the narrator working directory. Output is truncated.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command":           stringProp("Command to execute"),
					"description":       stringProp("Short description of what the command does"),
					"timeout":           map[string]any{"type": "number", "description": "Timeout in milliseconds, capped at 600000"},
					"workdir":           stringProp("Optional working directory under the narrator cwd"),
					"run_in_background": map[string]any{"type": "boolean", "description": "Run in background and return a task ID when executed through a narrator session"},
					"strict_serial":     map[string]any{"type": "boolean", "description": "Execute strictly serially instead of parallelizing with other eager tools"},
				},
				"required":             []string{"command"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "Read",
			Description: "Read a text file under the working directory.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": stringProp("Relative or absolute path"),
					"offset":    map[string]any{"type": "number", "description": "1-based line number to start reading from"},
					"limit":     map[string]any{"type": "number", "description": "Number of lines to read; -1 reads to EOF"},
					"pages":     stringProp("PDF page range such as 1-5; Go runtime currently rejects PDF page extraction explicitly"),
				},
				"required":             []string{"file_path"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "Write",
			Description: "Write a text file under the working directory.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": stringProp("Relative or absolute path"),
					"content":   stringProp("File content"),
				},
				"required":             []string{"file_path", "content"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "Edit",
			Description: "Replace exact text in a file under the working directory.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path":   stringProp("Relative or absolute path"),
					"old_string":  stringProp("Exact text to replace"),
					"new_string":  stringProp("Replacement text"),
					"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences instead of requiring a unique match"},
				},
				"required":             []string{"file_path", "old_string", "new_string"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "Glob",
			Description: "List files whose relative path matches a glob pattern such as **/*.go or *.ts.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": stringProp("Pattern such as *.go or internal"),
					"path":    stringProp("Optional directory under the working directory to search from"),
				},
				"required":             []string{"pattern"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "Grep",
			Description: "Search text files with a regex pattern under the working directory.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern":     stringProp("Regex pattern"),
					"path":        stringProp("Optional file or directory under the working directory to search in"),
					"glob":        stringProp("Optional glob filter, such as **/*.go"),
					"output_mode": map[string]any{"type": "string", "enum": []string{"content", "files_with_matches", "count"}, "description": "content, files_with_matches, or count; defaults to files_with_matches"},
					"context":     map[string]any{"type": "number", "description": "Lines before and after each match in content mode"},
					"-B":          map[string]any{"type": "number", "description": "Lines before each match in content mode"},
					"-A":          map[string]any{"type": "number", "description": "Lines after each match in content mode"},
					"-C":          map[string]any{"type": "number", "description": "Alias for context in content mode"},
					"-n":          map[string]any{"type": "boolean", "description": "Show line numbers in content mode; defaults to true"},
					"head_limit":  map[string]any{"type": "number", "description": "Maximum output entries"},
					"offset":      map[string]any{"type": "number", "description": "Skip this many output entries before applying head_limit"},
					"-i":          map[string]any{"type": "boolean", "description": "Case-insensitive search"},
					"type":        stringProp("File type to search, such as go, js, ts, py, rust, java, json, or md"),
					"multiline":   map[string]any{"type": "boolean", "description": "Enable multiline dotall matching"},
				},
				"required":             []string{"pattern"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "WebFetch",
			Description: "Fetch an HTTP, HTTPS, or data URL and return truncated response text.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":        stringProp("HTTP, HTTPS, or data URL to fetch"),
					"mode":       map[string]any{"type": "string", "enum": []string{"readability", "screenshot", "dom", "smart"}, "description": "TS extraction mode; Go runtime rejects these modes and defaults to raw fetch when omitted"},
					"selector":   stringProp("CSS selector for dom mode; unsupported in Go raw-only runtime"),
					"max_length": map[string]any{"type": "number", "description": "Maximum response bytes to return"},
					"purpose":    stringProp("Purpose for smart mode; unsupported in Go raw-only runtime"),
				},
				"required":             []string{"url"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "WebSearch",
			Description: "Search the web for current information and return result snippets with source URLs.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":           map[string]any{"type": "string", "minLength": 2, "description": "Search query"},
					"allowed_domains": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Only include results from these domains"},
					"blocked_domains": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Exclude results from these domains"},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
			},
		},
		recallToolSpec(),
		terminalToolSpec(),
		{
			Name:        "ShareFile",
			Description: "Share files from the working directory. Directories become .tar.gz, multiple paths become .zip, and single files can be gzip-compressed.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":     map[string]any{"description": "Relative/absolute path or an array of paths under the working directory", "oneOf": []map[string]any{{"type": "string"}, {"type": "array", "items": map[string]any{"type": "string"}}}},
					"compress": map[string]any{"type": "boolean", "description": "Gzip-compress a single file"},
					"preview":  map[string]any{"type": "boolean", "description": "Include preview URL for raw single files"},
				},
				"required":             []string{"path"},
				"additionalProperties": false,
			},
		},
		browserToolSpec(),
		{
			Name:        "Agent",
			Description: "Launch or stop a subagent narrator for a focused task. Built-in types include explore, plan, general, and review; custom type names are accepted by the Go backend with a basic fallback prompt.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"description":       stringProp("Short task description"),
					"subagent_type":     stringProp("explore, plan, general, review, or a custom subagent type name"),
					"prompt":            stringProp("Task prompt for the subagent"),
					"model":             stringProp("Optional model override"),
					"reasoning_effort":  map[string]any{"type": "string", "enum": []string{"none", "low", "medium", "high", "xhigh"}, "description": "Optional reasoning effort"},
					"workdir":           stringProp("Optional working directory under the parent cwd"),
					"alias":             stringProp("Optional alias for later Await calls"),
					"stop":              stringProp("Subagent id or alias to stop"),
					"timeout":           map[string]any{"type": "number", "description": "Foreground wait timeout in milliseconds, capped at 30000"},
					"run_in_background": map[string]any{"type": "boolean", "description": "Run as a background subagent"},
					"background":        map[string]any{"type": "boolean", "description": "Legacy alias for run_in_background"},
				},
				"additionalProperties": false,
			},
		},
		{
			Name:        "Await",
			Description: "Wait for or inspect a background agent or bash result.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":          map[string]any{"type": "string", "enum": []string{"agent", "bash"}, "description": "What to await: agent or bash"},
					"id":            stringProp("Subagent id or alias"),
					"timeout":       map[string]any{"type": "number", "description": "Milliseconds to wait, capped at 30000"},
					"wait_for_text": stringProp("For type=bash only: return early once matching output appears"),
				},
				"required":             []string{"type", "id"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "TeamStatus",
			Description: "List sibling/child subagents, inspect file changes, or send team messages.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":    map[string]any{"type": "string", "enum": []string{"list", "file_changes", "broadcast", "send"}, "description": "list, file_changes, broadcast, or send"},
					"target_id": stringProp("Target subagent id/alias/title/prefix for file_changes or send"),
					"message":   stringProp("Message text for broadcast or send"),
				},
				"required":             []string{"action"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "Send",
			Description: "Send a message to one or more child subagents; optionally wait for responses.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":          stringProp("Target subagent ID or alias"),
					"ids":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Target subagent IDs or aliases"},
					"name":        stringProp("Target subagent title, alias, or ID prefix"),
					"names":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Target names, aliases, or ID prefixes"},
					"message":     stringProp("Message to send"),
					"doInterrupt": map[string]any{"type": "boolean", "description": "Mark target idle before sending"},
					"await":       map[string]any{"type": "boolean", "description": "Wait for target responses"},
					"timeout":     map[string]any{"type": "number", "description": "Milliseconds to wait when await=true"},
				},
				"required":             []string{"message"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "AskUserQuestion",
			Description: "Ask the user structured questions during execution and wait for answers.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"questions": map[string]any{
						"type":     "array",
						"minItems": 1,
						"maxItems": 4,
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"question":    stringProp("The complete question to ask the user"),
								"header":      stringProp("Very short label for the question"),
								"multiSelect": map[string]any{"type": "boolean", "default": false, "description": "Allow selecting multiple options"},
								"options": map[string]any{
									"type":     "array",
									"minItems": 2,
									"maxItems": 4,
									"items": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"label":       stringProp("Option label"),
											"description": stringProp("Option description"),
											"preview":     stringProp("Optional markdown preview"),
										},
										"required":             []string{"label", "description"},
										"additionalProperties": false,
									},
								},
							},
							"required":             []string{"question", "header", "options", "multiSelect"},
							"additionalProperties": false,
						},
					},
					"answers": map[string]any{
						"type":                 "object",
						"description":          "User answers collected by the permission component",
						"propertyNames":        map[string]any{"type": "string"},
						"additionalProperties": map[string]any{"type": "string"},
					},
					"annotations": map[string]any{
						"type":                 "object",
						"description":          "Optional per-question annotations from the user",
						"propertyNames":        map[string]any{"type": "string"},
						"additionalProperties": map[string]any{"type": "object", "properties": map[string]any{"preview": stringProp("Selected option preview"), "notes": stringProp("Free-text notes")}, "additionalProperties": false},
					},
					"metadata": map[string]any{"type": "object", "properties": map[string]any{"source": stringProp("Optional source identifier")}, "additionalProperties": false, "description": "Optional metadata"},
				},
				"required":             []string{"questions"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "Skill",
			Description: "Execute or load a NarraFork skill by name and return its instructions.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"skill": stringProp("Skill name"),
					"name":  stringProp("Skill name alias"),
					"args":  stringProp("Optional arguments for the skill"),
				},
			},
		},
		{
			Name:        "LearningGuide",
			Description: "Look up NarraFork's built-in learning documentation.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"mode":  map[string]any{"type": "string", "enum": []string{"list", "search", "get"}, "default": "search", "description": "list, search, or get"},
					"query": stringProp("Search query when mode is search"),
					"id":    stringProp("Learning document id when mode is get"),
					"lang":  stringProp("Preferred language, such as en or zh-CN"),
				},
			},
		},
		{
			Name:        "TaskCreate",
			Description: "Write the complete todo list for this narrator session, replacing any existing todos.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"todos": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id":       stringProp("Short unique todo identifier"),
								"content":  stringProp("Todo description"),
								"status":   map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}, "description": "pending, in_progress, or completed"},
								"priority": map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}, "default": "medium", "description": "high, medium, or low"},
							},
							"required": []string{"id", "content", "status"},
						},
					},
				},
				"required": []string{"todos"},
			},
		},
		{
			Name:        "ConcludeReview",
			Description: "Submit your structured review conclusion. Use this only in review chapters when the code review is complete.",
			Visibility:  "review",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"verdict": map[string]any{"type": "string", "enum": []string{"approve", "request_changes", "comment_only"}, "description": "Overall verdict on the code changes"},
					"findings": map[string]any{
						"type":        "array",
						"description": "List of findings ordered by severity",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"severity": map[string]any{"type": "string", "enum": []string{"critical", "major", "minor", "suggestion"}},
								"file":     stringProp("Optional file path"),
								"line":     map[string]any{"type": "number", "description": "Optional line number"},
								"message":  stringProp("Description of the finding"),
							},
							"required":             []string{"severity", "message"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"verdict", "findings"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "StartPipeline",
			Description: "Start capturing subsequent tool outputs under short aliases for this agent turn.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"label":           stringProp("Optional pipeline label"),
					"maxPreviewChars": map[string]any{"type": "number", "description": "Preview characters per captured output, max 100"},
				},
				"additionalProperties": false,
			},
		},
		{
			Name:        "EndPipeline",
			Description: "Stop pipeline capture and return selected captured output.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"rule":     stringProp("Optional rule such as: from p1 p2 | grep PATTERN | head -n 20"),
					"aliases":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Default aliases when rule omits from"},
					"format":   map[string]any{"type": "string", "enum": []string{"sections", "plain"}, "description": "Output format. Default sections."},
					"maxChars": map[string]any{"type": "number", "description": "Maximum output chars, max 50000"},
				},
				"additionalProperties": false,
			},
		},
		{
			Name:        "EnterPlanMode",
			Description: "Enter NarraFork plan mode for the current narrator. The backend stores the previous permission mode, switches to read-only planning, and creates a designated .narrafork/plan-{id}.md file target for plan drafts.",
			Parameters: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		},
		{
			Name:        "ExitPlanMode",
			Description: "Exit NarraFork plan mode after providing a complete implementation plan. If plan is omitted, the backend reads the designated .narrafork/plan-{id}.md file. The plan is validated, reflected on, permissions are restored, and a plan compact is attempted with a documented fallback.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"plan": stringProp("Complete implementation plan; optional if the designated plan file contains the plan"),
					"allowedPrompts": map[string]any{
						"type":        "array",
						"description": "Optional command categories the implementation may require",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"tool":   map[string]any{"type": "string", "enum": []string{"Bash"}, "description": "The tool this prompt applies to"},
								"prompt": stringProp("Semantic description of the action"),
							},
							"required":             []string{"tool", "prompt"},
							"additionalProperties": false,
						},
					},
				},
				"additionalProperties": map[string]any{},
			},
		},
		{
			Name:        "GetGoals",
			Description: "Get the current narrator goal list.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"confirm": map[string]any{"type": "boolean", "const": true, "default": true, "description": "Dummy parameter; always pass true"},
				},
				"required":             []string{"confirm"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "AddGoal",
			Description: "Add a concrete objective to the current narrator goal list.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"objective": map[string]any{"type": "string", "minLength": 1, "maxLength": 4000, "description": "Concrete objective to add"},
				},
				"required":             []string{"objective"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "UpdateGoal",
			Description: "Update the current active goal. This tool can only mark the active goal complete after NarraFork has run a separate goal-completion reflection gate that verifies the user-provided objective is actually achieved and no required work remains.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{"type": "string", "const": "complete", "description": "Set to complete only when the active goal is achieved."},
				},
				"required":             []string{"status"},
				"additionalProperties": false,
			},
		},
		reflectionToolSpec("DangerConfirm", "Confirm the currently paused dangerous operation after reflection."),
		reflectionToolSpec("DangerCancel", "Cancel the currently paused dangerous operation after reflection."),
		reflectionToolSpec("ExitPlanConfirm", "Confirm an exit-plan reflection and leave plan mode."),
		reflectionToolSpec("ExitPlanRevise", "Request revision during an exit-plan reflection."),
		reflectionToolSpec("GoalCompleteConfirm", "Confirm goal-completion reflection with evidence."),
		reflectionToolSpec("GoalCompleteRevise", "Reject goal completion and request more work."),
	}
}

func recallToolSpec() toolSpec {
	stringProp := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	return toolSpec{
		Name:        "Recall",
		Description: "Search and browse the current narrator conversation. Go supports a self-scoped, read-only subset: search, read_conversation, and read_tool_call.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"search", "read_conversation", "read_tool_call"},
					"description": "Action to perform: search, read_conversation, or read_tool_call",
				},
				"query": map[string]any{
					"description": "Search query string or an array of query strings for action=search",
					"oneOf":       []map[string]any{{"type": "string"}, {"type": "array", "items": map[string]any{"type": "string"}}},
				},
				"narrator_id":  stringProp("Optional narrator ID; when provided it must match the current narrator"),
				"message_id":   stringProp("Optional message ID to center read_conversation around"),
				"tool_call_id": stringProp("Tool use ID for action=read_tool_call"),
				"limit":        map[string]any{"type": "number", "description": "Bounded result count; search/read default 20, max 50"},
				"from":         stringProp("Optional absolute start time for search"),
				"to":           stringProp("Optional absolute end time for search"),
				"time_range":   stringProp("Optional relative search window such as 24h, 7d, 2w, 1mo, or 1y"),
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func terminalToolSpec() toolSpec {
	stringProp := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	terminalActions := []string{"read", "write", "list", "create", "rename"}
	return toolSpec{
		Name:        "Terminal",
		Description: "Interact with persistent interactive terminals for this narrator or chapter. Use Bash for one-off commands; use Terminal for long-running dev servers, REPLs, and incremental terminal output.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":       map[string]any{"type": "string", "enum": terminalActions, "description": "Terminal action to perform: " + strings.Join(terminalActions, ", ")},
				"terminal_id":  stringProp("Terminal ID or exact terminal name for read/write/rename"),
				"input":        stringProp("Input to send for write, e.g. \\n for Enter or \\x03 for Ctrl-C"),
				"name":         stringProp("Name for create or rename"),
				"last_n_lines": map[string]any{"type": "number", "description": "For read: return only the last N lines instead of incremental output"},
				"wait_for":     stringProp("For read: wait until this substring appears in new output, up to 30s"),
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func browserToolSpec() toolSpec {
	stringProp := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	browserActions := []string{"launch", "click", "fill", "select", "type", "hover", "screenshot", "get_text", "get_attribute", "get_console", "get_network", "network_start", "network_stop", "evaluate", "evaluate_capture", "wait", "navigate", "scroll", "dom", "close", "set_ttl", "list_sessions", "perf_start", "perf_stop"}
	properties := map[string]any{
		"action":          map[string]any{"type": "string", "enum": browserActions, "description": "Browser action to perform: " + strings.Join(browserActions, ", ")},
		"url":             stringProp("HTTP, HTTPS, or data URL for launch/navigate"),
		"session_id":      stringProp("Browser session ID"),
		"selector":        stringProp("CSS selector for element actions"),
		"value":           stringProp("Value for fill/select/type/evaluate/get_attribute/filter actions"),
		"key":             stringProp("Keyboard key for type, for example Enter or Escape"),
		"direction":       map[string]any{"type": "string", "enum": []string{"back", "forward", "up", "down"}, "description": "Navigation or scroll direction: back/forward for navigate; up/down for scroll"},
		"amount":          map[string]any{"type": "number", "description": "Scroll amount in pixels"},
		"timeout":         map[string]any{"type": "number", "description": "Action timeout in milliseconds"},
		"ttl_ms":          map[string]any{"type": "number", "description": "Session inactivity TTL in milliseconds"},
		"max_length":      map[string]any{"type": "number", "description": "Maximum output length"},
		"file_path":       stringProp("Path under the working directory for screenshot or trace output"),
		"headless":        map[string]any{"type": "boolean", "description": "Run browser headless; defaults to true"},
		"clear":           map[string]any{"type": "boolean", "description": "Clear console/network captures before or after reading"},
		"include_details": map[string]any{"type": "boolean", "description": "Include network request/response details in get_network output"},
		"capture_network": map[string]any{"type": "boolean", "description": "Capture network traffic starting with launch"},
		"wait_after_ms":   map[string]any{"type": "number", "description": "Wait after evaluate_capture before reading console output"},
		"categories":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Chrome trace categories for perf_start"},
		"coordinate":      map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "number"}, "y": map[string]any{"type": "number"}}, "required": []string{"x", "y"}},
	}
	return toolSpec{
		Name:        "Browser",
		Description: "Interactive Chrome/Chromium browser session powered by rod. Supports launch, navigate, dom, get_text, click, fill, select, type, hover, wait, scroll, get_attribute, get_console, get_network, network_start, network_stop, evaluate, evaluate_capture, screenshot, set_ttl, list_sessions, perf_start, perf_stop, and close.",
		Parameters: map[string]any{
			"type":                 "object",
			"properties":           properties,
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

type ToolRuntimeSettings struct {
	WebFetchProxy WebFetchProxyPolicy
}

type WebFetchProxyPolicy struct {
	Mode string
	URL  string
}

type toolRuntimeSettingsContextKey struct{}

func ContextWithToolRuntimeSettings(ctx context.Context, settings ToolRuntimeSettings) context.Context {
	return context.WithValue(ctx, toolRuntimeSettingsContextKey{}, settings)
}

func ToolRuntimeSettingsFromRawSettings(raw map[string]any) ToolRuntimeSettings {
	agent, _ := raw["agent"].(map[string]any)
	policy, _ := agent["webFetchPolicy"].(map[string]any)
	proxy, _ := policy["proxy"].(map[string]any)
	mode := stringValue(proxy["mode"])
	if mode == "" {
		mode = "system"
	}
	return ToolRuntimeSettings{WebFetchProxy: WebFetchProxyPolicy{Mode: mode, URL: stringValue(proxy["url"])}}
}

func ExecuteTool(ctx context.Context, cwd, name, rawArgs string) string {
	output, _ := executeTool(ctx, cwd, name, rawArgs)
	return output
}

func executeTool(ctx context.Context, cwd, name, rawArgs string) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return "invalid tool arguments: " + err.Error(), nil
	}
	if cwd == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cwd = home
		} else {
			cwd = "."
		}
	}
	switch name {
	case "Bash":
		if boolValue(args["run_in_background"]) {
			return "Bash run_in_background requires narrator context; non-narrator ExecuteTool only supports foreground Bash commands.", nil
		}
		bashCwd := cwd
		if workdir := strings.TrimSpace(stringValue(args["workdir"])); workdir != "" {
			resolved, err := resolveOptionalPathWithin(cwd, workdir)
			if err != nil {
				return err.Error(), nil
			}
			info, err := os.Stat(resolved)
			if err != nil {
				return err.Error(), nil
			}
			if !info.IsDir() {
				return "workdir must be a directory", nil
			}
			bashCwd = resolved
		}
		return toolBash(ctx, bashCwd, stringValue(args["command"]), intValue(args["timeout"], 30000)), nil
	case "Read":
		if strings.TrimSpace(stringValue(args["pages"])) != "" {
			return "Read pages/PDF extraction is not supported in the Go backend yet; omit pages and read text files only.", nil
		}
		return toolRead(cwd, stringValue(args["file_path"]), intValue(args["offset"], 1), intValue(args["limit"], 0)), nil
	case "Write":
		return toolWrite(cwd, pathValue(args["file_path"]), stringValue(args["content"])), nil
	case "Edit":
		return toolEdit(cwd, pathValue(args["file_path"]), stringValue(args["old_string"]), stringValue(args["new_string"]), boolValue(args["replace_all"])), nil
	case "Glob":
		return toolGlob(cwd, stringValue(args["pattern"]), stringValue(args["path"])), nil
	case "Grep":
		showLineNumbers := true
		if _, ok := args["-n"]; ok {
			showLineNumbers = boolValue(args["-n"])
		}
		return toolGrep(cwd, grepOptions{
			Pattern:         stringValue(args["pattern"]),
			Path:            stringValue(args["path"]),
			Glob:            stringValue(args["glob"]),
			OutputMode:      stringValue(args["output_mode"]),
			Context:         intValue(args["context"], -1),
			Before:          intValue(args["-B"], -1),
			After:           intValue(args["-A"], -1),
			ContextAlias:    intValue(args["-C"], -1),
			ShowLineNumbers: showLineNumbers,
			HeadLimit:       intValue(args["head_limit"], 200),
			Offset:          intValue(args["offset"], 0),
			Insensitive:     boolValue(args["-i"]),
			Type:            stringValue(args["type"]),
			Multiline:       boolValue(args["multiline"]),
		}), nil
	case "WebFetch":
		mode := strings.TrimSpace(stringValue(args["mode"]))
		if mode != "" && !strings.EqualFold(mode, "raw") {
			return fmt.Sprintf("unsupported WebFetch mode %q in Go backend; supported mode: raw. TS extraction modes readability, dom, smart, and screenshot are not implemented yet.", mode), nil
		}
		if strings.TrimSpace(stringValue(args["selector"])) != "" || strings.TrimSpace(stringValue(args["purpose"])) != "" {
			return "WebFetch selector and purpose parameters require TS extraction modes and are not supported in the Go backend yet.", nil
		}
		return toolWebFetch(ctx, stringValue(args["url"]), intValue(args["max_length"], 20000)), nil
	case "WebSearch":
		return toolWebSearch(ctx, stringValue(args["query"]), stringList(args["allowed_domains"]), stringList(args["blocked_domains"])), nil
	case "ShareFile":
		return toolShareFile(cwd, args["path"], boolValue(args["compress"]), boolValue(args["preview"])), nil
	case "Browser":
		return toolBrowser(ctx, cwd, args), nil
	case "Recall":
		return "Recall requires narrator context and cannot run outside a narrator session; no database was accessed.", nil
	case "Terminal", "Agent", "Await", "TeamStatus", "Send", "AskUserQuestion", "Skill", "LearningGuide", "TaskCreate", "StartPipeline", "EndPipeline", "EnterPlanMode", "ExitPlanMode", "GetGoals", "AddGoal", "UpdateGoal":
		return name + " requires narrator context and cannot run outside a narrator session", nil
	default:
		return "unknown tool: " + name, nil
	}
}

var bashLongRunningThreshold = 60 * time.Second
var bashLongRunningCheckInterval = time.Second

func toolBash(ctx context.Context, cwd, command string, timeoutMs int) string {
	if strings.TrimSpace(command) == "" {
		return "command is required"
	}
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}
	if timeoutMs < 1000 {
		timeoutMs = 1000
	}
	if timeoutMs > 600000 {
		timeoutMs = 600000
	}
	toolCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(toolCtx, "bash", "-lc", command)
	cmd.Dir = cwd
	buf := &cappedBuffer{limit: 64 * 1024, onWrite: func(output string) { EmitToolRuntimeOutput(ctx, output) }}
	cmd.Stdout = buf
	cmd.Stderr = buf
	started := time.Now()
	longRunningDone := make(chan struct{})
	if bashLongRunningThreshold > 0 && bashLongRunningCheckInterval > 0 {
		go func() {
			ticker := time.NewTicker(bashLongRunningCheckInterval)
			defer ticker.Stop()
			fired := false
			for {
				select {
				case <-longRunningDone:
					return
				case <-toolCtx.Done():
					return
				case now := <-ticker.C:
					if !fired && now.Sub(started) >= bashLongRunningThreshold {
						fired = true
						EmitToolRuntimeLongRunning(ctx, now.Sub(started))
					}
				}
			}
		}()
	}
	err := cmd.Run()
	close(longRunningDone)
	result := buf.String()
	if buf.Truncated() {
		result += "\n[truncated]"
	}
	if err != nil {
		if errors.Is(toolCtx.Err(), context.DeadlineExceeded) {
			result += "\n[timeout] command exceeded timeout"
		}
		result += "\n[exit error] " + err.Error()
	}
	EmitToolRuntimeOutput(ctx, result)
	return result
}

func toolRead(cwd, path string, offset, limit int) string {
	resolved, err := resolveWithin(cwd, path)
	if err != nil {
		return err.Error()
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err.Error()
	}
	if info.IsDir() {
		return listDirectoryForRead(resolved)
	}
	if info.Size() > 2*1024*1024 {
		return readNumberedLinesStream(resolved, offset, limit)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return err.Error()
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "binary file cannot be read as text"
	}
	text := string(data)
	lines := strings.Split(text, "\n")
	if offset <= 0 {
		offset = 1
	}
	start := offset - 1
	if start >= len(lines) {
		return ""
	}
	end := len(lines)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	out := formatNumberedLines(lines[start:end], start+1)
	if len(out) > 100000 {
		out = out[:100000] + "\n[truncated]"
	}
	if out == "" {
		return "(empty file)"
	}
	return out
}

func readNumberedLinesStream(path string, offset, limit int) string {
	if offset <= 0 {
		offset = 1
	}
	if limit == 0 {
		limit = 2000
	}
	file, err := os.Open(path)
	if err != nil {
		return err.Error()
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lines := []string{}
	lineNo := 0
	truncated := false
	chars := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.ContainsRune(line, '\x00') {
			return "binary file cannot be read as text"
		}
		if lineNo < offset {
			continue
		}
		lines = append(lines, line)
		chars += len(line)
		if limit > 0 && len(lines) >= limit {
			truncated = true
			break
		}
		if chars > 100000 {
			truncated = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return err.Error()
	}
	out := formatNumberedLines(lines, offset)
	if out == "" {
		return "(no lines in requested range)"
	}
	if len(out) > 100000 {
		out = out[:100000]
		truncated = true
	}
	if truncated {
		out += "\n[truncated]"
	}
	return out
}

func formatNumberedLines(lines []string, startLine int) string {
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		out = append(out, fmt.Sprintf("%6d│%s", startLine+i, line))
	}
	return strings.Join(out, "\n")
}

func listDirectoryForRead(path string) string {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err.Error()
	}
	lines := []string{"Directory: " + path}
	limit := len(entries)
	truncated := false
	if limit > 200 {
		limit = 200
		truncated = true
	}
	for _, entry := range entries[:limit] {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		lines = append(lines, name)
	}
	if truncated {
		lines = append(lines, "[truncated]")
	}
	return strings.Join(lines, "\n")
}

func toolWrite(cwd, path, content string) string {
	resolved, err := resolveWithin(cwd, path)
	if err != nil {
		return err.Error()
	}
	if len([]byte(content)) > maxToolWriteBytes {
		return fmt.Sprintf("content is too large to write (%d bytes; max %d bytes)", len([]byte(content)), maxToolWriteBytes)
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return err.Error()
	}
	if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
		return err.Error()
	}
	return "wrote " + resolved
}

func toolEdit(cwd, path, oldString, newString string, replaceAll bool) string {
	resolved, err := resolveWithin(cwd, path)
	if err != nil {
		return err.Error()
	}
	if len([]byte(newString)) > maxToolWriteBytes {
		return fmt.Sprintf("new_string is too large to write (%d bytes; max %d bytes)", len([]byte(newString)), maxToolWriteBytes)
	}
	if oldString == "" {
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return err.Error()
		}
		if err := os.WriteFile(resolved, []byte(newString), 0o644); err != nil {
			return err.Error()
		}
		return "created " + resolved
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err.Error()
	}
	if info.Size() > maxToolEditFileBytes {
		return fmt.Sprintf("file is too large to edit (%d bytes; max %d bytes)", info.Size(), maxToolEditFileBytes)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return err.Error()
	}
	text := string(data)
	if oldString == newString {
		return "No changes to apply: old_string and new_string are identical."
	}
	count := strings.Count(text, oldString)
	if count == 0 {
		return "old_string not found"
	}
	if count > 1 && !replaceAll {
		return "Found multiple matches for old_string. Provide more surrounding context to make the match unique, or use replace_all."
	}
	replaceCount := 1
	targetReplacements := 1
	if replaceAll {
		replaceCount = -1
		targetReplacements = count
	}
	updatedBytes := len(data) - targetReplacements*len([]byte(oldString)) + targetReplacements*len([]byte(newString))
	if updatedBytes > maxToolWriteBytes {
		return fmt.Sprintf("edited content would be too large (%d bytes; max %d bytes)", updatedBytes, maxToolWriteBytes)
	}
	updated := strings.Replace(text, oldString, newString, replaceCount)
	if err := os.WriteFile(resolved, []byte(updated), 0o644); err != nil {
		return err.Error()
	}
	if replaceAll {
		return fmt.Sprintf("edited %s (%d replacements)", resolved, count)
	}
	return "edited " + resolved
}

func toolGlob(cwd, pattern, searchPath string) string {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		pattern = "*"
	}
	root, err := resolveOptionalPathWithin(cwd, searchPath)
	if err != nil {
		return err.Error()
	}
	info, err := os.Stat(root)
	if err != nil {
		return err.Error()
	}
	if !info.IsDir() {
		return "path must be a directory"
	}
	matches := []string{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if globMatch(pattern, rel) {
			matches = append(matches, rel)
			if len(matches) >= 500 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if len(matches) == 0 {
		return "No matches found"
	}
	out := strings.Join(matches, "\n")
	if len(matches) >= 500 {
		out += "\n(limited to 500)"
	}
	return out
}

func globMatch(pattern, rel string) bool {
	candidates := []string{rel}
	if !strings.Contains(pattern, "/") {
		candidates = append(candidates, filepath.Base(rel))
	}
	for _, candidate := range candidates {
		if ok, _ := filepath.Match(pattern, candidate); ok {
			return true
		}
		if strings.Contains(pattern, "**") && globDoubleStarMatch(pattern, candidate) {
			return true
		}
	}
	return false
}

func globDoubleStarMatch(pattern, rel string) bool {
	escaped := regexp.QuoteMeta(pattern)
	escaped = strings.ReplaceAll(escaped, `\*\*/`, `(?:.*/)?`)
	escaped = strings.ReplaceAll(escaped, `\*\*`, `.*`)
	escaped = strings.ReplaceAll(escaped, `\*`, `[^/]*`)
	escaped = strings.ReplaceAll(escaped, `\?`, `[^/]`)
	re, err := regexp.Compile("^" + escaped + "$")
	if err != nil {
		return false
	}
	return re.MatchString(rel)
}

type grepOptions struct {
	Pattern         string
	Path            string
	Glob            string
	OutputMode      string
	Context         int
	Before          int
	After           int
	ContextAlias    int
	ShowLineNumbers bool
	HeadLimit       int
	Offset          int
	Insensitive     bool
	Type            string
	Multiline       bool
}

var grepTypeExtensions = map[string][]string{
	"c":      {".c", ".h"},
	"cpp":    {".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx"},
	"css":    {".css"},
	"go":     {".go"},
	"html":   {".html", ".htm"},
	"java":   {".java"},
	"js":     {".js", ".jsx", ".mjs", ".cjs"},
	"json":   {".json"},
	"md":     {".md", ".markdown"},
	"php":    {".php"},
	"py":     {".py", ".pyw"},
	"python": {".py", ".pyw"},
	"rb":     {".rb"},
	"rs":     {".rs"},
	"rust":   {".rs"},
	"sh":     {".sh", ".bash", ".zsh"},
	"sql":    {".sql"},
	"swift":  {".swift"},
	"toml":   {".toml"},
	"ts":     {".ts", ".tsx"},
	"tsx":    {".tsx"},
	"txt":    {".txt"},
	"xml":    {".xml"},
	"yaml":   {".yaml", ".yml"},
	"yml":    {".yaml", ".yml"},
}

func grepTypeMatches(typeName, path string) bool {
	if typeName == "" {
		return true
	}
	extensions, ok := grepTypeExtensions[typeName]
	if !ok {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	for _, candidate := range extensions {
		if ext == candidate {
			return true
		}
	}
	return false
}

func toolGrep(cwd string, opts grepOptions) string {
	pattern := opts.Pattern
	if strings.TrimSpace(pattern) == "" {
		return "pattern is required"
	}
	flags := ""
	if opts.Insensitive {
		flags += "i"
	}
	if opts.Multiline {
		flags += "s"
	}
	if flags != "" {
		pattern = "(?" + flags + ")" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err.Error()
	}
	typeName := strings.ToLower(strings.TrimSpace(opts.Type))
	if typeName != "" {
		if _, ok := grepTypeExtensions[typeName]; !ok {
			return "unsupported Grep type: " + typeName
		}
	}
	root, err := resolveOptionalPathWithin(cwd, opts.Path)
	if err != nil {
		return err.Error()
	}
	info, err := os.Stat(root)
	if err != nil {
		return err.Error()
	}
	walkRoot := root
	relRoot := root
	if !info.IsDir() {
		relRoot = filepath.Dir(root)
	}
	mode := strings.TrimSpace(opts.OutputMode)
	if mode == "" {
		mode = "files_with_matches"
	}
	if opts.HeadLimit <= 0 || opts.HeadLimit > 1000 {
		opts.HeadLimit = 200
	}
	if opts.Offset < 0 {
		opts.Offset = 0
	}
	before, after := grepContextBounds(opts)
	collectLimit := opts.HeadLimit + opts.Offset
	outputs := []string{}
	_ = filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(relRoot, path)
		rel = filepath.ToSlash(rel)
		if opts.Glob != "" && !globMatch(filepath.ToSlash(opts.Glob), rel) {
			return nil
		}
		if !grepTypeMatches(typeName, path) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 1*1024*1024 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || bytes.IndexByte(data, 0) >= 0 {
			return nil
		}
		text := string(data)
		fileLines := strings.Split(text, "\n")
		matchLines := []int{}
		matchCount := 0
		if opts.Multiline {
			matchRanges := re.FindAllStringIndex(text, -1)
			matchCount = len(matchRanges)
			matchLines = grepMultilineMatchLines(text, matchRanges)
		} else {
			for idx, line := range fileLines {
				if re.MatchString(line) {
					matchLines = append(matchLines, idx)
				}
			}
			matchCount = len(matchLines)
		}
		if len(matchLines) == 0 {
			return nil
		}
		switch mode {
		case "files_with_matches":
			outputs = append(outputs, rel)
		case "count":
			outputs = append(outputs, fmt.Sprintf("%s:%d", rel, matchCount))
		default:
			seen := map[int]bool{}
			for _, idx := range matchLines {
				start := idx - before
				if start < 0 {
					start = 0
				}
				end := idx + after
				if end >= len(fileLines) {
					end = len(fileLines) - 1
				}
				for lineNo := start; lineNo <= end; lineNo++ {
					if seen[lineNo] {
						continue
					}
					seen[lineNo] = true
					if opts.ShowLineNumbers {
						outputs = append(outputs, fmt.Sprintf("%s:%d:%s", rel, lineNo+1, fileLines[lineNo]))
					} else {
						outputs = append(outputs, fmt.Sprintf("%s:%s", rel, fileLines[lineNo]))
					}
				}
			}
		}
		if len(outputs) >= collectLimit {
			return filepath.SkipAll
		}
		return nil
	})
	if opts.Offset > 0 {
		if opts.Offset >= len(outputs) {
			return "No matches found"
		}
		outputs = outputs[opts.Offset:]
	}
	if len(outputs) > opts.HeadLimit {
		outputs = outputs[:opts.HeadLimit]
	}
	if len(outputs) == 0 {
		return "No matches found"
	}
	return strings.Join(outputs, "\n")
}

func grepMultilineMatchLines(text string, ranges [][]int) []int {
	if len(ranges) == 0 {
		return nil
	}
	seen := map[int]bool{}
	lines := []int{}
	for _, matchRange := range ranges {
		if len(matchRange) != 2 {
			continue
		}
		start, end := matchRange[0], matchRange[1]
		if start < 0 {
			start = 0
		}
		if end < start {
			end = start
		}
		if end > len(text) {
			end = len(text)
		}
		startLine := strings.Count(text[:start], "\n")
		endLine := startLine
		if end > start {
			endLine = strings.Count(text[:end], "\n")
		}
		for line := startLine; line <= endLine; line++ {
			if seen[line] {
				continue
			}
			seen[line] = true
			lines = append(lines, line)
		}
	}
	sort.Ints(lines)
	return lines
}

func grepContextBounds(opts grepOptions) (int, int) {
	contextLines := opts.Context
	if opts.ContextAlias >= 0 {
		contextLines = opts.ContextAlias
	}
	if contextLines < 0 {
		contextLines = 0
	}
	if contextLines > 20 {
		contextLines = 20
	}
	before, after := contextLines, contextLines
	if opts.Before >= 0 || opts.After >= 0 {
		before, after = 0, 0
		if opts.Before >= 0 {
			before = opts.Before
		}
		if opts.After >= 0 {
			after = opts.After
		}
	}
	if before > 20 {
		before = 20
	}
	if after > 20 {
		after = 20
	}
	return before, after
}

func toolWebSearch(ctx context.Context, query string, allowedDomains, blockedDomains []string) string {
	query = strings.TrimSpace(query)
	if len(query) < 2 {
		return "query must be at least 2 characters"
	}
	searchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	endpoint := "https://api.duckduckgo.com/?" + url.Values{
		"q":             []string{query},
		"format":        []string{"json"},
		"no_html":       []string{"1"},
		"skip_disambig": []string{"1"},
		"no_redirect":   []string{"1"},
		"t":             []string{"narrafork-go"},
	}.Encode()
	req, err := http.NewRequestWithContext(searchCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err.Error()
	}
	req.Header.Set("User-Agent", "NarraFork-Go-Agent/1.0")
	res, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "Web search failed: " + err.Error()
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Sprintf("Web search failed: status %s", res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 512*1024))
	if err != nil {
		return err.Error()
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return "Web search response was not valid JSON: " + err.Error()
	}
	results := duckSearchResults(payload)
	filtered := []map[string]string{}
	for _, result := range results {
		if domainMatches(result["url"], allowedDomains, blockedDomains) {
			filtered = append(filtered, result)
		}
		if len(filtered) >= 8 {
			break
		}
	}
	if len(filtered) == 0 {
		return "No results found"
	}
	lines := []string{"Search results for: " + query, ""}
	sources := []string{}
	for i, result := range filtered {
		title := firstNonEmpty(result["title"], result["url"])
		lines = append(lines, fmt.Sprintf("%d. [%s](%s)", i+1, title, result["url"]))
		if snippet := strings.TrimSpace(result["snippet"]); snippet != "" {
			lines = append(lines, "   "+snippet)
		}
		sources = append(sources, fmt.Sprintf("- [%s](%s)", title, result["url"]))
	}
	lines = append(lines, "", "Sources:")
	lines = append(lines, sources...)
	return strings.Join(lines, "\n")
}

func duckSearchResults(payload map[string]any) []map[string]string {
	results := []map[string]string{}
	if urlValue := strings.TrimSpace(stringValue(payload["AbstractURL"])); urlValue != "" {
		results = append(results, map[string]string{"title": firstNonEmpty(stringValue(payload["Heading"]), urlValue), "url": urlValue, "snippet": stringValue(payload["AbstractText"])})
	}
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			if topics, ok := typed["Topics"]; ok {
				walk(topics)
				return
			}
			urlValue := strings.TrimSpace(stringValue(typed["FirstURL"]))
			text := strings.TrimSpace(stringValue(typed["Text"]))
			if urlValue != "" && text != "" {
				title := text
				if idx := strings.Index(text, " - "); idx > 0 {
					title = text[:idx]
				}
				results = append(results, map[string]string{"title": title, "url": urlValue, "snippet": text})
			}
		}
	}
	walk(payload["Results"])
	walk(payload["RelatedTopics"])
	return dedupeSearchResults(results)
}

func dedupeSearchResults(results []map[string]string) []map[string]string {
	seen := map[string]bool{}
	out := []map[string]string{}
	for _, result := range results {
		urlValue := strings.TrimSpace(result["url"])
		if urlValue == "" || seen[urlValue] {
			continue
		}
		seen[urlValue] = true
		out = append(out, result)
	}
	return out
}

func domainMatches(rawURL string, allowedDomains, blockedDomains []string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	if host == "" {
		return false
	}
	for _, domain := range blockedDomains {
		if domainSuffixMatch(host, domain) {
			return false
		}
	}
	if len(allowedDomains) == 0 {
		return true
	}
	for _, domain := range allowedDomains {
		if domainSuffixMatch(host, domain) {
			return true
		}
	}
	return false
}

func domainSuffixMatch(host, domain string) bool {
	domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), "www.")
	if domain == "" {
		return false
	}
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func toolWebFetch(ctx context.Context, rawURL string, maxLength int) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "url is required"
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "invalid URL"
	}
	if maxLength <= 0 || maxLength > 200000 {
		maxLength = 20000
	}
	if parsed.Scheme == "data" {
		return toolWebFetchDataURL(rawURL, maxLength)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "only http, https, and data URLs are supported"
	}
	if parsed.Host == "" {
		return "invalid URL"
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err.Error()
	}
	req.Header.Set("User-Agent", "NarraFork-Go-Agent/1.0")
	res, err := webFetchHTTPClient(ctx).Do(req)
	if err != nil {
		return err.Error()
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, int64(maxLength)+1))
	if err != nil {
		return err.Error()
	}
	truncated := len(data) > maxLength
	if truncated {
		data = data[:maxLength]
	}
	parts := []string{
		"URL: " + parsed.String(),
		"Status: " + res.Status,
	}
	if contentType := res.Header.Get("Content-Type"); contentType != "" {
		parts = append(parts, "Content-Type: "+contentType)
	}
	parts = append(parts, "", string(data))
	if truncated {
		parts = append(parts, "\n[truncated]")
	}
	return strings.Join(parts, "\n")
}

func toolWebFetchDataURL(rawURL string, maxLength int) string {
	payload := strings.TrimPrefix(rawURL, "data:")
	header, dataPart, ok := strings.Cut(payload, ",")
	if !ok {
		return "invalid data URL"
	}
	contentType, base64Encoded := parseDataURLHeader(header)
	var data []byte
	if base64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataPart))
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(dataPart))
			if err != nil {
				return "invalid data URL: " + err.Error()
			}
		}
		data = decoded
	} else {
		decoded, err := url.PathUnescape(dataPart)
		if err != nil {
			return "invalid data URL: " + err.Error()
		}
		data = []byte(decoded)
	}
	truncated := len(data) > maxLength
	if truncated {
		data = data[:maxLength]
	}
	parts := []string{
		"URL: data:[inline]",
		"Status: 200 OK",
		"Content-Type: " + contentType,
		"",
		string(data),
	}
	if truncated {
		parts = append(parts, "\n[truncated]")
	}
	return strings.Join(parts, "\n")
}

func parseDataURLHeader(header string) (string, bool) {
	if header == "" {
		return "text/plain;charset=US-ASCII", false
	}
	parts := strings.Split(header, ";")
	contentType := strings.TrimSpace(parts[0])
	params := []string{}
	base64Encoded := false
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		if strings.EqualFold(part, "base64") {
			base64Encoded = true
			continue
		}
		if part != "" {
			params = append(params, part)
		}
	}
	if contentType == "" {
		if len(params) > 0 {
			contentType = "text/plain"
		} else {
			contentType = "text/plain;charset=US-ASCII"
		}
	}
	if len(params) > 0 {
		contentType += ";" + strings.Join(params, ";")
	}
	return contentType, base64Encoded
}

func webFetchHTTPClient(ctx context.Context) *http.Client {
	settings, _ := ctx.Value(toolRuntimeSettingsContextKey{}).(ToolRuntimeSettings)
	mode := strings.ToLower(strings.TrimSpace(settings.WebFetchProxy.Mode))
	if mode == "" {
		mode = "system"
	}
	var proxy func(*http.Request) (*url.URL, error)
	switch mode {
	case "direct":
		proxy = nil
	case "custom":
		proxyURL := strings.TrimSpace(settings.WebFetchProxy.URL)
		if proxyURL != "" {
			if parsed, err := url.Parse(proxyURL); err == nil && parsed.Scheme != "" && parsed.Host != "" {
				proxy = http.ProxyURL(parsed)
			}
		}
	default:
		proxy = http.ProxyFromEnvironment
	}
	if mode == "system" || mode == "" {
		return &http.Client{Timeout: 20 * time.Second}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = proxy
	return &http.Client{Timeout: 20 * time.Second, Transport: transport}
}

func toolShareFile(cwd string, rawPath any, compress bool, preview bool) string {
	inputPaths := inputPathList(rawPath)
	if len(inputPaths) == 0 {
		return "path is required"
	}
	resolved := []string{}
	for _, inputPath := range inputPaths {
		path, err := resolveWithin(cwd, inputPath)
		if err != nil {
			return err.Error()
		}
		resolved = append(resolved, path)
	}
	if _, err := shareSourceSize(resolved); err != nil {
		return err.Error()
	}
	shareID := id.Must(12)
	dir := filepath.Join(sharesRoot(), shareID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err.Error()
	}
	var dest string
	compressed := compress || len(resolved) > 1
	if len(resolved) > 1 {
		dest = filepath.Join(dir, "shared-files.zip")
		if err := createZipArchive(dest, resolved); err != nil {
			return err.Error()
		}
		compressed = true
	} else {
		info, err := os.Stat(resolved[0])
		if err != nil {
			return err.Error()
		}
		if info.IsDir() {
			dest = filepath.Join(dir, filepath.Base(resolved[0])+".tar.gz")
			if err := createTarGzArchive(dest, resolved[0]); err != nil {
				return err.Error()
			}
			compressed = true
		} else if compress {
			dest = filepath.Join(dir, filepath.Base(resolved[0])+".gz")
			if err := gzipFile(dest, resolved[0]); err != nil {
				return err.Error()
			}
		} else {
			dest = filepath.Join(dir, filepath.Base(resolved[0]))
			if err := copyFile(dest, resolved[0]); err != nil {
				return err.Error()
			}
		}
	}
	info, err := os.Stat(dest)
	if err != nil {
		return err.Error()
	}
	fileName := filepath.Base(dest)
	expiresAt := time.Now().Add(defaultToolShareExpiry)
	if err := writeShareMetadata(shareDiskMetadata{
		ID:           shareID,
		OriginalName: fileName,
		FileName:     fileName,
		SizeBytes:    info.Size(),
		ExpiresAt:    expiresAt.Format(time.RFC3339Nano),
		CreatedBy:    "agent",
	}); err != nil {
		removeShareDisk(shareID)
		return err.Error()
	}
	payload := map[string]any{
		"id":         shareID,
		"url":        "/api/shares/" + shareID,
		"fileName":   fileName,
		"sizeBytes":  info.Size(),
		"compressed": compressed,
		"expiresAt":  expiresAt.Format(time.RFC3339),
	}
	if preview && !compressed && len(resolved) == 1 {
		if previewType := sharePreviewType(fileName); previewType != "" {
			payload["preview"] = true
			payload["previewType"] = previewType
			payload["previewUrl"] = "/api/shares/" + shareID + "/preview"
		}
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return string(data)
}

func sharePreviewType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".avif", ".bmp", ".ico":
		return "image"
	case ".mp4", ".webm", ".mov", ".ogg":
		return "video"
	case ".pdf":
		return "pdf"
	case ".html", ".htm":
		return "html"
	default:
		return ""
	}
}

func inputPathList(raw any) []string {
	if path := strings.TrimSpace(stringValue(raw)); path != "" {
		return []string{path}
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	paths := []string{}
	for _, item := range items {
		if path := strings.TrimSpace(stringValue(item)); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func shareSourceSize(paths []string) (int64, error) {
	var total int64
	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil {
			return 0, err
		}
		if !info.IsDir() {
			total += info.Size()
			if total > maxToolShareSourceBytes {
				return total, errors.New("source size exceeds 512MB ShareFile limit")
			}
			continue
		}
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("symlink paths cannot be shared")
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
			if total > maxToolShareSourceBytes {
				return errors.New("source size exceeds 512MB ShareFile limit")
			}
			return nil
		}); err != nil {
			return total, err
		}
	}
	return total, nil
}

func copyFile(dest, src string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.Size() > maxToolShareSourceBytes {
		return errors.New("file exceeds 512MB ShareFile limit")
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func gzipFile(dest, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	if _, err := io.Copy(gz, in); err != nil {
		_ = gz.Close()
		return err
	}
	return gz.Close()
}

func createTarGzArchive(dest, root string) error {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	base := filepath.Dir(root)
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink paths cannot be shared")
		}
		return addFileToTar(tw, base, path)
	})
}

func addFileToTar(tw *tar.Writer, base, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return err
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = filepath.ToSlash(rel)
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	_, err = io.Copy(tw, in)
	return err
}

func createZipArchive(dest string, paths []string) error {
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	defer zw.Close()
	for _, root := range paths {
		base := filepath.Dir(root)
		info, err := os.Stat(root)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			if err := addFileToZip(zw, base, root); err != nil {
				return err
			}
			continue
		}
		if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("symlink paths cannot be shared")
			}
			return addFileToZip(zw, base, path)
		}); err != nil {
			return err
		}
	}
	return nil
}

func addFileToZip(zw *zip.Writer, base, path string) error {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return err
	}
	writer, err := zw.Create(filepath.ToSlash(rel))
	if err != nil {
		return err
	}
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	_, err = io.Copy(writer, in)
	return err
}

func sharesRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".narrafork", "shares")
}

func shareMetadataPath(shareID string) string {
	return filepath.Join(sharesRoot(), shareID+".json")
}

func writeShareMetadata(metadata shareDiskMetadata) error {
	if !isSafeShareID(metadata.ID) {
		return errors.New("invalid share id")
	}
	if !isSafeShareFileName(metadata.FileName) || !isSafeShareFileName(metadata.OriginalName) {
		return errors.New("invalid share filename")
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	path := shareMetadataPath(metadata.ID)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func isSafeShareID(shareID string) bool {
	trimmed := strings.TrimSpace(shareID)
	return shareID == trimmed && safeShareIDPattern.MatchString(shareID)
}

func isSafeShareFileName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return false
	}
	return filepath.Base(name) == name
}

func removeShareDisk(shareID string) {
	if !isSafeShareID(shareID) {
		return
	}
	_ = os.RemoveAll(filepath.Join(sharesRoot(), shareID))
	_ = os.Remove(shareMetadataPath(shareID))
	_ = os.Remove(shareMetadataPath(shareID) + ".tmp")
}

func resolveOptionalPathWithin(cwd, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		root, err := filepath.Abs(cwd)
		if err != nil {
			return "", err
		}
		if realRoot, err := filepath.EvalSymlinks(root); err == nil {
			return realRoot, nil
		}
		return root, nil
	}
	return resolveWithin(cwd, strings.TrimSpace(path))
}

func resolveWithin(cwd, path string) (string, error) {
	if path == "" {
		return "", errors.New("file_path is required")
	}
	root, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if !pathWithinBase(root, resolved) {
		return "", errors.New("path escapes working directory")
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realResolved, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		realAncestor, ancestorErr := evalExistingAncestor(resolved)
		if ancestorErr != nil {
			return "", err
		}
		if !pathWithinBase(realRoot, realAncestor) {
			return "", errors.New("path escapes working directory")
		}
		return resolved, nil
	}
	if !pathWithinBase(realRoot, realResolved) {
		return "", errors.New("path escapes working directory")
	}
	return realResolved, nil
}

func evalExistingAncestor(path string) (string, error) {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
				return filepath.EvalSymlinks(current)
			}
			return filepath.EvalSymlinks(filepath.Dir(current))
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		current = parent
	}
}

func pathWithinBase(baseAbs, targetAbs string) bool {
	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel))
}

type cappedBuffer struct {
	limit     int
	buf       bytes.Buffer
	truncated bool
	onWrite   func(string)
	mu        sync.Mutex
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	var current string
	b.mu.Lock()
	if b.limit <= 0 {
		b.truncated = true
		current = b.buf.String()
		b.mu.Unlock()
		if b.onWrite != nil {
			b.onWrite(current)
		}
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		current = b.buf.String()
		b.mu.Unlock()
		if b.onWrite != nil {
			b.onWrite(current)
		}
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		current = b.buf.String()
		b.mu.Unlock()
		if b.onWrite != nil {
			b.onWrite(current)
		}
		return len(p), nil
	}
	_, err := b.buf.Write(p)
	current = b.buf.String()
	b.mu.Unlock()
	if b.onWrite != nil {
		b.onWrite(current)
	}
	return len(p), err
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *cappedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

func stringValue(v any) string {
	switch typed := v.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func pathValue(v any) string {
	return strings.TrimSpace(stringValue(v))
}

func intValue(v any, fallback int) int {
	switch typed := v.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	default:
		return fallback
	}
}

func boolValue(v any) bool {
	switch typed := v.(type) {
	case bool:
		return typed
	default:
		return false
	}
}

func stringList(v any) []string {
	switch typed := v.(type) {
	case []string:
		return typed
	case []any:
		out := []string{}
		for _, item := range typed {
			if value := strings.TrimSpace(stringValue(item)); value != "" {
				out = append(out, value)
			}
		}
		return out
	default:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func splitModel(model string) (string, string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "openai", ""
	}
	if idx := strings.Index(model, ":"); idx > 0 {
		prefix := strings.TrimSpace(model[:idx])
		bare := strings.TrimSpace(model[idx+1:])
		if prefix == "" {
			prefix = "openai"
		}
		return prefix, bare
	}
	return "openai", model
}

func readArray(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []string:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}
