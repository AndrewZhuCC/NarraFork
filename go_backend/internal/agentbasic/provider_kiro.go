package agentbasic

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type KiroProviderAdapter struct {
	Provider Provider
	Client   *http.Client
}

func (a *KiroProviderAdapter) ProviderName() string {
	if a.Provider.Prefix != "" {
		return a.Provider.Prefix
	}
	return "kiro"
}

func formatExtraDetails(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case float64, int, bool:
		return fmt.Sprint(typed)
	case map[string]any:
		lines := []string{}
		keys := make([]string, 0, len(typed))
		for key, entryValue := range typed {
			if entryValue != nil {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			entryValue := typed[key]
			switch v := entryValue.(type) {
			case string:
				lines = append(lines, key+": "+v)
			case float64, int, bool:
				lines = append(lines, key+": "+fmt.Sprint(v))
			default:
				encoded, err := json.Marshal(v)
				if err == nil {
					lines = append(lines, key+": "+string(encoded))
				}
			}
		}
		return strings.Join(lines, "\n")
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}

func (a *KiroProviderAdapter) FormatTools(toolScope string) []map[string]any {
	return kiroToolDefinitionsFromSpecs(visibleToolSpecs(toolScope))
}

func (a *KiroProviderAdapter) BuildHistory(messages []Message, model string, _ string) (BuiltHistory, error) {
	history := make([]any, 0, len(messages))
	trailingUser := ""
	pendingToolResults := []any{}
	for i, message := range messages {
		isLast := i == len(messages)-1
		switch message.Role {
		case "assistant":
			if len(pendingToolResults) > 0 {
				history = append(history, kiroUserMessage("", model, nil, pendingToolResults))
				pendingToolResults = nil
			}
			entry := map[string]any{"assistantResponseMessage": map[string]any{"content": message.Content}}
			if len(message.ToolCalls) > 0 {
				entry["assistantResponseMessage"].(map[string]any)["toolUses"] = kiroToolUses(message.ToolCalls)
			}
			history = append(history, entry)
		case "tool":
			if strings.TrimSpace(message.ToolCallID) != "" {
				pendingToolResults = append(pendingToolResults, map[string]any{"toolUseId": message.ToolCallID, "content": []map[string]any{{"text": message.Content}}, "status": "success", "isError": false})
			}
		default:
			if isLast {
				trailingUser = message.Content
				continue
			}
			history = append(history, kiroUserMessage(message.Content, model, nil, pendingToolResults))
			pendingToolResults = nil
		}
	}
	return BuiltHistory{History: history, TrailingUserText: trailingUser, TrailingToolResults: pendingToolResults}, nil
}

func (a *KiroProviderAdapter) InjectSystemPrompt(history *BuiltHistory, systemPrompt string, model string, _ string) {
	if history == nil || strings.TrimSpace(systemPrompt) == "" {
		return
	}
	ack := map[string]any{"assistantResponseMessage": map[string]any{"content": "OK"}}
	history.History = append([]any{kiroUserMessage(systemPrompt, model, nil, nil), ack}, history.History...)
}

func (a *KiroProviderAdapter) Chat(ctx context.Context, params ChatParams) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 16)
	client := a.Client
	if client == nil {
		client = defaultProviderHTTPClient()
	}
	go func() {
		defer close(ch)
		if err := streamKiroChat(ctx, client, a.Provider, params, ch); err != nil {
			ch <- StreamEvent{Err: err}
		}
	}()
	return ch, nil
}

func (a *KiroProviderAdapter) FormatToolResult(toolUseID string, output string, isError bool) any {
	status := "success"
	if isError {
		status = "error"
	}
	return map[string]any{"toolUseId": toolUseID, "content": []map[string]any{{"text": output}}, "status": status, "isError": isError}
}

func (a *KiroProviderAdapter) PushUserTurn(history *BuiltHistory, content string, toolResults []any) {
	if history == nil {
		return
	}
	history.TrailingUserText = content
	history.TrailingToolResults = toolResults
	history.History = append(history.History, kiroUserMessage(content, a.Provider.Model, nil, toolResults))
}

func (a *KiroProviderAdapter) PushAssistantTurn(history *BuiltHistory, text string, toolUses []ToolCall, _ any) {
	if history == nil {
		return
	}
	assistant := map[string]any{"content": text}
	if len(toolUses) > 0 {
		assistant["toolUses"] = kiroToolUses(toolUses)
	}
	history.History = append(history.History, map[string]any{"assistantResponseMessage": assistant})
}

func streamKiroChat(ctx context.Context, client *http.Client, provider Provider, params ChatParams, out chan<- StreamEvent) error {
	body := buildKiroChatRequest(provider.Model, params)
	if provider.BaseURL == "kiro-plugin" {
		return streamKiroPluginBridge(ctx, provider, body, out)
	}
	if provider.Kind == "nkp" {
		prepareNkpRequest(body)
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, kiroChatEndpoint(provider.BaseURL), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+provider.APIKey)
	if provider.Kind == "nkp" {
		req.Header.Set("X-NKP-Request-Token", buildNkpRequestToken(provider.APIKey))
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		bodyText := readProviderErrorBody(res)
		if bodyText != "" {
			return fmt.Errorf("kiro provider returned status %d: %s", res.StatusCode, bodyText)
		}
		return fmt.Errorf("kiro provider returned status %d", res.StatusCode)
	}
	if !isSSEContentType(res.Header) {
		return errors.New("kiro provider returned non-SSE response")
	}
	completed := false
	err = readSSE(ctx, res.Body, 0, func(event sseEvent) error {
		data := strings.TrimSpace(event.Data)
		if data == "" {
			return nil
		}
		if data == "[DONE]" {
			completed = true
			return nil
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return fmt.Errorf("decode kiro stream event: %w", err)
		}
		eventType := strings.TrimSpace(event.Event)
		if eventType == "" {
			eventType = stringValue(payload["eventType"])
		}
		if eventType == "" {
			eventType = stringValue(payload["type"])
		}
		if eventType == "" {
			eventType = "assistantResponseEvent"
		}
		parsed := parseKiroEvent(eventType, payload)
		if parsed.StopReason != "" || eventType == "message_stop" || eventType == "response.completed" || eventType == "done" || eventType == "complete" {
			completed = true
		}
		emitStreamEvent(ctx, out, StreamEvent{Parsed: parsed})
		return nil
	})
	if err != nil {
		return err
	}
	if !completed {
		emitStreamEvent(ctx, out, StreamEvent{Parsed: ParsedStreamEvent{SilentDisconnect: true}})
	}
	return nil
}

func defaultKiroBridgeScriptPath() string {
	cwdPath := filepath.Join("scripts", "kiro-bridge.ts")
	if _, err := os.Stat(cwdPath); err == nil {
		return cwdPath
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return cwdPath
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "scripts", "kiro-bridge.ts"))
}

func streamKiroPluginBridge(ctx context.Context, provider Provider, request map[string]any, out chan<- StreamEvent) error {
	bridge := strings.TrimSpace(os.Getenv("NARRAFORK_KIRO_BRIDGE_CMD"))
	args := []string{}
	if bridge == "" {
		bridge = "bun"
		args = []string{defaultKiroBridgeScriptPath()}
	} else {
		parts := strings.Fields(bridge)
		bridge = parts[0]
		if len(parts) > 1 {
			args = parts[1:]
		}
	}
	cmd := exec.CommandContext(ctx, bridge, args...)
	if provider.KiroCredentialsPath != "" {
		cmd.Env = append(os.Environ(), "NARRAFORK_KIRO_CREDENTIALS_PATH="+provider.KiroCredentialsPath, "NARRAFORK_KIRO_CONFIG_PATH="+provider.KiroConfigPath, "NARRAFORK_KIRO_DEFAULT_MODEL="+provider.Model)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		defer stdin.Close()
		_ = json.NewEncoder(stdin).Encode(request)
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw struct {
			EventType string         `json:"eventType"`
			Data      map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return fmt.Errorf("decode kiro bridge event: %w", err)
		}
		if raw.EventType == "" {
			raw.EventType = stringValue(raw.Data["type"])
		}
		if raw.EventType == "" {
			raw.EventType = "assistantResponseEvent"
		}
		emitStreamEvent(ctx, out, StreamEvent{Parsed: parseKiroEvent(raw.EventType, raw.Data)})
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return cmd.Wait()
}

func buildKiroChatRequest(model string, params ChatParams) map[string]any {
	history := []any{}
	currentContent := params.Content
	toolResults := []any(nil)
	if params.History != nil {
		history = params.History.History
		if strings.TrimSpace(currentContent) == "" {
			currentContent = params.History.TrailingUserText
		}
		toolResults = params.History.TrailingToolResults
	}
	tools := params.Tools
	if tools == nil {
		tools = []map[string]any{}
	}
	conversationID := strings.TrimSpace(params.ConversationID)
	if conversationID == "" {
		conversationID = newKiroConversationID()
	}
	contextValue := map[string]any{"tools": tools}
	if len(toolResults) > 0 {
		contextValue["toolResults"] = toolResults
	}
	state := map[string]any{
		"conversationId":      conversationID,
		"chatTriggerType":     "MANUAL",
		"agentTaskType":       "vibe",
		"agentContinuationId": conversationID,
		"currentMessage": map[string]any{"userInputMessage": map[string]any{
			"content":                 currentContent,
			"modelId":                 model,
			"origin":                  "AI_EDITOR",
			"userInputMessageContext": contextValue,
		}},
		"envState": map[string]any{
			"operatingSystem":         kiroOperatingSystem(),
			"currentWorkingDirectory": params.Cwd,
		},
		"shellState": map[string]any{"shellName": "bash"},
	}
	if len(history) > 0 {
		state["history"] = history
	}
	return normalizeKiroChatRequest(map[string]any{"conversationState": state})
}

func normalizeKiroChatRequest(request map[string]any) map[string]any {
	state, _ := request["conversationState"].(map[string]any)
	if state == nil {
		return request
	}
	current, _ := state["currentMessage"].(map[string]any)
	currentUser, _ := current["userInputMessage"].(map[string]any)
	if currentUser == nil {
		return request
	}
	modelID := stringValue(currentUser["modelId"])
	entries := []kiroRequestEntry{}
	for _, item := range readArray(state["history"]) {
		if message, ok := item.(map[string]any); ok {
			entries = append(entries, kiroRequestEntry{message: message})
		}
	}
	entries = append(entries, kiroRequestEntry{message: map[string]any{"userInputMessage": currentUser}, isCurrent: true})
	for i := 0; i < 3; i++ {
		entries = compactKiroRequestEntries(entries, modelID)
		entries = sanitizeKiroToolPairs(entries)
	}
	currentIndex := -1
	for i, entry := range entries {
		if entry.isCurrent {
			currentIndex = i
			break
		}
	}
	if currentIndex < 0 {
		currentIndex = len(entries) - 1
	}
	before := []any{}
	for i := 0; i < currentIndex && i < len(entries); i++ {
		before = append(before, entries[i].message)
	}
	if len(before) > 0 {
		state["history"] = before
	} else {
		delete(state, "history")
	}
	if currentIndex >= 0 && currentIndex < len(entries) {
		if user := kiroUserContent(entries[currentIndex].message); user != nil {
			state["currentMessage"] = map[string]any{"userInputMessage": user}
		}
	}
	return request
}

type kiroRequestEntry struct {
	message   map[string]any
	isCurrent bool
}

func compactKiroRequestEntries(entries []kiroRequestEntry, modelID string) []kiroRequestEntry {
	out := []kiroRequestEntry{}
	for _, entry := range entries {
		if kiroIsEmptyAssistant(entry.message) {
			continue
		}
		if !entry.isCurrent && kiroUserContent(entry.message) != nil && !kiroUserHasPayload(entry.message) {
			continue
		}
		if user := kiroUserContent(entry.message); user != nil {
			kiroCleanupUserContext(user)
			if len(out) > 0 && kiroUserContent(out[len(out)-1].message) != nil {
				if entry.isCurrent {
					out = append(out, kiroRequestEntry{message: kiroSyntheticAssistantAck()})
					out = append(out, entry)
				} else {
					kiroMergeUsers(kiroUserContent(out[len(out)-1].message), user)
					out[len(out)-1].isCurrent = out[len(out)-1].isCurrent || entry.isCurrent
				}
				continue
			}
			out = append(out, entry)
			continue
		}
		if assistant := kiroAssistantContent(entry.message); assistant != nil {
			if len(out) > 0 {
				if previousAssistant := kiroAssistantContent(out[len(out)-1].message); previousAssistant != nil {
					kiroMergeAssistants(previousAssistant, assistant)
					continue
				}
			} else {
				out = append(out, kiroRequestEntry{message: kiroSyntheticUser(modelID)})
			}
			out = append(out, entry)
		}
	}
	return out
}

func sanitizeKiroToolPairs(entries []kiroRequestEntry) []kiroRequestEntry {
	consumed := map[string]bool{}
	keep := make([]bool, len(entries))
	for i := range keep {
		keep[i] = true
	}
	for i, entry := range entries {
		user := kiroUserContent(entry.message)
		if user == nil {
			continue
		}
		ctx := kiroUserContext(user)
		incoming := readArray(ctx["toolResults"])
		var previousAssistant map[string]any
		if i > 0 {
			previousAssistant = kiroAssistantContent(entries[i-1].message)
		}
		expected := map[string]map[string]any{}
		for _, item := range readArray(previousAssistant["toolUses"]) {
			if toolUse, ok := item.(map[string]any); ok {
				id := stringValue(toolUse["toolUseId"])
				if id != "" && !consumed[id] {
					expected[id] = toolUse
				}
			}
		}
		keptResults := []any{}
		matched := map[string]bool{}
		for _, item := range incoming {
			result, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id := stringValue(result["toolUseId"])
			if _, ok := expected[id]; !ok || matched[id] {
				continue
			}
			keptResults = append(keptResults, result)
			matched[id] = true
			consumed[id] = true
		}
		if len(keptResults) > 0 {
			ctx["toolResults"] = keptResults
		} else {
			delete(ctx, "toolResults")
		}
		user["userInputMessageContext"] = ctx
		if previousAssistant != nil && len(expected) > 0 {
			filtered := []any{}
			for _, item := range readArray(previousAssistant["toolUses"]) {
				if toolUse, ok := item.(map[string]any); ok && matched[stringValue(toolUse["toolUseId"])] {
					filtered = append(filtered, toolUse)
				}
			}
			if len(filtered) > 0 {
				previousAssistant["toolUses"] = filtered
			} else {
				delete(previousAssistant, "toolUses")
			}
			if kiroAssistantMapIsEmpty(previousAssistant) {
				keep[i-1] = false
			}
		}
		if !entry.isCurrent && !kiroUserHasPayload(entry.message) {
			keep[i] = false
		}
	}
	out := make([]kiroRequestEntry, 0, len(entries))
	for i, entry := range entries {
		if keep[i] {
			out = append(out, entry)
		}
	}
	return out
}

func kiroUserContent(message map[string]any) map[string]any {
	user, _ := message["userInputMessage"].(map[string]any)
	return user
}

func kiroAssistantContent(message map[string]any) map[string]any {
	assistant, _ := message["assistantResponseMessage"].(map[string]any)
	return assistant
}

func kiroUserContext(user map[string]any) map[string]any {
	ctx, _ := user["userInputMessageContext"].(map[string]any)
	if ctx == nil {
		ctx = map[string]any{}
		user["userInputMessageContext"] = ctx
	}
	return ctx
}

func kiroCleanupUserContext(user map[string]any) {
	ctx := kiroUserContext(user)
	if len(readArray(ctx["toolResults"])) == 0 {
		delete(ctx, "toolResults")
	}
}

func kiroUserHasPayload(message map[string]any) bool {
	user := kiroUserContent(message)
	if user == nil {
		return false
	}
	ctx := kiroUserContext(user)
	return strings.TrimSpace(stringValue(user["content"])) != "" || len(readArray(user["images"])) > 0 || len(readArray(ctx["tools"])) > 0 || len(readArray(ctx["toolResults"])) > 0
}

func kiroIsEmptyAssistant(message map[string]any) bool {
	assistant := kiroAssistantContent(message)
	return assistant != nil && kiroAssistantMapIsEmpty(assistant)
}

func kiroAssistantMapIsEmpty(assistant map[string]any) bool {
	return strings.TrimSpace(stringValue(assistant["content"])) == "" && len(readArray(assistant["toolUses"])) == 0
}

func kiroSyntheticUser(modelID string) map[string]any {
	return map[string]any{"userInputMessage": map[string]any{"content": "", "modelId": modelID, "origin": "AI_EDITOR", "userInputMessageContext": map[string]any{}}}
}

func kiroSyntheticAssistantAck() map[string]any {
	return map[string]any{"assistantResponseMessage": map[string]any{"content": "OK"}}
}

func kiroMergeUsers(target, source map[string]any) {
	if content := stringValue(source["content"]); content != "" {
		if existing := stringValue(target["content"]); existing != "" {
			target["content"] = existing + "\n" + content
		} else {
			target["content"] = content
		}
	}
	targetCtx := kiroUserContext(target)
	sourceCtx := kiroUserContext(source)
	if tools := readArray(sourceCtx["tools"]); len(tools) > 0 {
		targetCtx["tools"] = tools
	}
	mergedResults := append(readArray(targetCtx["toolResults"]), readArray(sourceCtx["toolResults"])...)
	if len(mergedResults) > 0 {
		targetCtx["toolResults"] = mergedResults
	}
}

func kiroMergeAssistants(target, source map[string]any) {
	if content := stringValue(source["content"]); content != "" {
		if existing := stringValue(target["content"]); existing != "" {
			target["content"] = existing + "\n" + content
		} else {
			target["content"] = content
		}
	}
	if toolUses := readArray(source["toolUses"]); len(toolUses) > 0 {
		target["toolUses"] = append(readArray(target["toolUses"]), toolUses...)
	}
}

func newKiroConversationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("00000000-0000-4000-8000-%012x", time.Now().UnixNano()&0xffffffffffff)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func parseKiroEvent(eventType string, data map[string]any) ParsedStreamEvent {
	result := ParsedStreamEvent{}
	if usage := kiroUsageSnapshot(data); usage != nil {
		result.Usage = usage
	}
	switch eventType {
	case "assistantResponseEvent":
		if content := stringValue(data["content"]); content != "" {
			result.Text = content
		}
		result.MessageID = stringValue(data["messageId"])
		result.ConversationID = stringValue(data["conversationId"])
		if stopReason := firstNonEmpty(stringValue(data["stopReason"]), stringValue(data["stop_reason"])); stopReason != "" {
			result.StopReason = stopReason
			if stopReason == "max_tokens" || stopReason == "max_output_tokens" || stopReason == "length" {
				result.InvalidState = &InvalidState{Reason: "max_tokens", Message: "Response truncated: model reached maximum token limit."}
			}
		}
		if toolUse, ok := data["toolUse"].(map[string]any); ok {
			result.ToolUses = []ToolCall{kiroToolCall(toolUse)}
		}
		for _, item := range readArray(data["toolUses"]) {
			if toolUse, ok := item.(map[string]any); ok {
				result.ToolUses = append(result.ToolUses, kiroToolCall(toolUse))
			}
		}
	case "toolUseEvent":
		if toolUseID := stringValue(data["toolUseId"]); toolUseID != "" {
			result.ToolUseChunk = &ToolUseChunk{ToolUseID: toolUseID, Name: stringValue(data["name"]), Input: stringValue(data["input"]), Stop: boolValue(data["stop"])}
		}
	case "reasoningContentEvent":
		result.Reasoning = firstNonEmpty(stringValue(data["content"]), stringValue(data["text"]))
	case "contextUsageEvent":
		if pct, ok := kiroNumberValue(data["contextUsagePercentage"]); ok {
			result.ContextUsagePercentage = &pct
		} else if pct, ok := kiroNumberValue(data["context_usage_percentage"]); ok {
			result.ContextUsagePercentage = &pct
		}
	case "credentialIdEvent":
		result.CredentialID = stringValue(data["credentialId"])
	case "meteringEvent":
		if usage, ok := kiroNumberValue(data["usage"]); ok {
			result.Metering = &MeteringSnapshot{Unit: firstNonEmpty(stringValue(data["unit"]), "credit"), UnitPlural: firstNonEmpty(stringValue(data["unitPlural"]), "credits"), Usage: usage}
		}
	case "invalidStateEvent":
		result.InvalidState = &InvalidState{Reason: firstNonEmpty(stringValue(data["reason"]), "unknown"), Message: firstNonEmpty(stringValue(data["message"]), stringValue(data["content"]), "Invalid state")}
	case "error":
		result.InvalidState = &InvalidState{Reason: "kiro_error", Message: firstNonEmpty(stringValue(data["message"]), "Kiro provider error")}
	case "nkpQuotaBalanceEvent":
		if balance, ok := kiroNumberValue(data["quotaBalance"]); ok {
			result.NkpQuotaBalance = &balance
		}
	case "nkpQueueEvent":
		position := intValue(data["position"], 0)
		depth := intValue(data["queueDepth"], 0)
		result.NkpQueue = &QueueSnapshot{Position: position, QueueDepth: depth, QueueMessage: stringValue(data["message"])}
	case "queueEvent":
		position := intValue(data["position"], 0)
		depth := intValue(data["queueDepth"], 0)
		result.QueueStatus = &QueueSnapshot{Position: position, QueueDepth: depth, QueueMessage: stringValue(data["message"])}
	case "quotaBalanceEvent":
		balance := firstNonEmpty(stringValue(data["quotaBalance"]), stringValue(data["balance"]), stringValue(data["message"]))
		detailedBalance := firstNonEmpty(stringValue(data["detailedQuotaBalance"]), formatExtraDetails(data["extra"]))
		if balance != "" {
			result.QuotaBalance = &balance
			result.DetailedQuotaBalance = &detailedBalance
		}
	}
	return result
}

func kiroToolCall(raw map[string]any) ToolCall {
	input := raw["input"]
	args, err := json.Marshal(input)
	if err != nil || string(args) == "null" {
		args = []byte("{}")
	}
	return newFunctionToolCall(stringValue(raw["toolUseId"]), stringValue(raw["name"]), string(args))
}

func kiroToolUses(calls []ToolCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		var input any = map[string]any{}
		_ = json.Unmarshal([]byte(call.Function.Arguments), &input)
		out = append(out, map[string]any{"toolUseId": call.ID, "name": call.Function.Name, "input": input})
	}
	return out
}

func kiroUserMessage(content, model string, tools []map[string]any, toolResults []any) map[string]any {
	ctx := map[string]any{}
	if len(tools) > 0 {
		ctx["tools"] = tools
	}
	if len(toolResults) > 0 {
		ctx["toolResults"] = toolResults
	}
	return map[string]any{"userInputMessage": map[string]any{"content": content, "modelId": model, "origin": "AI_EDITOR", "userInputMessageContext": ctx}}
}

func kiroUsageSnapshot(data map[string]any) *UsageSnapshot {
	usageValue := data["usage"]
	if usageValue == nil {
		usageValue = data
	}
	usage, ok := usageValue.(map[string]any)
	if !ok {
		return nil
	}
	input := intValue(firstPresent(usage, "inputTokens", "input_tokens", "promptTokens", "prompt_tokens"), 0)
	output := intValue(firstPresent(usage, "outputTokens", "output_tokens", "completionTokens", "completion_tokens"), 0)
	cached := intValue(firstPresent(usage, "cachedInputTokens", "cached_input_tokens", "cacheReadInputTokens", "cache_read_input_tokens"), 0)
	reasoning := intValue(firstPresent(usage, "reasoningTokens", "reasoning_tokens"), 0)
	if input == 0 && output == 0 && cached == 0 && reasoning == 0 {
		return nil
	}
	return &UsageSnapshot{InputTokens: input, PromptTokens: input, CompletionTokens: output, CachedInputTokens: cached, ReasoningTokens: reasoning}
}

func firstPresent(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func kiroNumberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		v, err := typed.Float64()
		return v, err == nil
	default:
		return 0, false
	}
}

func ensureNonEmptySchema(schema map[string]any) map[string]any {
	if schema == nil || len(schema) == 0 {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if schema["type"] == nil {
		schema["type"] = "object"
	}
	if schema["properties"] == nil {
		schema["properties"] = map[string]any{}
	}
	return schema
}

func prepareNkpRequest(request map[string]any) {
	state, _ := request["conversationState"].(map[string]any)
	if state == nil {
		return
	}
	ts := time.Now().Unix()
	conversationID := stringValue(state["conversationId"])
	modelID := ""
	if current, _ := state["currentMessage"].(map[string]any); current != nil {
		if user, _ := current["userInputMessage"].(map[string]any); user != nil {
			modelID = stringValue(user["modelId"])
		}
	}
	mac := hmac.New(sha256.New, []byte("narrafork-nkp-signature-v1::HMAC-SHA256::9d87cae2f1b54a7e"))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d:%s:%s", ts, conversationID, modelID)))
	state["workspaceState"] = map[string]any{"sessionEpoch": ts, "traceId": hex.EncodeToString(mac.Sum(nil))}
}

func buildNkpRequestToken(apiKey string) string {
	ts := time.Now().Unix()
	sum := md5.Sum([]byte(fmt.Sprintf("%s%d", apiKey, ts)))
	hash := hex.EncodeToString(sum[:])[:16]
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%s", ts, hash)))
}

func kiroChatEndpoint(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/kiro/chat"
	}
	return baseURL + "/v1/kiro/chat"
}

func kiroOperatingSystem() string {
	switch runtime.GOOS {
	case "windows":
		return "WINDOWS"
	case "darwin":
		return "MAC"
	default:
		return "LINUX"
	}
}
