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

	"narrafork-go/internal/config"
	"narrafork-go/internal/restart"
)

func TestUpdateDownloadWithoutTrustedSourceReturnsTerminalErrorSSE(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	h := NewHandler(nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/update/download", strings.NewReader(`{"releaseInfo":{"version":"1.2.3","files":[{"url":"https://client.invalid/artifact"}]}}`))
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
	if terminal.Payload["code"] != "UPDATE_DOWNLOAD_FAILED" {
		t.Fatalf("expected structured failure code, got %#v", terminal.Payload)
	}
	reason, _ := terminal.Payload["reason"].(string)
	if reason == "" || terminal.Payload["error"] != reason || terminal.Payload["message"] != reason {
		t.Fatalf("expected reason/error/message diagnostics, got %#v", terminal.Payload)
	}
	if !strings.Contains(reason, "trusted update metadata") {
		t.Fatalf("expected trusted metadata error, got %#v", terminal.Payload)
	}
}

func TestUpdateCheckUsesConfiguredTrustedSourceShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	artifactBytes := []byte("fake update artifact")
	server := newTrustedUpdateServer(t, trustedUpdateServerOptions{
		Version: "1.2.3",
		Channel: "beta",
		Product: "narrafork-go-test",
		Bytes:   artifactBytes,
		SHA512:  sha512Base64(artifactBytes),
	})
	defer server.Close()
	h := newUpdateTestHandler(t, server.URL, "narrafork-go-test", "beta")

	req := httptest.NewRequest(http.MethodGet, "/api/update/check?downloadUrl=https://client.invalid/evil", nil)
	w := httptest.NewRecorder()
	if err := h.updateCheck(w, req); err != nil {
		t.Fatalf("updateCheck returned error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode check: %v", err)
	}
	if body["updateAvailable"] != true || body["latestVersion"] != "1.2.3" || body["currentVersion"] == "" {
		t.Fatalf("unexpected update check body: %#v", body)
	}
	if body["selfUpdateAvailable"] != false || body["canAutoRestart"] != false || body["manualOnly"] != true {
		t.Fatalf("expected update check to expose manual mode without restart handoff, got %#v", body)
	}
	instructions, _ := body["instructions"].(map[string]any)
	if instructions["manual"] != true {
		t.Fatalf("expected manual check instructions, got %#v", instructions)
	}
	source, _ := body["updateSource"].(map[string]any)
	if source["product"] != "narrafork-go-test" || source["channel"] != "beta" {
		t.Fatalf("expected configured update source metadata, got %#v", source)
	}
	releaseInfo, _ := body["releaseInfo"].(map[string]any)
	if releaseInfo["version"] != "1.2.3" || releaseInfo["path"] == "" || releaseInfo["sha512"] == "" {
		t.Fatalf("unexpected releaseInfo: %#v", releaseInfo)
	}
	files, _ := releaseInfo["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("expected one release file, got %#v", releaseInfo["files"])
	}
	file, _ := files[0].(map[string]any)
	if !strings.HasPrefix(file["url"].(string), server.URL+"/api/v2/products/narrafork-go-test/") {
		t.Fatalf("release file URL should be derived from trusted server, got %#v", file)
	}
}

func TestUpdateCheckExposesAutoCapabilityWhenRestartHandoffAvailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	currentExe := filepath.Join(home, "narrafork-go")
	if err := os.WriteFile(currentExe, []byte("current binary"), 0o700); err != nil {
		t.Fatalf("write current executable: %v", err)
	}
	artifactBytes := []byte("fake update artifact")
	server := newTrustedUpdateServer(t, trustedUpdateServerOptions{
		Version: "1.2.5",
		Channel: "stable",
		Product: "narrafork",
		Bytes:   artifactBytes,
		SHA512:  sha512Base64(artifactBytes),
	})
	defer server.Close()

	origExec := updateExecutablePath
	updateExecutablePath = func() (string, error) { return currentExe, nil }
	t.Cleanup(func() { updateExecutablePath = origExec })

	h := newUpdateTestHandler(t, server.URL, "narrafork", "stable")
	h.Restart = restart.NewManager(restart.RuntimeAddress{Protocol: "http", Host: "127.0.0.1", Port: 7778}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/update/check", nil)
	w := httptest.NewRecorder()
	if err := h.updateCheck(w, req); err != nil {
		t.Fatalf("updateCheck returned error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode check: %v", err)
	}
	if body["updateAvailable"] != true || body["selfUpdateAvailable"] != true || body["canAutoRestart"] != true || body["manualOnly"] != false {
		t.Fatalf("expected update check to expose auto-capable mode, got %#v", body)
	}
	instructions, _ := body["instructions"].(map[string]any)
	if instructions["manual"] != false || instructions["action"] != "apply" {
		t.Fatalf("expected automatic check instructions, got %#v", instructions)
	}
}

func TestUpdateDownloadRefetchesTrustedMetadataAndIgnoresClientURLs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	artifactBytes := []byte("fake update artifact")
	trusted := newTrustedUpdateServer(t, trustedUpdateServerOptions{
		Version: "1.2.3",
		Channel: "stable",
		Product: "narrafork",
		Bytes:   artifactBytes,
		SHA512:  sha512Base64(artifactBytes),
	})
	defer trusted.Close()
	clientURLHit := false
	clientURLServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientURLHit = true
		_, _ = w.Write([]byte("client supplied artifact must not be used"))
	}))
	defer clientURLServer.Close()

	h := newUpdateTestHandler(t, trusted.URL, "narrafork", "stable")
	body := `{"requestedVersion":"1.2.3","url":"` + clientURLServer.URL + `/top-level","downloadUrl":"` + clientURLServer.URL + `/download","artifactUrl":"` + clientURLServer.URL + `/artifact","releaseInfo":{"version":"client-version","files":[{"url":"` + clientURLServer.URL + `/file","size":999}]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/update/download", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.updateDownload(w, req); err != nil {
		t.Fatalf("updateDownload returned error: %v", err)
	}
	if clientURLHit {
		t.Fatal("download used a client-supplied artifact URL")
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
	terminal := events[len(events)-1]
	if terminal.Name != "complete" || terminal.Payload["success"] != true {
		t.Fatalf("expected successful complete terminal event, got %#v", terminal)
	}
	if terminal.Payload["phase"] != "complete" || terminal.Payload["version"] != "1.2.3" {
		t.Fatalf("expected complete payload with trusted version, got %#v", terminal.Payload)
	}
	if terminal.Payload["ready"] != true || terminal.Payload["selfUpdateAvailable"] != false || terminal.Payload["canAutoRestart"] != false || terminal.Payload["manualOnly"] != true {
		t.Fatalf("expected manual-only ready download contract, got %#v", terminal.Payload)
	}
	instructions, _ := terminal.Payload["instructions"].(map[string]any)
	if instructions["manual"] != true {
		t.Fatalf("expected manual download instructions, got %#v", instructions)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/update/status?version=1.2.3", nil)
	statusW := httptest.NewRecorder()
	if err := h.updateStatus(statusW, statusReq); err != nil {
		t.Fatalf("updateStatus returned error: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal(statusW.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status["ready"] != true || status["selfUpdateAvailable"] != false || status["canAutoRestart"] != false || status["manualOnly"] != true {
		t.Fatalf("expected manual-only ready status contract, got %#v", status)
	}
	statusInstructions, _ := status["instructions"].(map[string]any)
	if statusInstructions["manual"] != true {
		t.Fatalf("expected manual status instructions, got %#v", statusInstructions)
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

func TestUpdateDownloadAndStatusExposeAutoApplyWhenReady(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	currentExe := filepath.Join(home, "narrafork-go")
	if err := os.WriteFile(currentExe, []byte("current binary"), 0o700); err != nil {
		t.Fatalf("write current executable: %v", err)
	}
	artifactBytes := []byte("auto apply artifact")
	trusted := newTrustedUpdateServer(t, trustedUpdateServerOptions{
		Version: "1.2.4",
		Channel: "stable",
		Product: "narrafork",
		Bytes:   artifactBytes,
		SHA512:  sha512Base64(artifactBytes),
	})
	defer trusted.Close()

	origExec := updateExecutablePath
	updateExecutablePath = func() (string, error) { return currentExe, nil }
	t.Cleanup(func() { updateExecutablePath = origExec })

	h := newUpdateTestHandler(t, trusted.URL, "narrafork", "stable")
	h.Restart = restart.NewManager(restart.RuntimeAddress{Protocol: "http", Host: "127.0.0.1", Port: 7778}, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/update/download", strings.NewReader(`{"requestedVersion":"1.2.4"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if err := h.updateDownload(w, req); err != nil {
		t.Fatalf("updateDownload returned error: %v", err)
	}
	events := parseUpdateSSEEvents(t, w.Body.String())
	terminal := events[len(events)-1]
	if terminal.Name != "complete" || terminal.Payload["success"] != true {
		t.Fatalf("expected complete download, got %#v", terminal)
	}
	assertAutoReadyUpdateContract(t, terminal.Payload)
	if terminal.Payload["placed"] != false {
		t.Fatalf("download should cache but not pre-place the replacement binary, got %#v", terminal.Payload)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/update/status?version=1.2.4", nil)
	statusW := httptest.NewRecorder()
	if err := h.updateStatus(statusW, statusReq); err != nil {
		t.Fatalf("updateStatus returned error: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal(statusW.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	assertAutoReadyUpdateContract(t, status)
	if status["placed"] != false {
		t.Fatalf("status should keep placed=false until apply prepares the replacement binary, got %#v", status)
	}
}

func TestUpdateDownloadRejectsRequestedVersionMismatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	artifactBytes := []byte("new trusted artifact")
	trusted := newTrustedUpdateServer(t, trustedUpdateServerOptions{
		Version: "2.0.0",
		Channel: "stable",
		Product: "narrafork",
		Bytes:   artifactBytes,
		SHA512:  sha512Base64(artifactBytes),
	})
	defer trusted.Close()
	h := newUpdateTestHandler(t, trusted.URL, "narrafork", "stable")

	req := httptest.NewRequest(http.MethodPost, "/api/update/download", strings.NewReader(`{"version":"1.0.0"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	if err := h.updateDownload(w, req); err != nil {
		t.Fatalf("updateDownload returned error: %v", err)
	}
	events := parseUpdateSSEEvents(t, w.Body.String())
	terminal := events[len(events)-1]
	if terminal.Name != "error" || terminal.Payload["success"] != false {
		t.Fatalf("expected terminal version mismatch error, got %#v", terminal)
	}
	if terminal.Payload["requestedVersion"] != "1.0.0" || terminal.Payload["latestVersion"] != "2.0.0" {
		t.Fatalf("expected mismatch details, got %#v", terminal.Payload)
	}
}

func TestUpdateDownloadRejectsSHA512Mismatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	trusted := newTrustedUpdateServer(t, trustedUpdateServerOptions{
		Version: "bad-sha",
		Channel: "stable",
		Product: "narrafork",
		Bytes:   []byte("tampered artifact"),
		SHA512:  sha512Hex([]byte("expected artifact")),
	})
	defer trusted.Close()

	h := newUpdateTestHandler(t, trusted.URL, "narrafork", "stable")
	req := httptest.NewRequest(http.MethodPost, "/api/update/download", strings.NewReader(`{"requestedVersion":"bad-sha"}`))
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

func TestUpdateDownloadRejectsMissingSHA512(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	trusted := newTrustedUpdateServer(t, trustedUpdateServerOptions{
		Version: "missing-sha",
		Channel: "stable",
		Product: "narrafork",
		Bytes:   []byte("artifact without hash"),
		SHA512:  "",
	})
	defer trusted.Close()

	h := newUpdateTestHandler(t, trusted.URL, "narrafork", "stable")
	req := httptest.NewRequest(http.MethodPost, "/api/update/download", strings.NewReader(`{"requestedVersion":"missing-sha"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	if err := h.updateDownload(w, req); err != nil {
		t.Fatalf("updateDownload returned error: %v", err)
	}
	events := parseUpdateSSEEvents(t, w.Body.String())
	terminal := events[len(events)-1]
	if terminal.Name != "error" || terminal.Payload["success"] != false {
		t.Fatalf("expected terminal missing-hash error, got %#v", terminal)
	}
	if !strings.Contains(terminal.Payload["error"].(string), "SHA512") {
		t.Fatalf("expected SHA512-required error, got %#v", terminal.Payload)
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

func assertAutoReadyUpdateContract(t *testing.T, payload map[string]any) {
	t.Helper()
	if payload["ready"] != true || payload["selfUpdateAvailable"] != true || payload["canAutoRestart"] != true || payload["manualOnly"] != false {
		t.Fatalf("expected auto-ready update contract, got %#v", payload)
	}
	instructions, _ := payload["instructions"].(map[string]any)
	if instructions["manual"] != false || instructions["action"] != "apply" {
		t.Fatalf("expected automatic apply instructions, got %#v", instructions)
	}
	if payload["updatePath"] == "" || payload["artifactPath"] == "" {
		t.Fatalf("expected ready payload to include update/artifact path, got %#v", payload)
	}
}

type trustedUpdateServerOptions struct {
	Version string
	Channel string
	Product string
	Bytes   []byte
	SHA512  string
}

func newTrustedUpdateServer(t *testing.T, opts trustedUpdateServerOptions) *httptest.Server {
	t.Helper()
	filename := "narrafork-" + opts.Version + "-" + updatePlatform()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/products/"+opts.Product+"/releases/latest":
			if r.URL.Query().Get("channel") != opts.Channel {
				t.Fatalf("unexpected channel query: %q", r.URL.RawQuery)
			}
			if r.URL.Query().Get("platform") != updatePlatform() {
				t.Fatalf("unexpected platform query: %q", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"updateAvailable": true,
				"currentVersion":  r.URL.Query().Get("version"),
				"version":         opts.Version,
				"releaseDate":     "2026-05-18T00:00:00Z",
				"releaseNotes":    map[string]any{"en": "Trusted release", "zh-CN": "可信发布"},
				"platform":        updatePlatform(),
				"file": map[string]any{
					"filename": filename,
					"size":     len(opts.Bytes),
					"sha512":   opts.SHA512,
				},
				"zstdPatch": nil,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/products/"+opts.Product+"/releases/"+opts.Version+"/download/"+filename:
			w.Header().Set("Content-Length", strconv.Itoa(len(opts.Bytes)))
			_, _ = w.Write(opts.Bytes)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newUpdateTestHandler(t *testing.T, serverURL, product, channel string) *Handler {
	t.Helper()
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if _, err := settings.Patch(map[string]any{"update": map[string]any{"serverUrl": serverURL, "product": product, "channel": channel}}); err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	return NewHandler(nil, settings)
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
