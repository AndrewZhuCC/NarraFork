package agentbasic

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
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
	"strings"
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
}

type Provider struct {
	Prefix              string
	Kind                string
	APIKey              string
	BaseURL             string
	Model               string
	OfficialAPI         bool
	APIMode             string
	AccountID           string
	CredentialID        string
	CredentialPool      []Provider
	KiroCredentialsPath string
	KiroConfigPath      string
	PreviousResponseID  string
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

type ToolExecutor func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error)

const (
	DefaultSystemPrompt         = "You are NarraFork Go backend's coding assistant. Use available tools when needed, keep answers concise, and explain important changes."
	defaultMaxTurns             = 4
	maxAllowedTurns             = 200
	defaultMaxChatRetries       = 3
	defaultEmptyResponseRetries = 3
	maxToolWriteBytes           = 2 * 1024 * 1024
	maxToolEditFileBytes        = 2 * 1024 * 1024
)

type generateOptions struct {
	toolExecutor         ToolExecutor
	systemPrompt         string
	maxTurns             int
	reasoningEffort      string
	toolScope            string
	enabledTools         []string
	disabledTools        []string
	emitToolInventory    bool
	maxChatRetries       int
	emptyResponseRetries int
	retryBaseDelay       time.Duration
}

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

func WithEmptyResponseRetries(maxRetries int) GenerateOption {
	return func(opts *generateOptions) {
		opts.emptyResponseRetries = maxRetries
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
		systemPrompt:         DefaultSystemPrompt,
		maxTurns:             defaultMaxTurns,
		toolScope:            "normal",
		maxChatRetries:       defaultMaxChatRetries,
		emptyResponseRetries: defaultEmptyResponseRetries,
		retryBaseDelay:       200 * time.Millisecond,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if strings.TrimSpace(options.systemPrompt) == "" {
		options.systemPrompt = DefaultSystemPrompt
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
	tools := []map[string]any{}
	for _, spec := range visibleToolSpecs(toolScope) {
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
	tools := []map[string]any{}
	for _, spec := range visibleToolSpecs(toolScope) {
		tools = append(tools, map[string]any{
			"name":         spec.Name,
			"description":  spec.Description,
			"input_schema": spec.Parameters,
		})
	}
	return tools
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
		props["feedback"] = stringProp("Actionable feedback describing what remains before completion")
		required = append(required, "feedback")
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
					"command":     stringProp("Command to execute"),
					"description": stringProp("Short description of what the command does"),
					"timeout":     map[string]any{"type": "number", "description": "Timeout in milliseconds, capped at 600000"},
				},
				"required": []string{"command"},
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
				},
				"required": []string{"file_path"},
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
				"required": []string{"file_path", "content"},
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
				"required": []string{"file_path", "old_string", "new_string"},
			},
		},
		{
			Name:        "Glob",
			Description: "List files whose relative path matches a glob pattern such as **/*.go or *.ts.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": stringProp("Pattern such as *.go or internal"),
				},
				"required": []string{"pattern"},
			},
		},
		{
			Name:        "Grep",
			Description: "Search text files with a regex pattern under the working directory.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern":     stringProp("Regex pattern"),
					"glob":        stringProp("Optional glob filter, such as **/*.go"),
					"output_mode": stringProp("content, files_with_matches, or count"),
					"context":     map[string]any{"type": "number", "description": "Lines before and after each match"},
					"head_limit":  map[string]any{"type": "number", "description": "Maximum output entries"},
					"-i":          map[string]any{"type": "boolean", "description": "Case-insensitive search"},
				},
				"required": []string{"pattern"},
			},
		},
		{
			Name:        "WebFetch",
			Description: "Fetch an HTTP/HTTPS URL and return truncated response text.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":        stringProp("HTTP or HTTPS URL to fetch"),
					"max_length": map[string]any{"type": "number", "description": "Maximum response bytes to return"},
				},
				"required": []string{"url"},
			},
		},
		{
			Name:        "WebSearch",
			Description: "Search the web for current information and return result snippets with source URLs.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":           stringProp("Search query"),
					"allowed_domains": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Only include results from these domains"},
					"blocked_domains": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Exclude results from these domains"},
				},
				"required": []string{"query"},
			},
		},
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
				"required": []string{"path"},
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
					"reasoning_effort":  stringProp("Optional reasoning effort"),
					"workdir":           stringProp("Optional working directory under the parent cwd"),
					"alias":             stringProp("Optional alias for later Await calls"),
					"stop":              stringProp("Subagent id or alias to stop"),
					"timeout":           map[string]any{"type": "number", "description": "Foreground wait timeout in milliseconds, capped at 30000"},
					"run_in_background": map[string]any{"type": "boolean", "description": "Run as a background subagent"},
				},
			},
		},
		{
			Name:        "Await",
			Description: "Wait for or inspect a background subagent result.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":    stringProp("agent or bash; Go backend currently supports agent"),
					"id":      stringProp("Subagent id or alias"),
					"timeout": map[string]any{"type": "number", "description": "Milliseconds to wait, capped at 30000"},
				},
				"required": []string{"type", "id"},
			},
		},
		{
			Name:        "TeamStatus",
			Description: "List sibling/child subagents, inspect file changes, or send team messages.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":    stringProp("list, file_changes, broadcast, or send"),
					"target_id": stringProp("Target subagent id/alias/title/prefix for file_changes or send"),
					"message":   stringProp("Message text for broadcast or send"),
				},
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
				"required": []string{"message"},
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
								"multiSelect": map[string]any{"type": "boolean", "description": "Allow selecting multiple options"},
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
										"required": []string{"label", "description"},
									},
								},
							},
							"required": []string{"question", "header", "options", "multiSelect"},
						},
					},
					"metadata": map[string]any{"type": "object", "description": "Optional metadata"},
				},
				"required": []string{"questions"},
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
					"mode":  stringProp("list, search, or get"),
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
								"status":   stringProp("pending, in_progress, or completed"),
								"priority": stringProp("high, medium, or low"),
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
							"required": []string{"severity", "message"},
						},
					},
				},
				"required": []string{"verdict", "findings"},
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
					"format":   stringProp("sections or plain"),
					"maxChars": map[string]any{"type": "number", "description": "Maximum output chars, max 50000"},
				},
			},
		},
		{
			Name:        "EnterPlanMode",
			Description: "Enter NarraFork plan mode for the current narrator. The backend stores the previous permission mode, switches to read-only planning, and creates a designated .narrafork/plan-{id}.md file target for plan drafts.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "ExitPlanMode",
			Description: "Exit NarraFork plan mode after providing a complete implementation plan. If plan is omitted, the backend reads the designated .narrafork/plan-{id}.md file. The plan is validated, reflected on, permissions are restored, and a plan compact is attempted with a documented fallback.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"plan":           stringProp("Complete implementation plan; optional if the designated plan file contains the plan"),
					"allowedPrompts": map[string]any{"type": "array", "description": "Optional command categories the implementation may require"},
				},
			},
		},
		{
			Name:        "GetGoals",
			Description: "Get the current narrator goal list.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "AddGoal",
			Description: "Add a concrete objective to the current narrator goal list.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"objective": stringProp("Concrete objective to add"),
				},
				"required": []string{"objective"},
			},
		},
		{
			Name:        "UpdateGoal",
			Description: "Update a goal status or objective in the current narrator goal list.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"goal_id":    stringProp("Goal ID to update"),
					"status":     stringProp("New status, such as pending, active, paused, or complete"),
					"objective":  stringProp("Optional updated objective"),
					"evidence":   stringProp("Concrete evidence required when marking a goal complete"),
					"reflection": stringProp("Optional explanation for goal completion"),
				},
				"required": []string{"goal_id"},
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

func browserToolSpec() toolSpec {
	stringProp := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	properties := map[string]any{
		"action":          stringProp("launch, navigate, dom, get_text, click, fill, select, type, hover, wait, scroll, get_attribute, get_console, get_network, network_start, network_stop, evaluate, evaluate_capture, screenshot, set_ttl, list_sessions, perf_start, perf_stop, or close"),
		"url":             stringProp("HTTP, HTTPS, or data URL for launch/navigate"),
		"session_id":      stringProp("Browser session ID"),
		"selector":        stringProp("CSS selector for element actions"),
		"value":           stringProp("Value for fill/select/type/evaluate/get_attribute/filter actions"),
		"key":             stringProp("Keyboard key for type, for example Enter or Escape"),
		"state":           stringProp("Wait state: attached, visible, or hidden"),
		"direction":       stringProp("Navigation or scroll direction: back, forward, up, down, left, right"),
		"amount":          map[string]any{"type": "number", "description": "Scroll amount in pixels"},
		"timeout":         map[string]any{"type": "number", "description": "Action timeout in milliseconds"},
		"ttl_ms":          map[string]any{"type": "number", "description": "Session inactivity TTL in milliseconds"},
		"max_length":      map[string]any{"type": "number", "description": "Maximum output length"},
		"file_path":       stringProp("Path under the working directory for screenshot or trace output"),
		"full_page":       map[string]any{"type": "boolean", "description": "Capture the full page screenshot"},
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
			"type":       "object",
			"properties": properties,
			"required":   []string{"action"},
		},
	}
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
		return toolBash(ctx, cwd, stringValue(args["command"]), intValue(args["timeout"], 30000)), nil
	case "Read":
		return toolRead(cwd, stringValue(args["file_path"]), intValue(args["offset"], 1), intValue(args["limit"], 0)), nil
	case "Write":
		return toolWrite(cwd, pathValue(args["file_path"]), stringValue(args["content"])), nil
	case "Edit":
		return toolEdit(cwd, pathValue(args["file_path"]), stringValue(args["old_string"]), stringValue(args["new_string"]), boolValue(args["replace_all"])), nil
	case "Glob":
		return toolGlob(cwd, stringValue(args["pattern"])), nil
	case "Grep":
		return toolGrep(cwd, grepOptions{
			Pattern:     stringValue(args["pattern"]),
			Glob:        stringValue(args["glob"]),
			OutputMode:  stringValue(args["output_mode"]),
			Context:     intValue(args["context"], 0),
			HeadLimit:   intValue(args["head_limit"], 200),
			Insensitive: boolValue(args["-i"]) || boolValue(args["i"]) || boolValue(args["case_insensitive"]),
		}), nil
	case "WebFetch":
		return toolWebFetch(ctx, stringValue(args["url"]), intValue(args["max_length"], 20000)), nil
	case "WebSearch":
		return toolWebSearch(ctx, stringValue(args["query"]), stringList(args["allowed_domains"]), stringList(args["blocked_domains"])), nil
	case "ShareFile":
		return toolShareFile(cwd, args["path"], boolValue(args["compress"]), boolValue(args["preview"])), nil
	case "Browser":
		return toolBrowser(ctx, cwd, args), nil
	case "Agent", "Await", "TeamStatus", "Send", "AskUserQuestion", "Skill", "LearningGuide", "TaskCreate", "StartPipeline", "EndPipeline", "EnterPlanMode", "ExitPlanMode", "GetGoals", "AddGoal", "UpdateGoal":
		return name + " requires narrator context and cannot run outside a narrator session", nil
	default:
		return "unknown tool: " + name, nil
	}
}

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
	buf := &cappedBuffer{limit: 64 * 1024}
	cmd.Stdout = buf
	cmd.Stderr = buf
	err := cmd.Run()
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

func toolGlob(cwd, pattern string) string {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" {
		pattern = "*"
	}
	matches := []string{}
	_ = filepath.WalkDir(cwd, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(cwd, path)
		rel = filepath.ToSlash(rel)
		if globMatch(pattern, rel) {
			matches = append(matches, rel)
			if len(matches) >= 200 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	data, _ := json.MarshalIndent(matches, "", "  ")
	return string(data)
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
	Pattern     string
	Glob        string
	OutputMode  string
	Context     int
	HeadLimit   int
	Insensitive bool
}

func toolGrep(cwd string, opts grepOptions) string {
	pattern := opts.Pattern
	if strings.TrimSpace(pattern) == "" {
		return "pattern is required"
	}
	if opts.Insensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return err.Error()
	}
	mode := strings.TrimSpace(opts.OutputMode)
	if mode == "" {
		mode = "content"
	}
	if opts.HeadLimit <= 0 || opts.HeadLimit > 1000 {
		opts.HeadLimit = 200
	}
	if opts.Context < 0 {
		opts.Context = 0
	}
	if opts.Context > 20 {
		opts.Context = 20
	}
	outputs := []string{}
	_ = filepath.WalkDir(cwd, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(cwd, path)
		rel = filepath.ToSlash(rel)
		if opts.Glob != "" && !globMatch(filepath.ToSlash(opts.Glob), rel) {
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
		fileLines := strings.Split(string(data), "\n")
		matchLines := []int{}
		for idx, line := range fileLines {
			if re.MatchString(line) {
				matchLines = append(matchLines, idx)
			}
		}
		if len(matchLines) == 0 {
			return nil
		}
		switch mode {
		case "files_with_matches":
			outputs = append(outputs, rel)
		case "count":
			outputs = append(outputs, fmt.Sprintf("%s:%d", rel, len(matchLines)))
		default:
			seen := map[int]bool{}
			for _, idx := range matchLines {
				start := idx - opts.Context
				if start < 0 {
					start = 0
				}
				end := idx + opts.Context
				if end >= len(fileLines) {
					end = len(fileLines) - 1
				}
				for lineNo := start; lineNo <= end; lineNo++ {
					if seen[lineNo] {
						continue
					}
					seen[lineNo] = true
					outputs = append(outputs, fmt.Sprintf("%s:%d:%s", rel, lineNo+1, fileLines[lineNo]))
				}
			}
		}
		if len(outputs) >= opts.HeadLimit {
			return filepath.SkipAll
		}
		return nil
	})
	if len(outputs) > opts.HeadLimit {
		outputs = outputs[:opts.HeadLimit]
	}
	return strings.Join(outputs, "\n")
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
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "only http and https URLs are supported"
	}
	if parsed.Host == "" {
		return "invalid URL"
	}
	if maxLength <= 0 || maxLength > 200000 {
		maxLength = 20000
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err.Error()
	}
	req.Header.Set("User-Agent", "NarraFork-Go-Agent/1.0")
	res, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
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
	payload := map[string]any{
		"id":         shareID,
		"url":        "/api/shares/" + shareID,
		"fileName":   filepath.Base(dest),
		"sizeBytes":  info.Size(),
		"compressed": compressed,
	}
	if preview && !compressed && len(resolved) == 1 {
		payload["previewUrl"] = "/api/shares/" + shareID + "/preview"
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	return string(data)
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

func copyFile(dest, src string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.Size() > 512*1024*1024 {
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

func resolveWithin(cwd, path string) (string, error) {
	if path == "" {
		return "", errors.New("file_path is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root, _ := filepath.Abs(cwd)
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("path escapes working directory")
	}
	return resolved, nil
}

type cappedBuffer struct {
	limit     int
	buf       bytes.Buffer
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		b.truncated = true
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *cappedBuffer) String() string {
	return b.buf.String()
}

func (b *cappedBuffer) Truncated() bool {
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
