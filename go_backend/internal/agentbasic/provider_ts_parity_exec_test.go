package agentbasic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestGoMatchesExecutableTSProviderFixtures(t *testing.T) {
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skip("bun is required for executable TS provider fixture parity")
	}
	goBackendRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve go backend root: %v", err)
	}
	repoRoot := filepath.Dir(goBackendRoot)
	cmd := exec.Command("bun", "go_backend/scripts/provider-fixtures.ts")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "NARRAFORK_ALLOW_MULTIPLE=1", "HOME="+t.TempDir())
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
		TSCodexRebuildHistory struct {
			RequestCount             int              `json:"requestCount"`
			FirstAuthorizationPrefix string           `json:"firstAuthorizationPrefix"`
			FirstCredentialID        string           `json:"firstCredentialId"`
			FailedCredentialID       string           `json:"failedCredentialId"`
			PartialText              string           `json:"partialText"`
			Events                   []map[string]any `json:"events"`
			Error                    struct {
				Name                 string `json:"name"`
				Message              string `json:"message"`
				Code                 string `json:"code"`
				PreviousCredentialID string `json:"previousCredentialId"`
				Operation            string `json:"operation"`
			} `json:"error"`
			FailedDisabledReason string  `json:"failedDisabledReason"`
			FailedQuotaResetsAt  float64 `json:"failedQuotaResetsAt"`
			Available            int     `json:"available"`
		} `json:"tsCodexRebuildHistory"`
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

	goTools := appendCodexNativeTools([]map[string]any{{"type": "function", "name": "Read"}}, "gpt-5.3-codex", true, true)
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

	systemBlocks, ok := anthropicSystemBlocks("system", true).([]map[string]any)
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
	goOpenAIChatEvents := goOpenAIChatStreamFixture(t)
	if !goParsedEventsContainText(goOpenAIChatEvents, "ts chat") ||
		!goParsedEventsContainToolStop(goOpenAIChatEvents, "tool_ts_chat") ||
		!goParsedEventsContainUsage(goOpenAIChatEvents, "promptTokens", 4) ||
		!goParsedEventsContainUsage(goOpenAIChatEvents, "reasoningTokens", 2) {
		t.Fatalf("Go OpenAI Chat runtime fixture does not match TS text/tool/usage contract: %#v", goOpenAIChatEvents)
	}
	if !fixtureEventsContainText(tsFixture.TSOpenAIResponsesEvents, "ts openai") || !fixtureEventsContainResponseID(tsFixture.TSOpenAIResponsesEvents, "resp_ts") {
		t.Fatalf("TS OpenAI runtime fixture did not stream expected text/response id: %#v", tsFixture.TSOpenAIResponsesEvents)
	}
	goOpenAIResponsesEvents := goOpenAIResponsesStreamFixture(t)
	if !goParsedEventsContainText(goOpenAIResponsesEvents, "ts openai") || !goParsedEventsContainResponseID(goOpenAIResponsesEvents, "resp_ts") {
		t.Fatalf("Go OpenAI Responses runtime fixture does not match TS text/response id contract: %#v", goOpenAIResponsesEvents)
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
	goAnthropicEvents := goAnthropicStreamFixture(t)
	if !goParsedEventsContainText(goAnthropicEvents, "ts anthropic") ||
		!goParsedEventsContainMessageID(goAnthropicEvents, "msg_ts") ||
		!goParsedEventsContainReasoning(goAnthropicEvents, "ts think") ||
		!goParsedEventsContainAnthropicSignature(goAnthropicEvents, "sig_ts") ||
		!goParsedEventsContainRedactedThinking(goAnthropicEvents, "redacted_ts") ||
		!goParsedEventsContainToolStop(goAnthropicEvents, "tool_ts") ||
		!goParsedEventsContainUsage(goAnthropicEvents, "cacheCreation1hTokens", 6) {
		t.Fatalf("Go Anthropic runtime fixture does not match TS rich event contract: %#v", goAnthropicEvents)
	}
	if !strings.Contains(tsFixture.TSAnthropicRequest.OfficialBeta, "interleaved-thinking") ||
		(tsFixture.TSAnthropicRequest.OfficialThinkingType != "enabled" && tsFixture.TSAnthropicRequest.OfficialThinkingType != "adaptive") ||
		!sameStringSet(tsFixture.TSAnthropicRequest.CompatibleCleanedTypes, []string{"text"}) {
		t.Fatalf("TS Anthropic request fixture did not prove beta/thinking/compatible cleaning: %#v", tsFixture.TSAnthropicRequest)
	}
	if !fixtureEventsContainText(tsFixture.TSClineEvents, "ts cline") ||
		!fixtureEventsContainUsage(tsFixture.TSClineEvents, "promptTokens", 2) ||
		!fixtureEventsContainUsage(tsFixture.TSClineEvents, "completionTokens", 3) {
		t.Fatalf("TS Cline runtime fixture did not stream expected text/usage: %#v", tsFixture.TSClineEvents)
	}
	goClineEvents := goClineStreamFixture(t)
	if !goParsedEventsContainText(goClineEvents, "ts cline") ||
		!goParsedEventsContainUsage(goClineEvents, "promptTokens", 2) ||
		!goParsedEventsContainUsage(goClineEvents, "completionTokens", 3) {
		t.Fatalf("Go Cline runtime fixture does not match TS text/usage contract: %#v", goClineEvents)
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
	goKiroProviderEvents := goKiroProviderStreamFixture(t)
	if !goParsedEventsContainText(goKiroProviderEvents, "ts kiro") ||
		!goParsedEventsContainMessageID(goKiroProviderEvents, "msg_kiro") ||
		!goParsedEventsContainReasoning(goKiroProviderEvents, "ts kiro reasoning") ||
		!goParsedEventsContainToolStop(goKiroProviderEvents, "tool_kiro") ||
		!goParsedEventsContainCredential(goKiroProviderEvents, "kiro_cred_ts") ||
		!goParsedEventsContainContextUsage(goKiroProviderEvents, 12.5) ||
		!goParsedEventsContainMetering(goKiroProviderEvents, 7) {
		t.Fatalf("Go Kiro provider runtime fixture does not match TS event contract: %#v", goKiroProviderEvents)
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
	goNkpEvents := goNkpStreamFixture(t)
	if !goParsedEventsContainText(goNkpEvents, "ts nkp") || !goParsedEventsContainMessageID(goNkpEvents, "msg_nkp") {
		t.Fatalf("Go NKP runtime fixture does not match TS text/message id contract: %#v", goNkpEvents)
	}
	if !fixtureEventsContainText(tsFixture.TSNugKiroEvents, "ts nug kiro") ||
		!fixtureEventsContainMessageID(tsFixture.TSNugKiroEvents, "msg_nug_kiro") ||
		!fixtureEventsContainContextUsage(tsFixture.TSNugKiroEvents, 22) ||
		!fixtureEventsContainMetering(tsFixture.TSNugKiroEvents, 4) {
		t.Fatalf("TS NUG Kiro runtime fixture did not stream expected events: %#v", tsFixture.TSNugKiroEvents)
	}
	goNugKiroEvents := goNugKiroStreamFixture(t)
	if !goParsedEventsContainText(goNugKiroEvents, "ts nug kiro") ||
		!goParsedEventsContainMessageID(goNugKiroEvents, "msg_nug_kiro") ||
		!goParsedEventsContainContextUsage(goNugKiroEvents, 22) ||
		!goParsedEventsContainMetering(goNugKiroEvents, 4) {
		t.Fatalf("Go NUG Kiro runtime fixture does not match TS event contract: %#v", goNugKiroEvents)
	}
	if !fixtureEventsContainText(tsFixture.TSNugOpenAIEvents, "ts nug openai") ||
		!fixtureEventsContainUsage(tsFixture.TSNugOpenAIEvents, "promptTokens", 2) ||
		!fixtureEventsContainUsage(tsFixture.TSNugOpenAIEvents, "completionTokens", 3) {
		t.Fatalf("TS NUG OpenAI runtime fixture did not stream expected text/usage: %#v", tsFixture.TSNugOpenAIEvents)
	}
	goNugOpenAIEvents := goNugOpenAIStreamFixture(t)
	if !goParsedEventsContainText(goNugOpenAIEvents, "ts nug openai") ||
		!goParsedEventsContainUsage(goNugOpenAIEvents, "promptTokens", 2) ||
		!goParsedEventsContainUsage(goNugOpenAIEvents, "completionTokens", 3) {
		t.Fatalf("Go NUG OpenAI runtime fixture does not match TS text/usage contract: %#v", goNugOpenAIEvents)
	}
	if !fixtureEventsContainText(tsFixture.TSCodexProvider.Events, "ts codex provider") ||
		tsFixture.TSCodexProvider.RequestBody["prompt_cache_key"] != "conv_codex_fixture" ||
		tsFixture.TSCodexProvider.RequestBody["parallel_tool_calls"] != true ||
		tsFixture.TSCodexProvider.AuthorizationPrefix != "Bearer " {
		t.Fatalf("TS Codex provider runtime fixture missing expected stream/body/header: %#v", tsFixture.TSCodexProvider)
	}
	tsCodexCredentialID := firstFixtureCredentialID(tsFixture.TSCodexProvider.Events)
	if tsCodexCredentialID == "" {
		t.Fatalf("TS Codex provider runtime fixture did not expose a credentialId: %#v", tsFixture.TSCodexProvider.Events)
	}
	goCodexProvider := goCodexProviderStreamFixture(t, tsCodexCredentialID)
	if !goParsedEventsContainText(goCodexProvider.Events, "ts codex provider") ||
		!goParsedEventsContainResponseID(goCodexProvider.Events, "resp_codex") ||
		!goParsedEventsContainUsage(goCodexProvider.Events, "promptTokens", 2) ||
		!goParsedEventsContainUsage(goCodexProvider.Events, "completionTokens", 3) ||
		!goParsedEventsContainCredential(goCodexProvider.Events, tsCodexCredentialID) ||
		goCodexProvider.RequestBody["prompt_cache_key"] != tsFixture.TSCodexProvider.RequestBody["prompt_cache_key"] ||
		goCodexProvider.RequestBody["parallel_tool_calls"] != tsFixture.TSCodexProvider.RequestBody["parallel_tool_calls"] ||
		goCodexProvider.AuthorizationPrefix != tsFixture.TSCodexProvider.AuthorizationPrefix {
		t.Fatalf("Go Codex provider runtime fixture does not match TS stream/body/header contract: TS=%#v Go=%#v", tsFixture.TSCodexProvider, goCodexProvider)
	}
	failedCodexID := strings.TrimSpace(tsFixture.TSCodexRebuildHistory.FailedCredentialID)
	if !fixtureEventsContainText(tsFixture.TSCodexRebuildHistory.Events, "partial") ||
		failedCodexID == "" ||
		!fixtureEventsContainCredential(tsFixture.TSCodexRebuildHistory.Events, failedCodexID) ||
		tsFixture.TSCodexRebuildHistory.RequestCount != 1 ||
		strings.TrimSpace(tsFixture.TSCodexRebuildHistory.FirstCredentialID) != failedCodexID ||
		tsFixture.TSCodexRebuildHistory.FirstAuthorizationPrefix != "Bearer " ||
		tsFixture.TSCodexRebuildHistory.PartialText != "partial" ||
		tsFixture.TSCodexRebuildHistory.Error.Code != CodexRebuildHistoryRetryCode ||
		strings.TrimSpace(tsFixture.TSCodexRebuildHistory.Error.Operation) != "chat" ||
		strings.TrimSpace(tsFixture.TSCodexRebuildHistory.Error.PreviousCredentialID) != failedCodexID ||
		tsFixture.TSCodexRebuildHistory.FailedDisabledReason != "quota_exhausted" ||
		tsFixture.TSCodexRebuildHistory.FailedQuotaResetsAt <= 0 ||
		tsFixture.TSCodexRebuildHistory.Available < 1 {
		t.Fatalf("TS Codex rebuild-history fixture missing expected failover/retry state: %#v", tsFixture.TSCodexRebuildHistory)
	}
	goCodexRebuild := goCodexRebuildHistoryFixture(t, failedCodexID)
	if !goParsedEventsContainText(goCodexRebuild.Events, "partial") ||
		goCodexRebuild.RequestCount != tsFixture.TSCodexRebuildHistory.RequestCount ||
		goCodexRebuild.FirstAuthorizationPrefix != tsFixture.TSCodexRebuildHistory.FirstAuthorizationPrefix ||
		goCodexRebuild.FirstCredentialID != failedCodexID ||
		goCodexRebuild.FailedCredentialID != failedCodexID ||
		goCodexRebuild.PartialText != tsFixture.TSCodexRebuildHistory.PartialText ||
		goCodexRebuild.RetryCode != tsFixture.TSCodexRebuildHistory.Error.Code ||
		goCodexRebuild.RetryOperation != tsFixture.TSCodexRebuildHistory.Error.Operation ||
		goCodexRebuild.RetryPreviousCredentialID != failedCodexID ||
		goCodexRebuild.FailedDisabledReason != tsFixture.TSCodexRebuildHistory.FailedDisabledReason ||
		goCodexRebuild.FailedQuotaResetsAt <= 0 ||
		goCodexRebuild.Available != int(tsFixture.TSCodexRebuildHistory.Available) {
		t.Fatalf("Go Codex rebuild-history fixture does not match TS failover/retry state: TS=%#v Go=%#v", tsFixture.TSCodexRebuildHistory, goCodexRebuild)
	}
	if !fixtureEventsContainInvalidState(tsFixture.TSProviderErrors.OpenAIChat, "rate_limit", "ts rate limited") ||
		!fixtureEventsContainInvalidState(tsFixture.TSProviderErrors.OpenAIResponses, "server_error", "ts failed") ||
		!fixtureEventsContainInvalidState(tsFixture.TSProviderErrors.Anthropic, "overloaded_error", "ts overloaded") {
		t.Fatalf("TS provider error fixtures should yield invalidState events, got %#v", tsFixture.TSProviderErrors)
	}
	assertGoInvalidStateMatchesTSFixture(t, "OpenAI Chat", goOpenAIChatInvalidStateFixture(t), tsFixture.TSProviderErrors.OpenAIChat)
	assertGoInvalidStateMatchesTSFixture(t, "OpenAI Responses", goOpenAIResponsesInvalidStateFixture(t), tsFixture.TSProviderErrors.OpenAIResponses)
	assertGoInvalidStateMatchesTSFixture(t, "Anthropic", goAnthropicInvalidStateFixture(t), tsFixture.TSProviderErrors.Anthropic)
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

func assertGoInvalidStateMatchesTSFixture(t *testing.T, name string, goInvalid *InvalidState, tsEvents []map[string]any) {
	t.Helper()
	tsReason, tsMessage := firstFixtureInvalidState(tsEvents)
	if goInvalid == nil || goInvalid.Reason != tsReason || goInvalid.Message != tsMessage {
		t.Fatalf("%s invalidState differs TS=%s/%q Go=%#v", name, tsReason, tsMessage, goInvalid)
	}
}

func goOpenAIChatStreamFixture(t *testing.T) []ParsedStreamEvent {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"ts chat"}}]}

`)
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"tool_ts_chat","type":"function","function":{"name":"Read"}}]}}]}

`)
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"file_path\":"}}]}}]}

`)
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"chat.txt\"}"}}]}}]}

`)
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

`)
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":5,"completion_tokens_details":{"reasoning_tokens":2},"prompt_tokens_details":{"cached_tokens":1}}}

`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-test"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
	if err != nil {
		t.Fatalf("OpenAI Chat stream fixture returned error: %v", err)
	}
	return collectParsedFixtureEvents(t, stream)
}

func goOpenAIResponsesStreamFixture(t *testing.T) []ParsedStreamEvent {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.created","response":{"id":"resp_ts"}}

`)
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"ts openai"}

`)
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"id":"resp_ts","usage":{"input_tokens":2,"output_tokens":3}}}

`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-test", APIMode: "responses"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
	if err != nil {
		t.Fatalf("OpenAI Responses stream fixture returned error: %v", err)
	}
	return collectParsedFixtureEvents(t, stream)
}

func goClineStreamFixture(t *testing.T) []ParsedStreamEvent {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected Cline fixture path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer workos:fixture-token" {
			t.Fatalf("unexpected Cline auth header: %s", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"ts cline"}}]}

`)
		fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3}}

`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "cline", Kind: "cline", APIKey: "fixture-token", BaseURL: server.URL, Model: "anthropic/claude-sonnet-4"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hello"}}}})
	if err != nil {
		t.Fatalf("Cline stream fixture returned error: %v", err)
	}
	return collectParsedFixtureEvents(t, stream)
}

func goAnthropicStreamFixture(t *testing.T) []ParsedStreamEvent {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: message_start
data: {"type":"message_start","message":{"id":"msg_ts","usage":{"input_tokens":2,"cache_read_input_tokens":1}}}

`)
		fmt.Fprint(w, `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

`)
		fmt.Fprint(w, `event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"ts think"}}

`)
		fmt.Fprint(w, `event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_ts"}}

`)
		fmt.Fprint(w, `event: content_block_stop
data: {"type":"content_block_stop","index":0}

`)
		fmt.Fprint(w, `event: content_block_start
data: {"type":"content_block_start","index":3,"content_block":{"type":"redacted_thinking","data":"redacted_ts"}}

`)
		fmt.Fprint(w, `event: content_block_stop
data: {"type":"content_block_stop","index":3}

`)
		fmt.Fprint(w, `event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

`)
		fmt.Fprint(w, `event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"ts anthropic"}}

`)
		fmt.Fprint(w, `event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool_ts","name":"Read","input":{}}}

`)
		fmt.Fprint(w, `event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"ts.txt\"}"}}

`)
		fmt.Fprint(w, `event: content_block_stop
data: {"type":"content_block_stop","index":2}

`)
		fmt.Fprint(w, `event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3,"cache_creation_input_tokens":4,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":6}}}

`)
		fmt.Fprint(w, `event: message_stop
data: {"type":"message_stop"}

`)
	}))
	defer server.Close()

	adapter := &AnthropicMessagesAdapter{Provider: Provider{Prefix: "anthropic", Kind: "anthropic", APIKey: "test-key", BaseURL: server.URL, Model: "claude-test"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{map[string]any{"role": "user", "content": "hi"}}}, ReasoningEffort: "medium"})
	if err != nil {
		t.Fatalf("Anthropic stream fixture returned error: %v", err)
	}
	return collectParsedFixtureEvents(t, stream)
}

func goKiroProviderStreamFixture(t *testing.T) []ParsedStreamEvent {
	t.Helper()
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kiro/chat" {
			t.Fatalf("unexpected Kiro fixture path: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: credentialIdEvent
data: {"credentialId":"kiro_cred_ts"}

`)
		fmt.Fprint(w, `event: reasoningContentEvent
data: {"content":"ts kiro reasoning"}

`)
		fmt.Fprint(w, `event: assistantResponseEvent
data: {"content":"ts kiro","messageId":"msg_kiro","conversationId":"conv_kiro_response"}

`)
		fmt.Fprint(w, `event: toolUseEvent
data: {"toolUseId":"tool_kiro","name":"Read","input":"{\"file_path\":\"kiro.txt\"}"}

`)
		fmt.Fprint(w, `event: toolUseEvent
data: {"toolUseId":"tool_kiro","name":"Read","stop":true}

`)
		fmt.Fprint(w, `event: contextUsageEvent
data: {"contextUsagePercentage":12.5}

`)
		fmt.Fprint(w, `event: meteringEvent
data: {"usage":7,"unit":"credit","unitPlural":"credits"}

`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	adapter := &KiroProviderAdapter{Provider: Provider{Prefix: "kiro", Kind: "kiro", APIKey: "fixture-key", BaseURL: server.URL, Model: "claude-sonnet-4.5"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{TrailingUserText: "hello"}, Cwd: t.TempDir(), Tools: []map[string]any{}})
	if err != nil {
		t.Fatalf("Kiro provider stream fixture returned error: %v", err)
	}
	events := collectParsedFixtureEvents(t, stream)
	if fixtureNestedString(requestBody, "conversationState", "currentMessage", "userInputMessage", "modelId") != "claude-sonnet-4.5" {
		t.Fatalf("Go Kiro provider fixture built unexpected request body: %#v", requestBody)
	}
	return events
}

func goNkpStreamFixture(t *testing.T) []ParsedStreamEvent {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kiro/chat" {
			t.Fatalf("unexpected NKP fixture path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: assistantResponseEvent
data: {"content":"ts nkp","messageId":"msg_nkp"}

`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	adapter := &KiroProviderAdapter{Provider: Provider{Prefix: "nkp", Kind: "nkp", APIKey: "fixture-key", BaseURL: server.URL, Model: "claude-sonnet-4.5"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{TrailingUserText: "hello"}, Cwd: t.TempDir(), Tools: []map[string]any{}})
	if err != nil {
		t.Fatalf("NKP stream fixture returned error: %v", err)
	}
	return collectParsedFixtureEvents(t, stream)
}

func goNugKiroStreamFixture(t *testing.T) []ParsedStreamEvent {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kiro/chat" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: assistantResponseEvent
data: {"content":"ts nug kiro","messageId":"msg_nug_kiro"}

`)
		fmt.Fprint(w, `event: contextUsageEvent
data: {"contextUsagePercentage":22}

`)
		fmt.Fprint(w, `event: meteringEvent
data: {"usage":4,"unit":"credit","unitPlural":"credits"}

`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	adapter := &NugProviderAdapter{Provider: Provider{Prefix: "nug", Kind: "nug", APIKey: "fixture-key", BaseURL: server.URL, Model: "claude-sonnet-4.5"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{TrailingUserText: "hello"}, Cwd: t.TempDir(), Tools: []map[string]any{}})
	if err != nil {
		t.Fatalf("NUG Kiro stream fixture returned error: %v", err)
	}
	return collectParsedFixtureEvents(t, stream)
}

func goNugOpenAIStreamFixture(t *testing.T) []ParsedStreamEvent {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/usage/summary":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":{"remaining":"99","usage":3,"unit":"credit","unitPlural":"credits"}}`)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"ts nug openai"}}]}

`)
			fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3}}

`)
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			t.Fatalf("unexpected NUG OpenAI fixture path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	adapter := &NugProviderAdapter{Provider: Provider{Prefix: "nug", Kind: "nug", APIKey: "fixture-key", BaseURL: server.URL, Model: "gpt-test", APIMode: "openai"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hello"}}}})
	if err != nil {
		t.Fatalf("NUG OpenAI stream fixture returned error: %v", err)
	}
	return collectParsedFixtureEvents(t, stream)
}

type goCodexProviderFixture struct {
	Events              []ParsedStreamEvent
	RequestBody         map[string]any
	AuthorizationPrefix string
}

type goCodexRebuildHistoryResult struct {
	Events                    []ParsedStreamEvent
	RequestCount              int
	FirstAuthorizationPrefix  string
	FirstCredentialID         string
	FailedCredentialID        string
	PartialText               string
	RetryCode                 string
	RetryOperation            string
	RetryPreviousCredentialID string
	FailedDisabledReason      string
	FailedQuotaResetsAt       float64
	Available                 int
}

func goCodexProviderStreamFixture(t *testing.T, credentialID string) goCodexProviderFixture {
	t.Helper()
	var mu sync.Mutex
	var requestBody map[string]any
	authorization := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected Codex provider fixture path: %s", r.URL.Path)
		}
		if strings.Contains(strings.ToLower(r.Header.Get("Upgrade")), "websocket") {
			http.Error(w, "websocket unavailable", http.StatusBadRequest)
			return
		}
		var decoded map[string]any
		_ = json.NewDecoder(r.Body).Decode(&decoded)
		if r.Method == http.MethodPost {
			mu.Lock()
			for key, values := range r.Header {
				if strings.EqualFold(key, "Authorization") && len(values) > 0 {
					authorization = values[0]
					break
				}
			}
			requestBody = decoded
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.created","response":{"id":"resp_codex"}}

`)
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"ts codex provider"}

`)
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"id":"resp_codex","usage":{"input_tokens":2,"output_tokens":3}}}

`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "codex", Kind: "codex", APIKey: "codex-token", BaseURL: server.URL, Model: "gpt-5.3-codex", APIMode: "codex", CredentialID: credentialID}}
	stream, err := adapter.Chat(context.Background(), ChatParams{
		ConversationID: "conv_codex_fixture",
		History:        &BuiltHistory{History: []any{Message{Role: "user", Content: "hello"}}},
	})
	if err != nil {
		t.Fatalf("Codex provider stream fixture returned error: %v", err)
	}
	events := collectParsedFixtureEvents(t, stream)
	mu.Lock()
	defer mu.Unlock()
	authorizationPrefix := authorization
	if len(authorizationPrefix) > len("Bearer ") {
		authorizationPrefix = authorizationPrefix[:len("Bearer ")]
	}
	return goCodexProviderFixture{
		Events:              events,
		RequestBody:         requestBody,
		AuthorizationPrefix: authorizationPrefix,
	}
}

func goCodexRebuildHistoryFixture(t *testing.T, firstCredentialID string) goCodexRebuildHistoryResult {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "codex-credentials.json"), []byte(`[
		{"id":"bad","accessToken":"bad-token","accountId":"bad-acct","priority":0},
		{"id":"good","accessToken":"good-token","accountId":"good-acct","priority":1}
	]`), 0o600); err != nil {
		t.Fatalf("write codex creds: %v", err)
	}
	var mu sync.Mutex
	requests := 0
	firstAuthorization := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected codex rebuild fixture path: %s", r.URL.Path)
		}
		if strings.Contains(strings.ToLower(r.Header.Get("Upgrade")), "websocket") {
			http.Error(w, "websocket unavailable", http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodPost {
			mu.Lock()
			requests++
			if requests == 1 {
				for key, values := range r.Header {
					if strings.EqualFold(key, "Authorization") && len(values) > 0 {
						firstAuthorization = values[0]
						break
					}
				}
			}
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.created","response":{"id":"resp_codex_rebuild"}}

`)
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","delta":"partial"}

`)
		fmt.Fprint(w, `data: {"type":"response.failed","response":{"error":{"code":"usage_limit_reached","message":"usage limit reached","resets_at":2000000000}}}

`)
	}))
	defer server.Close()

	if firstCredentialID != "bad" && firstCredentialID != "good" {
		firstCredentialID = "bad"
	}
	secondCredentialID := "good"
	if firstCredentialID == "good" {
		secondCredentialID = "bad"
	}
	candidate := func(id string) Provider {
		return Provider{Prefix: "codex", Kind: "codex", APIKey: id + "-token", BaseURL: server.URL, Model: "gpt-5.3-codex", APIMode: "codex", CredentialID: id}
	}
	provider := Provider{
		Prefix:            "codex",
		Kind:              "codex",
		APIKey:            firstCredentialID + "-token",
		BaseURL:           server.URL,
		Model:             "gpt-5.3-codex",
		APIMode:           "codex",
		LoadBalancingMode: "tier-balanced",
		CredentialPool:    []Provider{candidate(firstCredentialID), candidate(secondCredentialID)},
	}
	adapter := &OpenAIChatCompletionsAdapter{Provider: provider}
	stream, err := adapter.Chat(context.Background(), ChatParams{
		ConversationID:   "conv_codex_rebuild_fixture",
		StickySessionKey: "nar-rebuild-fixture",
		History:          &BuiltHistory{History: []any{Message{Role: "user", Content: "hello"}}},
	})
	if err != nil {
		t.Fatalf("Codex rebuild-history fixture returned error: %v", err)
	}
	events := []ParsedStreamEvent{}
	var streamErr error
	for event := range stream {
		if event.Err != nil {
			streamErr = event.Err
			continue
		}
		events = append(events, event.Parsed)
	}
	if streamErr == nil {
		t.Fatalf("Codex rebuild-history fixture expected retryable error, got nil")
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "codex-credentials.json"))
	if err != nil {
		t.Fatalf("read codex creds: %v", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("decode codex creds: %v", err)
	}
	failedEntry := map[string]any{}
	for _, entry := range entries {
		if stringValue(entry["disabledReason"]) == "quota_exhausted" || boolValue(entry["disabled"]) {
			failedEntry = entry
			break
		}
	}
	firstAuthorizationPrefix := firstAuthorization
	if len(firstAuthorizationPrefix) > len("Bearer ") {
		firstAuthorizationPrefix = firstAuthorizationPrefix[:len("Bearer ")]
	}
	result := goCodexRebuildHistoryResult{
		Events:                   events,
		RequestCount:             requests,
		FirstAuthorizationPrefix: firstAuthorizationPrefix,
		FirstCredentialID:        firstParsedCredentialID(events),
		FailedCredentialID:       stringValue(failedEntry["id"]),
		PartialText:              firstParsedText(events),
		FailedDisabledReason:     stringValue(failedEntry["disabledReason"]),
		FailedQuotaResetsAt:      numericFixtureValue(failedEntry["quotaResetsAt"]),
		Available:                countEnabledCodexCredentials(entries),
	}
	if isCodexRebuildHistoryRetryError(streamErr) {
		metadata := codexRebuildHistoryRetryMetadata(streamErr)
		result.RetryCode = CodexRebuildHistoryRetryCode
		result.RetryOperation = stringValue(metadata["operation"])
		result.RetryPreviousCredentialID = stringValue(metadata["previousCredentialId"])
	}
	return result
}

func firstFixtureCredentialID(events []map[string]any) string {
	for _, event := range events {
		if credentialID := strings.TrimSpace(stringValue(event["credentialId"])); credentialID != "" {
			return credentialID
		}
	}
	return ""
}

func firstFixtureInvalidState(events []map[string]any) (string, string) {
	for _, event := range events {
		invalid, _ := event["invalidState"].(map[string]any)
		reason := strings.TrimSpace(stringValue(invalid["reason"]))
		message := strings.TrimSpace(stringValue(invalid["message"]))
		if reason != "" || message != "" {
			return reason, message
		}
	}
	return "", ""
}

func goOpenAIChatInvalidStateFixture(t *testing.T) *InvalidState {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"error":{"message":"ts rate limited","type":"rate_limit_error","code":"rate_limit"}}

`)
	}))
	defer server.Close()

	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-test"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
	if err != nil {
		t.Fatalf("OpenAI Chat fixture returned error: %v", err)
	}
	return collectInvalidStateFixture(t, stream)
}

func goOpenAIResponsesInvalidStateFixture(t *testing.T) *InvalidState {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.failed","response":{"id":"resp_failed","error":{"message":"ts failed","code":"server_error"}}}

`)
	}))
	defer server.Close()

	adapter := &OpenAIChatCompletionsAdapter{Provider: Provider{Prefix: "openai", Kind: "openai", APIKey: "test-key", BaseURL: server.URL, Model: "gpt-test", APIMode: "responses"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{Message{Role: "user", Content: "hi"}}}})
	if err != nil {
		t.Fatalf("OpenAI Responses fixture returned error: %v", err)
	}
	return collectInvalidStateFixture(t, stream)
}

func goAnthropicInvalidStateFixture(t *testing.T) *InvalidState {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"ts overloaded"}}

`)
	}))
	defer server.Close()

	adapter := &AnthropicMessagesAdapter{Provider: Provider{Prefix: "anthropic", Kind: "anthropic", APIKey: "test-key", BaseURL: server.URL, Model: "claude-test"}}
	stream, err := adapter.Chat(context.Background(), ChatParams{History: &BuiltHistory{History: []any{map[string]any{"role": "user", "content": "hi"}}}})
	if err != nil {
		t.Fatalf("Anthropic fixture returned error: %v", err)
	}
	return collectInvalidStateFixture(t, stream)
}

func collectInvalidStateFixture(t *testing.T, stream <-chan StreamEvent) *InvalidState {
	t.Helper()
	var invalid *InvalidState
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("expected invalidState event instead of stream error: %v", event.Err)
		}
		if event.Parsed.InvalidState != nil {
			invalid = event.Parsed.InvalidState
		}
	}
	return invalid
}

func collectParsedFixtureEvents(t *testing.T, stream <-chan StreamEvent) []ParsedStreamEvent {
	t.Helper()
	events := []ParsedStreamEvent{}
	for event := range stream {
		if event.Err != nil {
			t.Fatalf("stream fixture error: %v", event.Err)
		}
		events = append(events, event.Parsed)
	}
	return events
}

func goParsedEventsContainText(events []ParsedStreamEvent, text string) bool {
	for _, event := range events {
		if event.Text == text {
			return true
		}
	}
	return false
}

func goParsedEventsContainResponseID(events []ParsedStreamEvent, responseID string) bool {
	for _, event := range events {
		if event.ResponseID == responseID {
			return true
		}
	}
	return false
}

func goParsedEventsContainMessageID(events []ParsedStreamEvent, messageID string) bool {
	for _, event := range events {
		if event.MessageID == messageID {
			return true
		}
	}
	return false
}

func goParsedEventsContainReasoning(events []ParsedStreamEvent, text string) bool {
	for _, event := range events {
		if event.Reasoning == text {
			return true
		}
	}
	return false
}

func goParsedEventsContainCredential(events []ParsedStreamEvent, credentialID string) bool {
	for _, event := range events {
		if event.CredentialID == credentialID {
			return true
		}
	}
	return false
}

func firstParsedText(events []ParsedStreamEvent) string {
	parts := make([]string, 0, len(events))
	for _, event := range events {
		if strings.TrimSpace(event.Text) != "" {
			parts = append(parts, event.Text)
		}
	}
	return strings.Join(parts, "")
}

func firstParsedCredentialID(events []ParsedStreamEvent) string {
	for _, event := range events {
		if credentialID := strings.TrimSpace(event.CredentialID); credentialID != "" {
			return credentialID
		}
	}
	return ""
}

func countEnabledCodexCredentials(entries []map[string]any) int {
	count := 0
	for _, entry := range entries {
		if boolValue(entry["disabled"]) {
			continue
		}
		count++
	}
	return count
}

func goParsedEventsContainContextUsage(events []ParsedStreamEvent, want float64) bool {
	for _, event := range events {
		if event.ContextUsagePercentage != nil && *event.ContextUsagePercentage == want {
			return true
		}
	}
	return false
}

func goParsedEventsContainMetering(events []ParsedStreamEvent, want float64) bool {
	for _, event := range events {
		if event.Metering != nil && event.Metering.Usage == want {
			return true
		}
	}
	return false
}

func goParsedEventsContainAnthropicSignature(events []ParsedStreamEvent, signature string) bool {
	for _, event := range events {
		if event.ReasoningMetadata != nil && stringValue(event.ReasoningMetadata["signature"]) == signature {
			return true
		}
	}
	return false
}

func goParsedEventsContainRedactedThinking(events []ParsedStreamEvent, data string) bool {
	for _, event := range events {
		if event.RedactedThinking != nil && event.RedactedThinking.Data == data {
			return true
		}
	}
	return false
}

func goParsedEventsContainToolStop(events []ParsedStreamEvent, toolUseID string) bool {
	for _, event := range events {
		if event.ToolUseChunk != nil && event.ToolUseChunk.ToolUseID == toolUseID && event.ToolUseChunk.Stop {
			return true
		}
	}
	return false
}

func goParsedEventsContainUsage(events []ParsedStreamEvent, key string, want int) bool {
	for _, event := range events {
		if event.Usage == nil {
			continue
		}
		var got int
		switch key {
		case "promptTokens":
			got = event.Usage.PromptTokens
		case "completionTokens":
			got = event.Usage.CompletionTokens
		case "reasoningTokens":
			got = event.Usage.ReasoningTokens
		case "cacheCreation1hTokens":
			got = event.Usage.CacheCreation1hTokens
		default:
			continue
		}
		if got == want {
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
