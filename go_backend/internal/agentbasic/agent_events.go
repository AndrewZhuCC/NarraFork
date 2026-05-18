package agentbasic

import "encoding/json"

type AgentEvent struct {
	Type         string         `json:"type"`
	Text         string         `json:"text,omitempty"`
	OutputIndex  *int           `json:"outputIndex,omitempty"`
	ToolUseID    string         `json:"toolUseId,omitempty"`
	ToolName     string         `json:"toolName,omitempty"`
	Input        map[string]any `json:"input,omitempty"`
	Output       string         `json:"output,omitempty"`
	IsError      bool           `json:"isError,omitempty"`
	DurationMs   int64          `json:"durationMs,omitempty"`
	TurnIndex    int            `json:"turnIndex,omitempty"`
	Block        *ContentBlock  `json:"block,omitempty"`
	Message      string         `json:"message,omitempty"`
	Code         string         `json:"code,omitempty"`
	Reason       string         `json:"reason,omitempty"`
	Attempt      int            `json:"attempt,omitempty"`
	MaxRetries   int            `json:"maxRetries,omitempty"`
	DelayMs      int64          `json:"delayMs,omitempty"`
	Percentage   float64        `json:"percentage,omitempty"`
	Usage        *UsageSnapshot `json:"usage,omitempty"`
	CredentialID string         `json:"credentialId,omitempty"`
	RequestID    string         `json:"requestId,omitempty"`
	Provider     string         `json:"provider,omitempty"`
	Model        string         `json:"model,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	ToolUses     []ToolCall     `json:"toolUses,omitempty"`
	ToolRun      *ToolRun       `json:"-"`
	Streaming    *ToolUseChunk  `json:"streaming,omitempty"`
}

type ContentBlock struct {
	Type             string         `json:"type"`
	Text             string         `json:"text,omitempty"`
	ToolUseID        string         `json:"toolUseId,omitempty"`
	ToolName         string         `json:"toolName,omitempty"`
	Input            map[string]any `json:"input,omitempty"`
	Output           string         `json:"output,omitempty"`
	IsError          bool           `json:"isError,omitempty"`
	OutputIndex      *int           `json:"outputIndex,omitempty"`
	ProviderMetadata map[string]any `json:"providerMetadata,omitempty"`
	Data             string         `json:"data,omitempty"`
	ID               string         `json:"id,omitempty"`
	Status           string         `json:"status,omitempty"`
	Query            string         `json:"query,omitempty"`
	Queries          []string       `json:"queries,omitempty"`
	RevisedPrompt    string         `json:"revisedPrompt,omitempty"`
	Result           string         `json:"result,omitempty"`
}

func toolCallInput(call ToolCall) map[string]any {
	input := map[string]any{}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &input); err != nil {
		return map[string]any{"_raw": call.Function.Arguments}
	}
	return input
}

func newFunctionToolCall(id, name, arguments string) ToolCall {
	var call ToolCall
	call.ID = id
	call.Type = "function"
	call.Function.Name = name
	if arguments == "" {
		arguments = "{}"
	}
	call.Function.Arguments = arguments
	return call
}
