package restart

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	EnvURL         = "NARRAFORK_GRACEFUL_RESTART_URL"
	EnvToken       = "NARRAFORK_GRACEFUL_RESTART_TOKEN"
	EnvMarkerPath  = "NARRAFORK_GRACEFUL_RESTART_MARKER_PATH"
	EnvMarkerNonce = "NARRAFORK_GRACEFUL_RESTART_MARKER_NONCE"
)

type RuntimeAddress struct {
	Protocol string
	Host     string
	Port     int
}

type GracefulShutdownRequest struct {
	Token   string `json:"token"`
	PID     int    `json:"pid,omitempty"`
	Version string `json:"version,omitempty"`
}

type GracefulShutdownResult struct {
	Success    bool   `json:"success"`
	Reason     string `json:"reason"`
	PID        int    `json:"pid"`
	DurationMs int64  `json:"durationMs"`
}

type ShutdownFunc func(ctx context.Context, request GracefulShutdownRequest) (GracefulShutdownResult, error)

type Session struct {
	Token       string
	URL         string
	MarkerPath  string
	MarkerNonce string
	CreatedAt   time.Time
}

type Manager struct {
	mu       sync.Mutex
	address  RuntimeAddress
	shutdown ShutdownFunc
	session  *Session
	state    string
}

func NewManager(address RuntimeAddress, shutdown ShutdownFunc) *Manager {
	return &Manager{address: address, shutdown: shutdown}
}

func (m *Manager) SetShutdown(fn ShutdownFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shutdown = fn
}

func (m *Manager) SetAddress(address RuntimeAddress) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.address = address
}

func (m *Manager) CanAutoRestart() bool {
	return m != nil
}

func (m *Manager) Begin() (Session, error) {
	if m == nil {
		return Session{}, errors.New("restart manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(handoffDir(), 0o700); err != nil {
		return Session{}, err
	}
	nonce, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	token, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	s := Session{
		Token:       token,
		URL:         gracefulShutdownURL(m.address),
		MarkerPath:  filepath.Join(handoffDir(), fmt.Sprintf("%d-%s.json", time.Now().UnixMilli(), nonce[:12])),
		MarkerNonce: nonce,
		CreatedAt:   time.Now().UTC(),
	}
	m.session = &s
	m.state = "pending"
	return s, nil
}

func (m *Manager) Cancel() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.session != nil {
		_ = os.Remove(m.session.MarkerPath)
	}
	m.session = nil
	m.state = ""
}

func (m *Manager) Handle(ctx context.Context, request GracefulShutdownRequest) (int, map[string]any) {
	if m == nil {
		return http.StatusConflict, map[string]any{"success": false, "error": "No graceful restart pending"}
	}
	m.mu.Lock()
	session := m.session
	state := m.state
	shutdown := m.shutdown
	if session == nil {
		m.mu.Unlock()
		return http.StatusConflict, map[string]any{"success": false, "error": "No graceful restart pending"}
	}
	if request.Token != session.Token {
		m.mu.Unlock()
		return http.StatusForbidden, map[string]any{"success": false, "error": "Invalid graceful restart token"}
	}
	if shutdown == nil {
		m.mu.Unlock()
		return http.StatusInternalServerError, map[string]any{"success": false, "error": "Graceful shutdown handler is not registered"}
	}
	if state == "pending" {
		m.state = "shutting_down"
		go m.runShutdown(session, shutdown, request)
	}
	m.mu.Unlock()
	return http.StatusOK, map[string]any{"success": true, "reason": "replacement_started", "pid": os.Getpid(), "durationMs": 0}
}

func (m *Manager) runShutdown(session *Session, shutdown ShutdownFunc, request GracefulShutdownRequest) {
	started := time.Now()
	time.Sleep(150 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := shutdown(ctx, request)
	if err != nil {
		result = GracefulShutdownResult{Success: false, Reason: err.Error(), PID: os.Getpid(), DurationMs: time.Since(started).Milliseconds()}
	} else if result.DurationMs == 0 {
		result.DurationMs = time.Since(started).Milliseconds()
	}
	_ = writeMarker(*session, result)
	m.mu.Lock()
	if m.session == session {
		m.state = "completed"
	}
	m.mu.Unlock()
}

func StartupHandoffFromEnv(ctx context.Context, version string) error {
	urlValue := strings.TrimSpace(os.Getenv(EnvURL))
	token := strings.TrimSpace(os.Getenv(EnvToken))
	markerPath := strings.TrimSpace(os.Getenv(EnvMarkerPath))
	markerNonce := strings.TrimSpace(os.Getenv(EnvMarkerNonce))
	if urlValue == "" && token == "" && markerPath == "" && markerNonce == "" {
		return nil
	}
	clearStartupHandoffEnv()
	if urlValue == "" || token == "" {
		return errors.New("incomplete graceful restart handoff environment")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	body, _ := json.Marshal(GracefulShutdownRequest{Token: token, PID: os.Getpid(), Version: version})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlValue, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return fmt.Errorf("graceful restart handoff rejected: status=%d error=%v", resp.StatusCode, payload["error"])
	}
	return waitForOldEndpointDown(ctx, client, urlValue)
}

func waitForOldEndpointDown(ctx context.Context, client *http.Client, shutdownURL string) error {
	healthURL := shutdownURL
	if parsed, err := url.Parse(shutdownURL); err == nil {
		parsed.Path = "/api/health"
		parsed.RawQuery = ""
		healthURL = parsed.String()
	}
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, _ := http.NewRequestWithContext(deadline, http.MethodGet, healthURL, nil)
		resp, err := client.Do(req)
		if err != nil {
			return nil
		}
		status := resp.StatusCode
		_ = resp.Body.Close()
		if status >= 500 {
			return nil
		}
		select {
		case <-deadline.Done():
			return errors.New("timed out waiting for previous process to release its HTTP endpoint")
		case <-ticker.C:
		}
	}
}

func writeMarker(session Session, result GracefulShutdownResult) error {
	if session.MarkerPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(session.MarkerPath), 0o700); err != nil {
		return err
	}
	tmp := session.MarkerPath + ".tmp"
	payload, _ := json.Marshal(map[string]any{"nonce": session.MarkerNonce, "pid": result.PID, "reason": result.Reason, "durationMs": result.DurationMs, "success": result.Success, "ts": time.Now().UTC().Format(time.RFC3339)})
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, session.MarkerPath)
}

func EnvForSession(session Session) []string {
	return []string{EnvURL + "=" + session.URL, EnvToken + "=" + session.Token, EnvMarkerPath + "=" + session.MarkerPath, EnvMarkerNonce + "=" + session.MarkerNonce}
}

func clearStartupHandoffEnv() {
	for _, key := range []string{EnvURL, EnvToken, EnvMarkerPath, EnvMarkerNonce} {
		_ = os.Unsetenv(key)
	}
}

func gracefulShutdownURL(address RuntimeAddress) string {
	protocol := strings.TrimSpace(address.Protocol)
	if protocol == "" {
		protocol = "http"
	}
	host := strings.TrimSpace(address.Host)
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s://%s:%d/api/gracefully_shutdown", protocol, host, address.Port)
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func handoffDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, ".narrafork", "restart-handoff")
	}
	return filepath.Join(home, ".narrafork", "restart-handoff")
}
