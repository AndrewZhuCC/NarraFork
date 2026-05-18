package agentbasic

import "context"

// ProviderAdapter is the Go-side equivalent of the TypeScript ProviderAdapter.
// It isolates provider-specific history, tool formatting, and chat transport details
// from the agent loop.
type ProviderAdapter interface {
	ProviderName() string
	FormatTools(toolScope string) []map[string]any
	BuildHistory(messages []Message, model string, narratorID string) (BuiltHistory, error)
	InjectSystemPrompt(history *BuiltHistory, systemPrompt string, model string, locale string)
	Chat(ctx context.Context, params ChatParams) (<-chan StreamEvent, error)
	FormatToolResult(toolUseID string, output string, isError bool) any
	PushUserTurn(history *BuiltHistory, content string, toolResults []any)
	PushAssistantTurn(history *BuiltHistory, text string, toolUses []ToolCall, rawContent any)
}

type ProviderResolution struct {
	RequestedProvider string
	RequestedModel    string
	Provider          string
	Model             string
	Config            Provider
	Adapter           ProviderAdapter
}

type BuiltHistory struct {
	History             []any
	TrailingToolResults []any
	TrailingUserText    string
}

type ChatParams struct {
	ConversationID   string
	Content          string
	Model            string
	Cwd              string
	History          *BuiltHistory
	Tools            []map[string]any
	ToolResults      []any
	ReasoningEffort  string
	ServiceTier      string
	Metadata         map[string]string
	ResetUpstream    bool
	SystemPrompt     string
	ToolScope        string
	StickySessionKey string
}

type StreamEvent struct {
	Parsed ParsedStreamEvent
	Err    error
}

type ParsedStreamEvent struct {
	Text                   string
	TextOutputIndex        *int
	ToolUses               []ToolCall
	ToolUseChunk           *ToolUseChunk
	MessageID              string
	ConversationID         string
	Reasoning              string
	ReasoningMetadata      map[string]any
	ReasoningOutputIndex   *int
	RedactedThinking       *RedactedThinking
	ContextUsagePercentage *float64
	Usage                  *UsageSnapshot
	CredentialID           string
	NkpQuotaBalance        *float64
	NkpQueue               *QueueSnapshot
	QueueStatus            *QueueSnapshot
	QuotaBalance           *string
	Metering               *MeteringSnapshot
	InvalidState           *InvalidState
	WebSearch              *WebSearchEvent
	ImageGeneration        *ImageGenerationEvent
	OutputTruncated        string
	ModelSwitched          *ModelSwitchedEvent
	APIRequest             *APIRequestEvent
	ResponseID             string
	ResponsesAPI           bool
	StopReason             string
	SilentDisconnect       bool
}

type ToolUseChunk struct {
	ToolUseID             string
	Name                  string
	Input                 string
	Stop                  bool
	OutputIndex           *int
	InputCharsTotal       int
	ContentCharsReceived  int
	ExtractedFilePath     string
	ExtractedFields       map[string]string
	StreamingFieldName    string
	StreamingFieldDelta   string
	ProviderSpecificState map[string]any
}

type RedactedThinking struct {
	Data        string
	OutputIndex *int
}

type UsageSnapshot struct {
	PromptTokens             int
	InputTokens              int
	CompletionTokens         int
	ReasoningTokens          int
	CachedInputTokens        int
	CacheCreationInputTokens int
	CacheCreation5mTokens    int
	CacheCreation1hTokens    int
	ContextWindow            int
	Estimated                bool
}

type QueueSnapshot struct {
	Position     int
	QueueDepth   int
	QueueMessage string
}

type MeteringSnapshot struct {
	Unit       string
	UnitPlural string
	Usage      float64
}

type InvalidState struct {
	Reason  string
	Message string
}

type WebSearchEvent struct {
	ID          string
	Status      string
	Query       string
	Queries     []string
	OutputIndex *int
	Final       bool
	Action      map[string]any
}

type ImageGenerationEvent struct {
	ID            string
	Status        string
	RevisedPrompt string
	Result        string
	OutputIndex   *int
	Final         bool
}

type ModelSwitchedEvent struct {
	Model           string
	Provider        string
	ReasoningEffort string
	Cause           string
}

type APIRequestEvent struct {
	Phase        string
	RequestID    string
	Provider     string
	Model        string
	CredentialID string
	Usage        *UsageSnapshot
	ErrorMessage string
	DurationMs   int64
	TTFTMs       int64
}
