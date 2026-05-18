package notifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestNotifyStatusChangedSendsConfiguredWebhookForUnreadNarrator(t *testing.T) {
	database := newNotificationTestDB(t)
	insertNotificationNarratorFixture(t, database)
	calls := []postedWebhook{}
	service := &Service{DB: database, Post: func(_ context.Context, endpoint string, payload []byte, provider string) error {
		calls = append(calls, postedWebhook{endpoint: endpoint, provider: provider, payload: string(payload)})
		return nil
	}}

	if err := service.NotifyStatusChanged(context.Background(), "nar-1", "idle", []string{"unread"}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected dingtalk and feishu calls, got %#v", calls)
	}
	if calls[0].provider != "DingTalk" || !strings.Contains(calls[0].endpoint, "timestamp=") || !strings.Contains(calls[0].endpoint, "sign=") {
		t.Fatalf("unexpected dingtalk call: %#v", calls[0])
	}
	if !strings.Contains(calls[0].payload, "Narrator One") || !strings.Contains(calls[0].payload, "Chapter One") || !strings.Contains(calls[0].payload, "done") {
		t.Fatalf("dingtalk payload missing title/chapter/status: %s", calls[0].payload)
	}
	if calls[1].provider != "Feishu" || calls[1].endpoint != "https://feishu.test/hook" {
		t.Fatalf("unexpected feishu call: %#v", calls[1])
	}
	var feishu map[string]any
	if err := json.Unmarshal([]byte(calls[1].payload), &feishu); err != nil {
		t.Fatalf("decode feishu payload: %v", err)
	}
	if feishu["msg_type"] != "interactive" || feishu["timestamp"] == nil || feishu["sign"] == nil {
		t.Fatalf("feishu payload missing interactive/sign: %#v", feishu)
	}
}

func TestNotifyStatusChangedHonorsPreferencesAndSilentSubstatus(t *testing.T) {
	database := newNotificationTestDB(t)
	insertNotificationNarratorFixture(t, database)
	calls := 0
	service := &Service{DB: database, Post: func(context.Context, string, []byte, string) error {
		calls++
		return nil
	}}

	if err := service.NotifyStatusChanged(context.Background(), "nar-1", "idle", []string{}); err != nil {
		t.Fatalf("notify idle: %v", err)
	}
	if err := service.NotifyStatusChanged(context.Background(), "nar-1", "idle", []string{"unread", "silent_notification"}); err != nil {
		t.Fatalf("notify silent: %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected no calls without unread or with silent_notification, got %d", calls)
	}
	if err := service.NotifyStatusChanged(context.Background(), "nar-1", "waiting", []string{}); err != nil {
		t.Fatalf("notify waiting: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected waiting notification on two enabled providers, got %d", calls)
	}
}

func TestAttachToHubHandlesOnlyStatusChangePayloads(t *testing.T) {
	database := newNotificationTestDB(t)
	insertNotificationNarratorFixture(t, database)
	calls := make(chan struct{}, 4)
	service := &Service{DB: database, Post: func(context.Context, string, []byte, string) error {
		calls <- struct{}{}
		return nil
	}}
	hub := &fakeNotificationHub{}
	service.AttachToHub(hub)
	if len(hub.hooks) != 1 {
		t.Fatalf("expected one hook, got %d", len(hub.hooks))
	}
	hub.hooks[0]("nar-1", map[string]any{"type": "status", "status": "idle", "substatus": []string{"unread"}}, nil)
	select {
	case <-calls:
		t.Fatal("legacy status frame should not notify directly")
	case <-time.After(50 * time.Millisecond):
	}
	hub.hooks[0]("nar-1", map[string]any{"type": "status_change", "status": "idle", "substatus": []string{"unread"}}, nil)
	waitNotificationCalls(t, calls, 2)
}

type postedWebhook struct {
	endpoint string
	provider string
	payload  string
}

type fakeNotificationHub struct {
	hooks []func(narratorID string, payload any, data []byte)
}

func (h *fakeNotificationHub) AddNarratorBroadcastHook(hook func(narratorID string, payload any, data []byte)) {
	h.hooks = append(h.hooks, hook)
}

func waitNotificationCalls(t *testing.T, calls <-chan struct{}, want int) {
	t.Helper()
	for i := 0; i < want; i++ {
		select {
		case <-calls:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for notification call %d/%d", i+1, want)
		}
	}
}

func newNotificationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	stmts := []string{
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, title TEXT, chapter_id TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, title TEXT)`,
		`CREATE TABLE user_preferences (id TEXT PRIMARY KEY, user_id TEXT, recent_tabs TEXT, notify_on_done INTEGER, notify_on_waiting INTEGER, notify_dingtalk_enabled INTEGER, notify_dingtalk_webhook TEXT, notify_dingtalk_secret TEXT, notify_feishu_enabled INTEGER, notify_feishu_webhook TEXT, notify_feishu_secret TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("exec schema: %v", err)
		}
	}
	return database
}

func insertNotificationNarratorFixture(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO chapters (id, title) VALUES ('chapter-1', 'Chapter One')`); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrators (id, title, chapter_id) VALUES ('nar-1', 'Narrator One', 'chapter-1')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	recentTabs := `[{"type":"narrator","id":"nar-1"}]`
	if _, err := database.Exec(`INSERT INTO user_preferences (id, user_id, recent_tabs, notify_on_done, notify_on_waiting, notify_dingtalk_enabled, notify_dingtalk_webhook, notify_dingtalk_secret, notify_feishu_enabled, notify_feishu_webhook, notify_feishu_secret) VALUES ('pref-1', 'user-1', ?, 1, 1, 1, 'https://dingtalk.test/hook?access_token=x', 'ding-secret', 1, 'https://feishu.test/hook', 'fei-secret')`, recentTabs); err != nil {
		t.Fatalf("insert prefs: %v", err)
	}
}
