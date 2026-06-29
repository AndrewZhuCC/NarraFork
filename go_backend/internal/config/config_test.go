package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseExtendedDuration(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"7d", 7 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"2w", 2 * 7 * 24 * time.Hour, false},
		{"1w", 7 * 24 * time.Hour, false},
		{"168h", 168 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"1h30m", 90 * time.Minute, false},
		{"0.5d", 12 * time.Hour, false},
		{"", 0, true},
		{"abc", 0, true},
		{"7x", 0, true},
		{"d", 0, true},
	}
	for _, tt := range tests {
		got, err := parseExtendedDuration(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseExtendedDuration(%q) = %v, want error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseExtendedDuration(%q) unexpected error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseExtendedDuration(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestTokenExpiryDurationDefaultFallback(t *testing.T) {
	// Unparseable value falls back to 7d.
	c := &Config{Auth: AuthConfig{TokenExpiry: "not-a-duration"}}
	if got := c.TokenExpiryDuration(); got != defaultTokenExpiry {
		t.Errorf("fallback = %v, want %v", got, defaultTokenExpiry)
	}

	// The human-readable default actually parses.
	c2 := &Config{Auth: AuthConfig{TokenExpiry: "7d"}}
	if got := c2.TokenExpiryDuration(); got != 7*24*time.Hour {
		t.Errorf("\"7d\" = %v, want 168h", got)
	}
}

func TestEnsureJWTSecretGeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()

	cfg := &Config{DataDir: dir}
	if err := ensureJWTSecret(dir, cfg); err != nil {
		t.Fatalf("ensureJWTSecret: %v", err)
	}
	if cfg.Auth.JWTSecret == "" {
		t.Fatal("expected a generated secret, got empty")
	}
	if len(cfg.Auth.JWTSecret) != 64 {
		t.Errorf("secret length = %d, want 64 (hex of 32 bytes)", len(cfg.Auth.JWTSecret))
	}

	// settings.json must exist with 0600 perms.
	settingsPath := filepath.Join(dir, "settings.json")
	info, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("stat settings.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("settings.json perm = %o, want 600", perm)
	}

	// The persisted secret must match the in-memory one.
	raw := map[string]any{}
	data, _ := os.ReadFile(settingsPath)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal settings.json: %v", err)
	}
	auth, _ := raw["auth"].(map[string]any)
	if auth == nil || auth["jwtSecret"] != cfg.Auth.JWTSecret {
		t.Errorf("persisted secret mismatch: %v", raw)
	}
}

func TestEnsureJWTSecretPreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	// Simulate a settings.json owned by the TS backend with many keys, some
	// nested under "auth", but no jwtSecret yet.
	existing := map[string]any{
		"theme":    "dark",
		"language": "zh-CN",
		"auth": map[string]any{
			"registrationOpen": true,
		},
		"providers": map[string]any{
			"anthropic": map[string]any{"model": "claude"},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(settingsPath, data, 0o600); err != nil {
		t.Fatalf("seed settings.json: %v", err)
	}

	cfg := &Config{DataDir: dir}
	if err := ensureJWTSecret(dir, cfg); err != nil {
		t.Fatalf("ensureJWTSecret: %v", err)
	}

	raw := map[string]any{}
	out, _ := os.ReadFile(settingsPath)
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("unmarshal merged settings.json: %v", err)
	}

	// Pre-existing top-level keys survive.
	if raw["theme"] != "dark" || raw["language"] != "zh-CN" {
		t.Errorf("top-level keys lost: %v", raw)
	}
	// Pre-existing nested provider survives.
	providers, _ := raw["providers"].(map[string]any)
	if providers == nil || providers["anthropic"] == nil {
		t.Errorf("providers key lost: %v", raw)
	}
	// auth.registrationOpen survives AND jwtSecret was added.
	auth, _ := raw["auth"].(map[string]any)
	if auth == nil {
		t.Fatalf("auth key lost: %v", raw)
	}
	if auth["registrationOpen"] != true {
		t.Errorf("auth.registrationOpen lost: %v", auth)
	}
	if s, _ := auth["jwtSecret"].(string); s == "" || s != cfg.Auth.JWTSecret {
		t.Errorf("jwtSecret not persisted correctly: %v", auth)
	}
}

func TestEnsureJWTSecretNoOpWhenPresent(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{DataDir: dir, Auth: AuthConfig{JWTSecret: "preexisting"}}
	if err := ensureJWTSecret(dir, cfg); err != nil {
		t.Fatalf("ensureJWTSecret: %v", err)
	}
	if cfg.Auth.JWTSecret != "preexisting" {
		t.Errorf("secret changed to %q, want preexisting", cfg.Auth.JWTSecret)
	}
	// Must not have written a file when nothing changed.
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); !os.IsNotExist(err) {
		t.Errorf("settings.json should not be written when secret already present")
	}
}

func TestEnsureJWTSecretAdoptsConcurrentSecret(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	// A secret already exists on disk (e.g. written by the TS backend) but the
	// in-memory cfg didn't load it. ensureJWTSecret must adopt it, not clobber.
	seed := map[string]any{"auth": map[string]any{"jwtSecret": "disk-secret"}}
	data, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(settingsPath, data, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := &Config{DataDir: dir} // empty in-memory secret
	if err := ensureJWTSecret(dir, cfg); err != nil {
		t.Fatalf("ensureJWTSecret: %v", err)
	}
	if cfg.Auth.JWTSecret != "disk-secret" {
		t.Errorf("expected to adopt disk secret, got %q", cfg.Auth.JWTSecret)
	}
}
