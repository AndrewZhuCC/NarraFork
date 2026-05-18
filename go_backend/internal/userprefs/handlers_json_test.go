package userprefs

import (
	"database/sql"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"
)

func TestUserPreferencesLoadDecodesJSONFields(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	_, err = sqlDB.Exec(`CREATE TABLE user_preferences (
		id TEXT PRIMARY KEY, user_id TEXT, auto_load_older_messages INTEGER DEFAULT 0,
		fast_mode_default INTEGER DEFAULT 0, language TEXT, word_wrap_markdown INTEGER DEFAULT 0,
		word_wrap_code INTEGER DEFAULT 0, word_wrap_diff INTEGER DEFAULT 0,
		reply_in_user_language INTEGER DEFAULT 0, show_token_usage INTEGER DEFAULT 0,
		show_output_stats INTEGER DEFAULT 0, terminal_theme TEXT, terminal_font_size INTEGER,
		recent_tabs TEXT, add_subagent_to_recent_tabs INTEGER DEFAULT 0, notify_on_done INTEGER DEFAULT 0,
		notify_on_waiting INTEGER DEFAULT 0, notify_pwa_enabled INTEGER DEFAULT 0,
		notify_sound_enabled INTEGER DEFAULT 0, notify_sound_type TEXT, notify_sound_builtin TEXT,
		notify_sound_file_id TEXT, notify_dingtalk_enabled INTEGER DEFAULT 0, notify_dingtalk_webhook TEXT,
		notify_dingtalk_secret TEXT, notify_feishu_enabled INTEGER DEFAULT 0, notify_feishu_webhook TEXT,
		notify_feishu_secret TEXT, commands TEXT, graph_viewports TEXT, send_mode TEXT,
		setup_wizard_completed INTEGER DEFAULT 0, gateway_config TEXT, created_at TEXT, updated_at TEXT
	)`)
	if err != nil {
		t.Fatalf("create user_preferences table: %v", err)
	}
	_, err = sqlDB.Exec(`INSERT INTO user_preferences (id, user_id, recent_tabs, commands, graph_viewports, gateway_config, created_at, updated_at) VALUES ('pref1', 'u1', ?, ?, ?, ?, 'now', 'now')`,
		`[{"type":"narrator","id":"n1"}]`,
		`[{"name":"ship","prompt":"/ship"}]`,
		`{"p1":{"x":1,"y":2}}`,
		`{"enabled":true}`,
	)
	if err != nil {
		t.Fatalf("insert prefs: %v", err)
	}

	prefs, err := (&Handler{DB: sqlDB}).load(httptest.NewRequest("GET", "/", nil), "u1")
	if err != nil {
		t.Fatalf("load prefs: %v", err)
	}
	if _, ok := prefs["recentTabs"].([]any); !ok {
		t.Fatalf("recentTabs = %#v, want array", prefs["recentTabs"])
	}
	if _, ok := prefs["commands"].([]any); !ok {
		t.Fatalf("commands = %#v, want array", prefs["commands"])
	}
	if _, ok := prefs["graphViewports"].(map[string]any); !ok {
		t.Fatalf("graphViewports = %#v, want object", prefs["graphViewports"])
	}
	if _, ok := prefs["gatewayConfig"].(map[string]any); !ok {
		t.Fatalf("gatewayConfig = %#v, want object", prefs["gatewayConfig"])
	}
}
