package agentbasic

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestGoMatchesExecutableTSProviderFixtures(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun is required for executable TS provider fixture parity")
	}
	cmd := exec.Command("bun", "../../scripts/provider-fixtures.ts")
	cmd.Env = append(os.Environ(), "NARRAFORK_ALLOW_MULTIPLE=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run TS provider fixture: %v output=%s", err, string(output))
	}
	jsonLine := lastNonEmptyLine(string(output))
	var tsFixture struct {
		CodexToolTypes         []string       `json:"codexToolTypes"`
		CodexFunctionToolShape map[string]any `json:"codexFunctionToolShape"`
		NkpToolShape           map[string]any `json:"nkpToolShape"`
		AnthropicToolShape     map[string]any `json:"anthropicToolShape"`
		NkpWorkspaceStateShape struct {
			HasSessionEpoch bool `json:"hasSessionEpoch"`
			TraceIDLength   int  `json:"traceIdLength"`
		} `json:"nkpWorkspaceStateShape"`
		AnthropicSystemCacheControl struct {
			Type             string `json:"type"`
			CacheControlType string `json:"cacheControlType"`
		} `json:"anthropicSystemCacheControl"`
		TSOpenAIChatEvents      []map[string]any `json:"tsOpenAIChatEvents"`
		TSOpenAIResponsesEvents []map[string]any `json:"tsOpenAIResponsesEvents"`
		TSAnthropicEvents       []map[string]any `json:"tsAnthropicEvents"`
		TSAnthropicRequest      struct {
			OfficialBeta           string   `json:"officialBeta"`
			OfficialThinkingType   string   `json:"officialThinkingType"`
			CompatibleCleanedTypes []string `json:"compatibleCleanedTypes"`
		} `json:"tsAnthropicRequest"`
		TSClineEvents  []map[string]any `json:"tsClineEvents"`
		TSKiroProvider struct {
			Events  []map[string]any `json:"events"`
			Request map[string]any   `json:"request"`
		} `json:"tsKiroProvider"`
		TSKiroNativeModule struct {
			Initialized   bool             `json:"initialized"`
			RequestModel  string           `json:"requestModel"`
			RequestTarget string           `json:"requestTarget"`
			Events        []map[string]any `json:"events"`
		} `json:"tsKiroNativeModule"`
		TSNkpEvents       []map[string]any `json:"tsNkpEvents"`
		TSNugKiroEvents   []map[string]any `json:"tsNugKiroEvents"`
		TSNugOpenAIEvents []map[string]any `json:"tsNugOpenAIEvents"`
		TSCodexProvider   struct {
			Events              []map[string]any `json:"events"`
			RequestBody         map[string]any   `json:"requestBody"`
			AuthorizationPrefix string           `json:"authorizationPrefix"`
		} `json:"tsCodexProvider"`
		TSProviderErrors struct {
			OpenAIChat      []map[string]any `json:"openAIChat"`
			OpenAIResponses []map[string]any `json:"openAIResponses"`
			Anthropic       []map[string]any `json:"anthropic"`
		} `json:"tsProviderErrors"`
		TSCodexManager map[string]any `json:"tsCodexManager"`
	}
	if err := json.Unmarshal([]byte(jsonLine), &tsFixture); err != nil {
		t.Fatalf("decode TS fixture: %v output=%s", err, string(output))
	}

	goTools := appendCodexNativeTools([]map[string]any{{"type": "function", "name": "Read"}}, "gpt-5.3-codex")
	goToolTypes := make([]string, 0, len(goTools))
	for _, tool := range goTools {
		goToolTypes = append(goToolTypes, stringValue(tool["type"]))
	}
	if !sameStringSet(tsFixture.CodexToolTypes, goToolTypes) {
		t.Fatalf("Codex native tool types differ TS=%v Go=%v", tsFixture.CodexToolTypes, goToolTypes)
	}

	request := buildKiroChatRequest("claude-sonnet-4.5", ChatParams{History: &BuiltHistory{TrailingUserText: "hi"}})
	state, _ := request["conversationState"].(map[string]any)
	state["conversationId"] = "conv_fixture"
	prepareNkpRequest(request)
	workspace, _ := state["workspaceState"].(map[string]any)
	if tsFixture.NkpWorkspaceStateShape.HasSessionEpoch != (workspace["sessionEpoch"] != nil) {
		t.Fatalf("NKP sessionEpoch shape differs TS=%v Go=%#v", tsFixture.NkpWorkspaceStateShape, workspace)
	}
	if tsFixture.NkpWorkspaceStateShape.TraceIDLength != len(stringValue(workspace["traceId"])) {
		t.Fatalf("NKP traceId length differs TS=%d Go=%#v", tsFixture.NkpWorkspaceStateShape.TraceIDLength, workspace)
	}

	systemBlocks, ok := anthropicSystemBlocks("system").([]map[string]any)
	if !ok || len(systemBlocks) != 1 {
		t.Fatalf("unexpected Go Anthropic system blocks: %#v", systemBlocks)
	}
	cacheControl, _ := systemBlocks[0]["cache_control"].(map[string]any)
	if systemBlocks[0]["type"] != tsFixture.AnthropicSystemCacheControl.Type || cacheControl["type"] != tsFixture.AnthropicSystemCacheControl.CacheControlType {
		t.Fatalf("Anthropic cache_control fixture differs TS=%#v Go=%#v", tsFixture.AnthropicSystemCacheControl, systemBlocks[0])
	}
	if !fixtureEventsContainText(tsFixture.TSOpenAIChatEvents, "ts chat") ||
		!fixtureEventsContainToolStop(tsFixture.TSOpenAIChatEvents, "tool_ts_chat") ||
		!fixtureEventsContainUsage(tsFixture.TSOpenAIChatEvents, "promptTokens", 4) ||
		!fixtureEventsContainUsage(tsFixture.TSOpenAIChatEvents, "reasoningTokens", 2) {
		t.Fatalf("TS OpenAI Chat runtime fixture did not stream expected text/tool/usage events: %#v", tsFixture.TSOpenAIChatEvents)
	}
	if !fixtureEventsContainText(tsFixture.TSOpenAIResponsesEvents, "ts openai") || !fixtureEventsContainResponseID(tsFixture.TSOpenAIResponsesEvents, "resp_ts") {
		t.Fatalf("TS OpenAI runtime fixture did not stream expected text/response id: %#v", tsFixture.TSOpenAIResponsesEvents)
	}
	if !fixtureEventsContainText(tsFixture.TSAnthropicEvents, "ts anthropic") ||
		!fixtureEventsContainMessageID(tsFixture.TSAnthropicEvents, "msg_ts") ||
		!fixtureEventsContainReasoning(tsFixture.TSAnthropicEvents, "ts think") ||
		!fixtureEventsContainAnthropicSignature(tsFixture.TSAnthropicEvents, "sig_ts") ||
		!fixtureEventsContainRedactedThinking(tsFixture.TSAnthropicEvents, "redacted_ts") ||
		!fixtureEventsContainToolStop(tsFixture.TSAnthropicEvents, "tool_ts") ||
		!fixtureEventsContainUsage(tsFixture.TSAnthropicEvents, "cacheCreation1hTokens", 6) {
		t.Fatalf("TS Anthropic runtime fixture did not stream expected rich events: %#v", tsFixture.TSAnthropicEvents)
	}
	if !strings.Contains(tsFixture.TSAnthropicRequest.OfficialBeta, "interleaved-thinking") ||
		(tsFixture.TSAnthropicRequest.OfficialThinkingType != "enabled" && tsFixture.TSAnthropicRequest.OfficialThinkingType != "adaptive") ||
		!sameStringSet(tsFixture.TSAnthropicRequest.CompatibleCleanedTypes, []string{"text"}) {
		t.Fatalf("TS Anthropic request fixture did not prove beta/thinking/compatible cleaning: %#v", tsFixture.TSAnthropicRequest)
	}
	if !fixtureEventsContainText(tsFixture.TSClineEvents, "ts cline") {
		t.Fatalf("TS Cline runtime fixture did not stream expected text: %#v", tsFixture.TSClineEvents)
	}
	if !fixtureEventsContainText(tsFixture.TSKiroProvider.Events, "ts kiro") ||
		!fixtureEventsContainMessageID(tsFixture.TSKiroProvider.Events, "msg_kiro") ||
		!fixtureEventsContainReasoning(tsFixture.TSKiroProvider.Events, "ts kiro reasoning") ||
		!fixtureEventsContainToolStop(tsFixture.TSKiroProvider.Events, "tool_kiro") ||
		!fixtureEventsContainCredential(tsFixture.TSKiroProvider.Events, "kiro_cred_ts") ||
		!fixtureEventsContainContextUsage(tsFixture.TSKiroProvider.Events, 12.5) ||
		!fixtureEventsContainMetering(tsFixture.TSKiroProvider.Events, 7) ||
		fixtureNestedString(tsFixture.TSKiroProvider.Request, "conversationState", "currentMessage", "userInputMessage", "modelId") != "claude-sonnet-4.5" {
		t.Fatalf("TS Kiro provider runtime fixture did not stream expected events/request: %#v", tsFixture.TSKiroProvider)
	}
	if !tsFixture.TSKiroNativeModule.Initialized ||
		tsFixture.TSKiroNativeModule.RequestModel != "deepseek-3.2" ||
		!strings.Contains(tsFixture.TSKiroNativeModule.RequestTarget, "GenerateAssistantResponse") ||
		!fixtureRawEventsContain(tsFixture.TSKiroNativeModule.Events, "assistantResponseEvent", "content", "ts kiro native") ||
		!fixtureRawEventsContain(tsFixture.TSKiroNativeModule.Events, "contextUsageEvent", "contextUsagePercentage", float64(33)) {
		t.Fatalf("TS native Kiro module fixture did not prove initKiro/kiroChatRaw path: %#v", tsFixture.TSKiroNativeModule)
	}
	if !fixtureEventsContainText(tsFixture.TSNkpEvents, "ts nkp") || !fixtureEventsContainMessageID(tsFixture.TSNkpEvents, "msg_nkp") {
		t.Fatalf("TS NKP runtime fixture did not stream expected text/message id: %#v", tsFixture.TSNkpEvents)
	}
	if !fixtureEventsContainText(tsFixture.TSNugKiroEvents, "ts nug kiro") ||
		!fixtureEventsContainMessageID(tsFixture.TSNugKiroEvents, "msg_nug_kiro") ||
		!fixtureEventsContainContextUsage(tsFixture.TSNugKiroEvents, 22) ||
		!fixtureEventsContainMetering(tsFixture.TSNugKiroEvents, 4) {
		t.Fatalf("TS NUG Kiro runtime fixture did not stream expected events: %#v", tsFixture.TSNugKiroEvents)
	}
	if !fixtureEventsContainText(tsFixture.TSNugOpenAIEvents, "ts nug openai") {
		t.Fatalf("TS NUG OpenAI runtime fixture did not stream expected text: %#v", tsFixture.TSNugOpenAIEvents)
	}
	if !fixtureEventsContainText(tsFixture.TSCodexProvider.Events, "ts codex provider") ||
		tsFixture.TSCodexProvider.RequestBody["prompt_cache_key"] != "conv_codex_fixture" ||
		tsFixture.TSCodexProvider.RequestBody["parallel_tool_calls"] != true ||
		tsFixture.TSCodexProvider.AuthorizationPrefix != "Bearer " {
		t.Fatalf("TS Codex provider runtime fixture missing expected stream/body/header: %#v", tsFixture.TSCodexProvider)
	}
	if !fixtureEventsContainInvalidState(tsFixture.TSProviderErrors.OpenAIChat, "rate_limit", "ts rate limited") ||
		!fixtureEventsContainInvalidState(tsFixture.TSProviderErrors.OpenAIResponses, "server_error", "ts failed") ||
		!fixtureEventsContainInvalidState(tsFixture.TSProviderErrors.Anthropic, "overloaded_error", "ts overloaded") {
		t.Fatalf("TS provider error fixtures should yield invalidState events, got %#v", tsFixture.TSProviderErrors)
	}
	if tsFixture.TSCodexManager["selectedPlanType"] != "pro" ||
		tsFixture.TSCodexManager["stickyAgainId"] != tsFixture.TSCodexManager["selectedId"] ||
		tsFixture.TSCodexManager["afterQuotaId"] == tsFixture.TSCodexManager["selectedId"] ||
		tsFixture.TSCodexManager["hasMoreAfterQuota"] != true ||
		tsFixture.TSCodexManager["quotaDisabledReason"] != "quota_exhausted" ||
		tsFixture.TSCodexManager["failureDisabledReason"] != "too_many_failures" ||
		numericFixtureValue(tsFixture.TSCodexManager["stickySessionCount"]) < 1 ||
		numericFixtureValue(tsFixture.TSCodexManager["usageKnownAccounts"]) < 2 ||
		numericFixtureValue(tsFixture.TSCodexManager["usageForecastPoints"]) < 1 {
		t.Fatalf("TS Codex manager fixture should cover tier/session/quota/failure/usage behavior, got %#v", tsFixture.TSCodexManager)
	}
}

func fixtureEventsContainText(events []map[string]any, text string) bool {
	for _, event := range events {
		if event["text"] == text {
			return true
		}
	}
	return false
}

func fixtureRawEventsContain(events []map[string]any, eventType string, key string, value any) bool {
	for _, event := range events {
		data, _ := event["data"].(map[string]any)
		if event["eventType"] == eventType && data[key] == value {
			return true
		}
	}
	return false
}

func fixtureEventsContainResponseID(events []map[string]any, responseID string) bool {
	for _, event := range events {
		if event["responseId"] == responseID {
			return true
		}
	}
	return false
}

func fixtureEventsContainMessageID(events []map[string]any, messageID string) bool {
	for _, event := range events {
		if event["messageId"] == messageID {
			return true
		}
	}
	return false
}

func fixtureEventsContainReasoning(events []map[string]any, text string) bool {
	for _, event := range events {
		if event["reasoning"] == text {
			return true
		}
	}
	return false
}

func fixtureEventsContainAnthropicSignature(events []map[string]any, signature string) bool {
	for _, event := range events {
		metadata, _ := event["reasoningMetadata"].(map[string]any)
		anthropic, _ := metadata["anthropic"].(map[string]any)
		if anthropic["signature"] == signature {
			return true
		}
	}
	return false
}

func fixtureEventsContainRedactedThinking(events []map[string]any, data string) bool {
	for _, event := range events {
		redacted, _ := event["redactedThinking"].(map[string]any)
		if redacted["data"] == data {
			return true
		}
	}
	return false
}

func fixtureEventsContainToolStop(events []map[string]any, toolUseID string) bool {
	for _, event := range events {
		chunk, _ := event["toolUseChunk"].(map[string]any)
		if chunk["toolUseId"] == toolUseID && chunk["stop"] == true {
			return true
		}
	}
	return false
}

func fixtureEventsContainUsage(events []map[string]any, key string, want float64) bool {
	for _, event := range events {
		usage, _ := event["usage"].(map[string]any)
		if usage[key] == want {
			return true
		}
	}
	return false
}

func fixtureEventsContainInvalidState(events []map[string]any, reason string, message string) bool {
	for _, event := range events {
		invalid, _ := event["invalidState"].(map[string]any)
		if invalid["reason"] == reason && invalid["message"] == message {
			return true
		}
	}
	return false
}

func fixtureEventsContainCredential(events []map[string]any, credentialID string) bool {
	for _, event := range events {
		if event["credentialId"] == credentialID {
			return true
		}
	}
	return false
}

func fixtureEventsContainContextUsage(events []map[string]any, want float64) bool {
	for _, event := range events {
		if event["contextUsagePercentage"] == want {
			return true
		}
	}
	return false
}

func fixtureEventsContainMetering(events []map[string]any, want float64) bool {
	for _, event := range events {
		metering, _ := event["metering"].(map[string]any)
		if metering["usage"] == want {
			return true
		}
	}
	return false
}

func fixtureNestedString(root map[string]any, path ...string) string {
	var current any = root
	for _, key := range path {
		obj, _ := current.(map[string]any)
		current = obj[key]
	}
	return stringValue(current)
}

func numericFixtureValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	default:
		return 0
	}
}

func lastNonEmptyLine(output string) string {
	lines := strings.Split(output, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if strings.TrimSpace(lines[index]) != "" {
			return strings.TrimSpace(lines[index])
		}
	}
	return ""
}

func sameStringSet(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, value := range a {
		seen[value]++
	}
	for _, value := range b {
		seen[value]--
		if seen[value] < 0 {
			return false
		}
	}
	return true
}
