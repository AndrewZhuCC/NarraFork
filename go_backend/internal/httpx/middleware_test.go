package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSAllowsLoopbackAndSameHostOrigins(t *testing.T) {
	handler := CORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name        string
		requestURL  string
		origin      string
		wantAllowed bool
	}{
		{
			name:        "loopback dev origin",
			requestURL:  "http://127.0.0.1:7779/api/health",
			origin:      "http://localhost:5173",
			wantAllowed: true,
		},
		{
			name:        "same public host",
			requestURL:  "https://narrafork.example.test/api/health",
			origin:      "https://narrafork.example.test",
			wantAllowed: true,
		},
		{
			name:        "unrelated host",
			requestURL:  "https://narrafork.example.test/api/health",
			origin:      "https://evil.example.test",
			wantAllowed: false,
		},
		{
			name:        "unsupported scheme",
			requestURL:  "http://127.0.0.1:7779/api/health",
			origin:      "chrome-extension://abcdef",
			wantAllowed: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.requestURL, nil)
			req.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			got := w.Header().Get("Access-Control-Allow-Origin")
			if tc.wantAllowed && got != tc.origin {
				t.Fatalf("expected allowed origin %q, got %q", tc.origin, got)
			}
			if !tc.wantAllowed && got != "" {
				t.Fatalf("expected origin to be blocked, got %q", got)
			}
			if w.Header().Get("Vary") != "Origin" {
				t.Fatalf("expected Vary: Origin, got %q", w.Header().Get("Vary"))
			}
		})
	}
}

func TestCORSPreflightDoesNotEchoUntrustedOrigin(t *testing.T) {
	handler := CORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodOptions, "https://narrafork.example.test/api/projects", nil)
	req.Header.Set("Origin", "https://evil.example.test")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected preflight 204, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected untrusted origin to be blocked, got %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type" {
		t.Fatalf("unexpected allow headers: %q", got)
	}
}
