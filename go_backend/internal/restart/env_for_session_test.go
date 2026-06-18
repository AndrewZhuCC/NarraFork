package restart

import (
	"strings"
	"testing"
)

// TestEnvForSessionProducesHandoffEnvKeys verifies EnvForSession emits exactly the
// four KEY=VALUE entries that the new process reads back via StartupHandoffFromEnv
// (EnvURL/EnvToken/EnvMarkerPath/EnvMarkerNonce). This pins the self-restart
// handoff env contract; previously only the shutdown gate and HTTP handoff post
// were tested, not the env construction.
func TestEnvForSessionProducesHandoffEnvKeys(t *testing.T) {
	session := Session{
		Token:       "tok-123",
		URL:         "http://127.0.0.1:7779/api/gracefully_shutdown",
		MarkerPath:  "/home/u/.narrafork/restart-handoff/marker",
		MarkerNonce: "nonce-abc",
	}

	env := EnvForSession(session)
	if len(env) != 4 {
		t.Fatalf("expected 4 env entries, got %d (%#v)", len(env), env)
	}

	got := map[string]string{}
	for _, entry := range env {
		key, val, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("env entry missing '=': %q", entry)
		}
		got[key] = val
	}

	want := map[string]string{
		EnvURL:         session.URL,
		EnvToken:       session.Token,
		EnvMarkerPath:  session.MarkerPath,
		EnvMarkerNonce: session.MarkerNonce,
	}
	for key, wantVal := range want {
		if got[key] != wantVal {
			t.Fatalf("env[%s] = %q, want %q", key, got[key], wantVal)
		}
	}
}

// TestEnvForSessionHandlesValuesWithEquals confirms values containing '=' (e.g.
// query strings or base64 padding) are preserved intact, since the consumer
// splits only on the first '='.
func TestEnvForSessionHandlesValuesWithEquals(t *testing.T) {
	session := Session{
		Token:       "abc==",
		URL:         "http://h/api/gracefully_shutdown?a=1&b=2",
		MarkerPath:  "/tmp/marker",
		MarkerNonce: "n=ce",
	}
	env := EnvForSession(session)
	got := map[string]string{}
	for _, entry := range env {
		key, val, _ := strings.Cut(entry, "=")
		got[key] = val
	}
	if got[EnvToken] != "abc==" {
		t.Fatalf("token with '=' should be preserved, got %q", got[EnvToken])
	}
	if got[EnvURL] != session.URL {
		t.Fatalf("URL with query should be preserved, got %q", got[EnvURL])
	}
	if got[EnvMarkerNonce] != "n=ce" {
		t.Fatalf("nonce with '=' should be preserved, got %q", got[EnvMarkerNonce])
	}
}
