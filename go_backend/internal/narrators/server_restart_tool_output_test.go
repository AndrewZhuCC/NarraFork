package narrators

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestRecoverOnStartupUsesTSServerRestartToolOutput verifies that the model-facing
// tool result output for tool calls interrupted by a server restart mirrors the TS
// tool.interruptedByServerRestart i18n message ("Tool execution was interrupted by
// a server restart.", i18n.ts:30-33), which is intentionally distinct from the
// internal errorMessage ("Interrupted by server restart"). TS resolves the output
// via getToolMessage("interruptedByServerRestart") WITHOUT a locale (recovery runs
// at process startup), so it is the English default (narrator-session.ts:4644/4662).
func TestRecoverOnStartupUsesTSServerRestartToolOutput(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)

	// Seed a stale "running" and a stale "initializing" tool call (no output yet).
	for _, tc := range []struct{ id, status string }{
		{"run-tc", "running"},
		{"init-tc", "initializing"},
	} {
		if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES (?, ?, ?, 'Bash', '{}', ?, '2026-05-18T00:00:00Z')`,
			tc.id, narratorID, tc.id, tc.status); err != nil {
			t.Fatalf("insert stale %s tool call: %v", tc.status, err)
		}
	}

	if err := (&Service{DB: sqlDB}).RecoverOnStartup(context.Background()); err != nil {
		t.Fatalf("recover on startup: %v", err)
	}

	for _, id := range []string{"run-tc", "init-tc"} {
		var status, errorMessage, outputJSON string
		if err := sqlDB.QueryRow(`SELECT status, COALESCE(error_message, ''), COALESCE(output_json, '') FROM narrator_tool_calls WHERE id = ?`, id).Scan(&status, &errorMessage, &outputJSON); err != nil {
			t.Fatalf("load recovered tool call %s: %v", id, err)
		}
		if status != "fail" {
			t.Fatalf("%s: expected status fail, got %s", id, status)
		}
		// Internal errorMessage stays the short internal string (matches TS errorMessage).
		if errorMessage != "Interrupted by server restart" {
			t.Fatalf("%s: expected internal errorMessage %q, got %q", id, "Interrupted by server restart", errorMessage)
		}
		// Model-facing output_json.output mirrors the TS interruptedByServerRestart text.
		var parsed map[string]any
		if err := json.Unmarshal([]byte(outputJSON), &parsed); err != nil {
			t.Fatalf("%s: output_json not JSON: %v (%q)", id, err, outputJSON)
		}
		output, _ := parsed["output"].(string)
		if output != "Tool execution was interrupted by a server restart." {
			t.Fatalf("%s: expected TS server-restart tool output, got %q", id, output)
		}
		// The model-facing output must NOT be the internal error string.
		if strings.Contains(output, "Interrupted by server restart") {
			t.Fatalf("%s: model-facing output must differ from internal errorMessage, got %q", id, output)
		}
	}
}
