package systemroutes

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestUpdateDownloadWithoutURLReturnsTerminalErrorSSE(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	h := NewHandler(nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/update/download", strings.NewReader(`{"releaseInfo":{"version":"1.2.3"}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.updateDownload(w, req); err != nil {
		t.Fatalf("updateDownload returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("expected SSE response, got content-type %q", w.Header().Get("Content-Type"))
	}

	events := parseUpdateSSEEvents(t, w.Body.String())
	if len(events) != 2 {
		t.Fatalf("expected checking + terminal error events, got %#v", events)
	}
	if events[0].Name != "progress" || events[0].Payload["phase"] != "checking" {
		t.Fatalf("expected checking progress event, got %#v", events[0])
	}
	terminal := events[len(events)-1]
	if terminal.Name != "error" {
		t.Fatalf("expected terminal error event, got %#v", terminal)
	}
	if terminal.Payload["success"] != false || terminal.Payload["phase"] != "error" {
		t.Fatalf("expected parseable failed result payload, got %#v", terminal.Payload)
	}
	if terminal.Payload["version"] != "1.2.3" {
		t.Fatalf("expected releaseInfo version to be preserved, got %#v", terminal.Payload)
	}
	if _, ok := terminal.Payload["error"].(string); !ok {
		t.Fatalf("expected error string, got %#v", terminal.Payload)
	}
}

func TestUpdateDownloadStreamsProgressAndCompleteSSE(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	artifactBytes := []byte("fake update artifact")
	artifactServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "20")
		_, _ = w.Write(artifactBytes)
	}))
	defer artifactServer.Close()

	h := NewHandler(nil, nil)
	body := `{"releaseInfo":{"version":"1.2.3","files":[{"url":"` + artifactServer.URL + `/artifact","size":20}]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/update/download", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.updateDownload(w, req); err != nil {
		t.Fatalf("updateDownload returned error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("expected SSE response, got content-type %q", w.Header().Get("Content-Type"))
	}

	events := parseUpdateSSEEvents(t, w.Body.String())
	if len(events) < 3 {
		t.Fatalf("expected checking, downloading, and complete events, got %#v", events)
	}
	if events[0].Name != "progress" || events[0].Payload["phase"] != "checking" {
		t.Fatalf("expected initial checking progress, got %#v", events[0])
	}
	if events[1].Name != "progress" || events[1].Payload["phase"] != "downloading" {
		t.Fatalf("expected downloading progress, got %#v", events[1])
	}
	terminal := events[len(events)-1]
	if terminal.Name != "complete" || terminal.Payload["success"] != true {
		t.Fatalf("expected successful complete terminal event, got %#v", terminal)
	}
	if terminal.Payload["phase"] != "complete" || terminal.Payload["version"] != "1.2.3" {
		t.Fatalf("expected complete payload with version, got %#v", terminal.Payload)
	}

	artifactPath := filepath.Join(home, ".narrafork", "updates", "1.2.3", "artifact")
	written, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read downloaded artifact: %v", err)
	}
	if string(written) != string(artifactBytes) {
		t.Fatalf("artifact content mismatch: %q", string(written))
	}
}

func TestUpdateDownloadSupportsMultipleFilesAndSHA512(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	files := map[string][]byte{
		"/one.bin": []byte("first update artifact"),
		"/two.bin": []byte("second update artifact"),
	}
	artifactServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer artifactServer.Close()

	h := NewHandler(nil, nil)
	body := `{"releaseInfo":{"version":"2.0.0","files":[` +
		`{"url":"` + artifactServer.URL + `/one.bin","size":21,"sha512":"` + sha512Base64(files["/one.bin"]) + `"},` +
		`{"url":"` + artifactServer.URL + `/two.bin","size":22,"sha512":"` + sha512Hex(files["/two.bin"]) + `"}` +
		`]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/update/download", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.updateDownload(w, req); err != nil {
		t.Fatalf("updateDownload returned error: %v", err)
	}
	events := parseUpdateSSEEvents(t, w.Body.String())
	terminal := events[len(events)-1]
	if terminal.Name != "complete" || terminal.Payload["success"] != true {
		t.Fatalf("expected complete success, got %#v", terminal)
	}
	if terminal.Payload["bytesDownloaded"] != float64(43) || terminal.Payload["totalBytes"] != float64(43) || terminal.Payload["percent"] != float64(100) {
		t.Fatalf("expected aggregate progress totals, got %#v", terminal.Payload)
	}
	if terminal.Payload["selfUpdateAvailable"] != false {
		t.Fatalf("expected selfUpdateAvailable=false, got %#v", terminal.Payload)
	}
	instructions, _ := terminal.Payload["instructions"].(map[string]any)
	if instructions["manual"] != true || !strings.Contains(instructions["message"].(string), "not supported") {
		t.Fatalf("expected manual unsupported instructions, got %#v", instructions)
	}

	for name, expected := range map[string][]byte{"one.bin": files["/one.bin"], "two.bin": files["/two.bin"]} {
		written, err := os.ReadFile(filepath.Join(home, ".narrafork", "updates", "2.0.0", name))
		if err != nil {
			t.Fatalf("read downloaded %s: %v", name, err)
		}
		if string(written) != string(expected) {
			t.Fatalf("content mismatch for %s: %q", name, string(written))
		}
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/update/status?version=2.0.0", nil)
	statusW := httptest.NewRecorder()
	if err := h.updateStatus(statusW, statusReq); err != nil {
		t.Fatalf("updateStatus returned error: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal(statusW.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status["ready"] != true || status["canAutoRestart"] != false || status["selfUpdateAvailable"] != false {
		t.Fatalf("unexpected status contract: %#v", status)
	}
}

func TestUpdateDownloadRejectsSHA512Mismatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	artifactServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tampered artifact"))
	}))
	defer artifactServer.Close()

	h := NewHandler(nil, nil)
	body := `{"releaseInfo":{"version":"bad-sha","files":[{"url":"` + artifactServer.URL + `/artifact","sha512":"` + sha512Hex([]byte("expected artifact")) + `"}]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/update/download", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.updateDownload(w, req); err != nil {
		t.Fatalf("updateDownload returned error: %v", err)
	}
	events := parseUpdateSSEEvents(t, w.Body.String())
	terminal := events[len(events)-1]
	if terminal.Name != "error" || terminal.Payload["success"] != false {
		t.Fatalf("expected terminal SHA error, got %#v", terminal)
	}
	if !strings.Contains(terminal.Payload["error"].(string), "SHA512 mismatch") {
		t.Fatalf("expected SHA512 mismatch error, got %#v", terminal.Payload)
	}
	if _, err := os.Stat(filepath.Join(home, ".narrafork", "updates", "bad-sha", "artifact")); !os.IsNotExist(err) {
		t.Fatalf("expected mismatched artifact to be removed, stat err=%v", err)
	}
}

func TestUpdateVersionExposesManualOnlyContract(t *testing.T) {
	t.Setenv("APP_VERSION", "9.9.9-test")
	t.Setenv("GIT_COMMIT", "deadbeef")
	h := NewHandler(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/update/version", nil)
	w := httptest.NewRecorder()

	if err := h.updateVersion(w, req); err != nil {
		t.Fatalf("updateVersion returned error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode version: %v", err)
	}
	if body["version"] != "9.9.9-test" || body["commit"] != "deadbeef" || body["manualOnly"] != true || body["selfUpdateAvailable"] != false {
		t.Fatalf("unexpected version manual-only contract: %#v", body)
	}
}

func TestUpdateDirectoryCleanupAndApplyExposeUnsupportedContract(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	h := NewHandler(nil, nil)

	dirReq := httptest.NewRequest(http.MethodGet, "/api/update/directory", nil)
	dirW := httptest.NewRecorder()
	if err := h.updateDirectory(dirW, dirReq); err != nil {
		t.Fatalf("updateDirectory returned error: %v", err)
	}
	var directory map[string]any
	if err := json.Unmarshal(dirW.Body.Bytes(), &directory); err != nil {
		t.Fatalf("decode directory: %v", err)
	}
	if directory["directory"] == "" || directory["selfUpdateAvailable"] != false {
		t.Fatalf("unexpected directory contract: %#v", directory)
	}

	versionDir := filepath.Join(home, ".narrafork", "updates", "cleanup-me")
	if err := os.MkdirAll(versionDir, 0o700); err != nil {
		t.Fatalf("mkdir update dir: %v", err)
	}
	cleanupReq := httptest.NewRequest(http.MethodPost, "/api/update/cleanup", strings.NewReader(`{"version":"cleanup-me"}`))
	cleanupReq.Header.Set("Content-Type", "application/json")
	cleanupW := httptest.NewRecorder()
	if err := h.updateCleanup(cleanupW, cleanupReq); err != nil {
		t.Fatalf("updateCleanup returned error: %v", err)
	}
	var cleanup map[string]any
	if err := json.Unmarshal(cleanupW.Body.Bytes(), &cleanup); err != nil {
		t.Fatalf("decode cleanup: %v", err)
	}
	if cleanup["success"] != true || cleanup["removed"] != true {
		t.Fatalf("unexpected cleanup contract: %#v", cleanup)
	}
	if _, err := os.Stat(versionDir); !os.IsNotExist(err) {
		t.Fatalf("expected version update dir removed, stat err=%v", err)
	}

	applyReq := httptest.NewRequest(http.MethodPost, "/api/update/apply", strings.NewReader(`{"version":"2.0.0"}`))
	applyW := httptest.NewRecorder()
	if err := h.updateApply(applyW, applyReq); err != nil {
		t.Fatalf("updateApply returned error: %v", err)
	}
	var apply map[string]any
	if err := json.Unmarshal(applyW.Body.Bytes(), &apply); err != nil {
		t.Fatalf("decode apply: %v", err)
	}
	if apply["success"] != false || apply["selfUpdateAvailable"] != false {
		t.Fatalf("unexpected apply contract: %#v", apply)
	}
	applyInstructions, _ := apply["instructions"].(map[string]any)
	if applyInstructions["manual"] != true {
		t.Fatalf("expected manual apply instructions, got %#v", applyInstructions)
	}
}

func sha512Base64(payload []byte) string {
	sum := sha512.Sum512(payload)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func sha512Hex(payload []byte) string {
	sum := sha512.Sum512(payload)
	return hex.EncodeToString(sum[:])
}

type updateSSEEvent struct {
	Name    string
	Payload map[string]any
}

func parseUpdateSSEEvents(t *testing.T, stream string) []updateSSEEvent {
	t.Helper()
	events := []updateSSEEvent{}
	for _, chunk := range strings.Split(stream, "\n\n") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		event := updateSSEEvent{Payload: map[string]any{}}
		for _, line := range strings.Split(chunk, "\n") {
			if strings.HasPrefix(line, "event:") {
				event.Name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			}
			if strings.HasPrefix(line, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if err := json.Unmarshal([]byte(data), &event.Payload); err != nil {
					t.Fatalf("decode SSE data %q: %v", data, err)
				}
			}
		}
		events = append(events, event)
	}
	return events
}
