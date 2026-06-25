package narrators

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// firstSystemMessageText returns the concatenated text content of the first
// system message in an OpenAI-style request body.
func firstSystemMessageText(body map[string]any) string {
	messages, _ := body["messages"].([]any)
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if stringFromAny(msg["role"]) != "system" {
			continue
		}
		switch content := msg["content"].(type) {
		case string:
			return content
		case []any:
			parts := []string{}
			for _, c := range content {
				if cm, ok := c.(map[string]any); ok {
					parts = append(parts, stringFromAny(cm["text"]))
				}
			}
			return strings.Join(parts, "")
		}
	}
	return ""
}

// TestAskQuestionReflectionSystemPromptMatchesTS exercises the helper directly,
// mirroring TS tool.suggestAnswerSystem / tool.questionReflectionSystem.
func TestAskQuestionReflectionSystemPromptMatchesTS(t *testing.T) {
	svc := &Service{}
	suggestEn := svc.askQuestionReflectionSystemPrompt("suggest", "en")
	for _, want := range []string{
		"senior software engineering advisor",
		"the full conversation context in <conversation> tags",
		"No explanation, no markdown fences.",
	} {
		if !strings.Contains(suggestEn, want) {
			t.Fatalf("suggest en prompt missing %q:\n%s", want, suggestEn)
		}
	}
	suggestZh := svc.askQuestionReflectionSystemPrompt("suggest", "zh-CN")
	for _, want := range []string{"资深软件工程顾问", "完整对话上下文", "不要 markdown 代码块。"} {
		if !strings.Contains(suggestZh, want) {
			t.Fatalf("suggest zh prompt missing %q:\n%s", want, suggestZh)
		}
	}
	if strings.Contains(suggestZh, "senior software engineering advisor") {
		t.Fatalf("suggest zh prompt must not contain english text:\n%s", suggestZh)
	}

	reflEn := svc.askQuestionReflectionSystemPrompt("reflection", "en")
	if !strings.Contains(reflEn, "NarraFork's question reflection gate") {
		t.Fatalf("reflection en prompt mismatch:\n%s", reflEn)
	}
	reflZh := svc.askQuestionReflectionSystemPrompt("reflection", "zh-CN")
	if !strings.Contains(reflZh, "问题反思关卡") {
		t.Fatalf("reflection zh prompt mismatch:\n%s", reflZh)
	}
	if strings.Contains(reflZh, "question reflection gate") {
		t.Fatalf("reflection zh prompt must not contain english text:\n%s", reflZh)
	}
}

// askQuestionSystemCaptureServer records the system-message text of the request.
func askQuestionSystemCaptureServer(t *testing.T, capture *string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		*capture = firstSystemMessageText(body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": `{"choice":"B"}`}}}})
	}))
	t.Cleanup(server.Close)
	return server
}

// TestReflectPendingAskUserQuestionUsesOwnerLocalePrompt verifies the reflection
// gate uses the narrator owner's locale for the system prompt.
func TestReflectPendingAskUserQuestionUsesOwnerLocalePrompt(t *testing.T) {
	var captured string
	var mu sync.Mutex
	server := askQuestionSystemCaptureServer(t, &captured, &mu)
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`CREATE TABLE user_preferences (user_id TEXT PRIMARY KEY, language TEXT)`); err != nil {
		t.Fatalf("create user_preferences: %v", err)
	}
	// Make the owner a zh-CN user: seed an owning user message + preference.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := service.DB.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, parent_tool_use_id, created_by, created_at) VALUES ('owner-msg', ?, 'user', NULL, 'user-zh', ?)`, narratorID, now); err != nil {
		t.Fatalf("insert owner message: %v", err)
	}
	if _, err := service.DB.Exec(`INSERT INTO user_preferences (user_id, language) VALUES ('user-zh', 'zh-CN')`); err != nil {
		t.Fatalf("insert pref: %v", err)
	}
	input := `{"questions":[{"question":"choice","header":"Pick one","options":[{"label":"A","description":"First"},{"label":"B","description":"Second"}],"multiSelect":false}]}`
	if _, err := service.DB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('req-q', ?, 'ask-1', 'AskUserQuestion', ?, 'pending', ?)`, narratorID, input, now); err != nil {
		t.Fatalf("insert pending question: %v", err)
	}
	if _, err := service.ReflectPendingAskUserQuestion(context.Background(), narratorID, "ask-1", false); err != nil {
		t.Fatalf("reflect pending question: %v", err)
	}
	mu.Lock()
	prompt := captured
	mu.Unlock()
	if !strings.Contains(prompt, "问题反思关卡") {
		t.Fatalf("reflection request should use owner zh-CN system prompt, got: %q", prompt)
	}
}

// TestSuggestAnswersUsesRequestUserLocalePrompt verifies the suggest route uses
// the requesting user's locale (userID "system" when no claims) for the prompt.
func TestSuggestAnswersUsesRequestUserLocalePrompt(t *testing.T) {
	var captured string
	var mu sync.Mutex
	server := askQuestionSystemCaptureServer(t, &captured, &mu)
	service, narratorID, _ := newTestNarratorService(t, "openai:gpt-test", server.URL)
	if _, err := service.DB.Exec(`CREATE TABLE user_preferences (user_id TEXT PRIMARY KEY, language TEXT)`); err != nil {
		t.Fatalf("create user_preferences: %v", err)
	}
	// No JWT claims -> handler userID falls back to "system"; set system zh-CN.
	if _, err := service.DB.Exec(`INSERT INTO user_preferences (user_id, language) VALUES ('system', 'zh-CN')`); err != nil {
		t.Fatalf("insert pref: %v", err)
	}
	router := chi.NewRouter()
	NewHandler(service).RegisterRoutes(router)
	reqBody := `{"questions":[{"question":"choice","header":"Pick one","options":[{"label":"A","description":"First"},{"label":"B","description":"Second"}],"multiSelect":false}]}`
	req := httptest.NewRequest(http.MethodPost, "/"+narratorID+"/suggest-answers", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("suggest-answers status = %d, body = %s", w.Code, w.Body.String())
	}
	mu.Lock()
	prompt := captured
	mu.Unlock()
	if !strings.Contains(prompt, "资深软件工程顾问") {
		t.Fatalf("suggest request should use request-user zh-CN system prompt, got: %q", prompt)
	}
}
