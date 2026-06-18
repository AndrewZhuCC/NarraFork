package misccompat

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestUploadsRoutesServeAvatarAndNarratorImagesThroughRouter exercises the
// registered upload routes end to end through the chi router: the avatar route
// (/avatars/{userId}/{imageId}, nested dir) and the narrator-image route
// (/{narratorId}/{imageId}). Existing coverage calls serveUploadFile directly
// for the narrator path only, so the avatar route + router wiring + imageId
// prefix matching were previously unverified.
func TestUploadsRoutesServeAvatarAndNarratorImagesThroughRouter(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Seed an avatar (nested under avatars/{userId}) and a narrator image.
	avatarDir := filepath.Join(uploadsRoot(), "avatars", "user-42")
	if err := os.MkdirAll(avatarDir, 0o755); err != nil {
		t.Fatalf("mkdir avatar dir: %v", err)
	}
	// Stored file name carries a suffix beyond the imageId to prove prefix matching.
	if err := os.WriteFile(filepath.Join(avatarDir, "avatar9.png"), []byte("avatar-bytes"), 0o644); err != nil {
		t.Fatalf("write avatar: %v", err)
	}
	narratorDir := filepath.Join(uploadsRoot(), "narrator-7")
	if err := os.MkdirAll(narratorDir, 0o755); err != nil {
		t.Fatalf("mkdir narrator dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(narratorDir, "img5.jpg"), []byte("jpeg-bytes"), 0o644); err != nil {
		t.Fatalf("write narrator image: %v", err)
	}

	router := chi.NewRouter()
	router.Route("/api/uploads", NewHandler(nil).RegisterUploads)

	serve := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	// Avatar route: imageId "avatar9" prefix-matches the stored avatar9.png.
	avatar := serve("/api/uploads/avatars/user-42/avatar9")
	if avatar.Code != http.StatusOK {
		t.Fatalf("avatar serve should 200, got %d body=%s", avatar.Code, avatar.Body.String())
	}
	if avatar.Body.String() != "avatar-bytes" {
		t.Fatalf("avatar body mismatch: %q", avatar.Body.String())
	}
	if ct := avatar.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("avatar content type should be image/png, got %q", ct)
	}

	// Narrator-image route.
	narrator := serve("/api/uploads/narrator-7/img5")
	if narrator.Code != http.StatusOK {
		t.Fatalf("narrator image serve should 200, got %d body=%s", narrator.Code, narrator.Body.String())
	}
	if narrator.Body.String() != "jpeg-bytes" {
		t.Fatalf("narrator image body mismatch: %q", narrator.Body.String())
	}
	if ct := narrator.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("narrator image content type should be image/jpeg, got %q", ct)
	}

	// Unknown image id under an existing dir returns 404.
	missing := serve("/api/uploads/narrator-7/nope")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown image should 404, got %d", missing.Code)
	}

	// Unknown avatar user dir returns 404.
	missingUser := serve("/api/uploads/avatars/ghost/avatar9")
	if missingUser.Code != http.StatusNotFound {
		t.Fatalf("unknown avatar user should 404, got %d", missingUser.Code)
	}
}
