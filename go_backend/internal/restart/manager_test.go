package restart

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestManagerGracefulShutdownTokenGate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	called := make(chan struct{}, 1)
	m := NewManager(RuntimeAddress{Protocol: "http", Host: "127.0.0.1", Port: 7778}, func(ctx context.Context, req GracefulShutdownRequest) (GracefulShutdownResult, error) {
		called <- struct{}{}
		return GracefulShutdownResult{Success: true, Reason: "replacement_started", PID: os.Getpid()}, nil
	})
	status, _ := m.Handle(context.Background(), GracefulShutdownRequest{Token: "none"})
	if status != http.StatusConflict {
		t.Fatalf("expected 409 without session, got %d", status)
	}
	session, err := m.Begin()
	if err != nil {
		t.Fatal(err)
	}
	status, _ = m.Handle(context.Background(), GracefulShutdownRequest{Token: "bad"})
	if status != http.StatusForbidden {
		t.Fatalf("expected 403 for bad token, got %d", status)
	}
	status, body := m.Handle(context.Background(), GracefulShutdownRequest{Token: session.Token})
	if status != http.StatusOK || body["success"] != true {
		t.Fatalf("expected accepted shutdown, got %d %#v", status, body)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown callback was not called")
	}
}

func TestStartupHandoffPostsToOldProcess(t *testing.T) {
	got := make(chan GracefulShutdownRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/gracefully_shutdown" {
			var req GracefulShutdownRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			got <- req
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
			return
		}
		http.Error(w, "closed", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	t.Setenv(EnvURL, server.URL+"/api/gracefully_shutdown")
	t.Setenv(EnvToken, "secret")
	t.Setenv(EnvMarkerPath, "/tmp/marker.json")
	t.Setenv(EnvMarkerNonce, "nonce")
	if err := StartupHandoffFromEnv(context.Background(), "9.9.9"); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	if value := strings.TrimSpace(os.Getenv(EnvURL)); value != "" || strings.TrimSpace(os.Getenv(EnvToken)) != "" || strings.TrimSpace(os.Getenv(EnvMarkerPath)) != "" || strings.TrimSpace(os.Getenv(EnvMarkerNonce)) != "" {
		t.Fatalf("expected graceful restart env to be consumed, got url=%q token=%q markerPath=%q markerNonce=%q", os.Getenv(EnvURL), os.Getenv(EnvToken), os.Getenv(EnvMarkerPath), os.Getenv(EnvMarkerNonce))
	}
	select {
	case req := <-got:
		if req.Token != "secret" || req.Version != "9.9.9" || req.PID == 0 {
			t.Fatalf("unexpected handoff request: %#v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("old process did not receive handoff")
	}
}
