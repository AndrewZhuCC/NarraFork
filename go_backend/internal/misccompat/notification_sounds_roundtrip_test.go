package misccompat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestNotificationSoundServeAndDeleteRoundTrip exercises the serve and delete
// HTTP handlers end to end: after a successful upload the sound is served with
// the correct Content-Type/Cache-Control and body, then delete removes it and a
// subsequent serve returns 404. Existing tests cover upload validation and the
// findSoundFile helper, but the serve/delete routes' success path was unverified.
func TestNotificationSoundServeAndDeleteRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	router := chi.NewRouter()
	router.Route("/api/notification-sounds", NewHandler(nil).RegisterNotificationSounds)

	// Upload a valid sound and capture its id/filename.
	uploadResp := performNotificationSoundUpload(t, router, "tone.mp3", "audio/mpeg", []byte("ID3-bytes"))
	if uploadResp.Code != http.StatusOK {
		t.Fatalf("upload should succeed, got %d: %s", uploadResp.Code, uploadResp.Body.String())
	}
	var uploaded map[string]any
	if err := json.Unmarshal(uploadResp.Body.Bytes(), &uploaded); err != nil {
		t.Fatalf("decode upload body: %v", err)
	}
	soundID, _ := uploaded["id"].(string)
	if soundID == "" {
		t.Fatalf("upload did not return an id: %#v", uploaded)
	}

	// Serve returns the stored bytes with audio Content-Type and a cache header.
	serveReq := httptest.NewRequest(http.MethodGet, "/api/notification-sounds/"+soundID, nil)
	serveW := httptest.NewRecorder()
	router.ServeHTTP(serveW, serveReq)
	if serveW.Code != http.StatusOK {
		t.Fatalf("serve should return 200, got %d: %s", serveW.Code, serveW.Body.String())
	}
	if serveW.Body.String() != "ID3-bytes" {
		t.Fatalf("serve returned unexpected body %q", serveW.Body.String())
	}
	if ct := serveW.Header().Get("Content-Type"); ct == "" {
		t.Fatalf("serve should set a Content-Type for the .mp3 extension")
	}
	if cc := serveW.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
		t.Fatalf("serve should set the public cache header, got %q", cc)
	}

	// Serving an unknown id returns 404.
	missingReq := httptest.NewRequest(http.MethodGet, "/api/notification-sounds/deadbeef", nil)
	missingW := httptest.NewRecorder()
	router.ServeHTTP(missingW, missingReq)
	if missingW.Code != http.StatusNotFound {
		t.Fatalf("serving unknown sound should 404, got %d", missingW.Code)
	}

	// Delete removes the sound.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/notification-sounds/"+soundID, nil)
	delW := httptest.NewRecorder()
	router.ServeHTTP(delW, delReq)
	if delW.Code != http.StatusOK {
		t.Fatalf("delete should return 200, got %d: %s", delW.Code, delW.Body.String())
	}
	var delBody map[string]any
	if err := json.Unmarshal(delW.Body.Bytes(), &delBody); err != nil {
		t.Fatalf("decode delete body: %v", err)
	}
	if delBody["ok"] != true {
		t.Fatalf("delete should report ok:true, got %#v", delBody)
	}

	// After delete, serve returns 404 and a second delete also 404s.
	afterReq := httptest.NewRequest(http.MethodGet, "/api/notification-sounds/"+soundID, nil)
	afterW := httptest.NewRecorder()
	router.ServeHTTP(afterW, afterReq)
	if afterW.Code != http.StatusNotFound {
		t.Fatalf("serving a deleted sound should 404, got %d", afterW.Code)
	}
	delAgainReq := httptest.NewRequest(http.MethodDelete, "/api/notification-sounds/"+soundID, nil)
	delAgainW := httptest.NewRecorder()
	router.ServeHTTP(delAgainW, delAgainReq)
	if delAgainW.Code != http.StatusNotFound {
		t.Fatalf("deleting an already-deleted sound should 404, got %d", delAgainW.Code)
	}
}
