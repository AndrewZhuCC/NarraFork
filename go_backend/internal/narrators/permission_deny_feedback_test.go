package narrators

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestPermissionDenyBroadcastsFeedbackText verifies the deny route includes the
// denial reason as feedbackText in the permission_resolved WS payload so the
// frontend can render permissionDenyMessage on the tool card live, mirroring TS
// permission_resolved feedbackText (= message || feedbackText;
// narrator-permission.ts:2926-2928).
func TestPermissionDenyBroadcastsFeedbackText(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, drain := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('req-deny-fb', ?, 'deny-fb-tu', 'Bash', '{}', 'pending', ?)`, narratorID, now); err != nil {
		t.Fatalf("insert pending tool call: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/narrators/permissions/req-deny-fb/deny", strings.NewReader(`{"message":"unsafe command, denied"}`))
	req.Header.Set("Content-Type", "application/json")
	w := serveNarratorHandlerWithService(service, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected deny 200, got %d body=%s", w.Code, w.Body.String())
	}

	resolved := findBroadcast(drain(), narratorID, "permission_resolved")
	if resolved == nil {
		t.Fatalf("expected permission_resolved broadcast, got %#v", drain())
	}
	if resolved["decision"] != "deny" {
		t.Fatalf("decision = %v, want deny", resolved["decision"])
	}
	if resolved["feedbackText"] != "unsafe command, denied" {
		t.Fatalf("feedbackText = %v, want denial message", resolved["feedbackText"])
	}
}

// TestPermissionDenyFeedbackTextFallsBackToFeedbackField verifies that when no
// message is provided, the body's feedbackText field is used (mirrors TS
// denyMessage || feedbackText precedence).
func TestPermissionDenyFeedbackTextFallsBackToFeedbackField(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, drain := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('req-deny-fb2', ?, 'deny-fb2-tu', 'Bash', '{}', 'pending', ?)`, narratorID, now); err != nil {
		t.Fatalf("insert pending tool call: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/narrators/permissions/req-deny-fb2/deny", strings.NewReader(`{"feedbackText":"please use a safer approach"}`))
	req.Header.Set("Content-Type", "application/json")
	w := serveNarratorHandlerWithService(service, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected deny 200, got %d body=%s", w.Code, w.Body.String())
	}

	resolved := findBroadcast(drain(), narratorID, "permission_resolved")
	if resolved == nil {
		t.Fatalf("expected permission_resolved broadcast, got %#v", drain())
	}
	if resolved["feedbackText"] != "please use a safer approach" {
		t.Fatalf("feedbackText = %v, want feedbackText field value", resolved["feedbackText"])
	}
}

// TestPermissionApproveDoesNotIncludeFeedbackText verifies feedbackText is only
// attached on deny (TS gates it behind decision === "deny").
func TestPermissionApproveDoesNotIncludeFeedbackText(t *testing.T) {
	sqlDB := openNarratorHandlerTestDB(t)
	defer sqlDB.Close()
	narratorID := insertNarratorHandlerTestRow(t, sqlDB)
	hub, drain := captureNarratorBroadcasts()
	service := &Service{DB: sqlDB, Hub: hub}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(`INSERT INTO narrator_tool_calls (id, narrator_id, tool_use_id, tool_name, input_json, status, created_at) VALUES ('req-approve-fb', ?, 'approve-fb-tu', 'Bash', '{}', 'pending', ?)`, narratorID, now); err != nil {
		t.Fatalf("insert pending tool call: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/narrators/permissions/req-approve-fb/approve", strings.NewReader(`{"message":"looks fine"}`))
	req.Header.Set("Content-Type", "application/json")
	w := serveNarratorHandlerWithService(service, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected approve 200, got %d body=%s", w.Code, w.Body.String())
	}

	resolved := findBroadcast(drain(), narratorID, "permission_resolved")
	if resolved == nil {
		t.Fatalf("expected permission_resolved broadcast, got %#v", drain())
	}
	if resolved["decision"] != "allow" {
		t.Fatalf("decision = %v, want allow", resolved["decision"])
	}
	if _, present := resolved["feedbackText"]; present {
		t.Fatalf("approve must not include feedbackText, got %#v", resolved["feedbackText"])
	}
}
