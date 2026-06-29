package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Config holds all NarraFork configuration.
type Config struct {
	DataDir string `json:"-"` // ~/.narrafork (not stored in JSON)

	Server   ServerConfig   `json:"server"`
	Database DatabaseConfig `json:"database"`
	Auth     AuthConfig     `json:"auth"`
	Provider ProviderConfig `json:"provider"`
}

type DatabaseConfig struct {
	// DSN determines the database backend:
	//   "" → SQLite at ~/.narrafork/narrafork.db (default)
	//   "sqlite:/path/to/file.db" → explicit SQLite path
	//   "postgres://user:pass@host:5432/narrafork" → PostgreSQL
	DSN string `json:"dsn"`
}

type ServerConfig struct {
	Port int    `json:"port"`
	Host string `json:"host"`
}

type AuthConfig struct {
	JWTSecret   string `json:"jwtSecret"`
	TokenExpiry string `json:"tokenExpiry"` // e.g. "7d"
}

type ProviderConfig struct {
	// Provider configs will be expanded in Phase 5
	DefaultProvider string `json:"defaultProvider"`
}

// defaultTokenExpiry is the fallback used when TokenExpiry is missing or
// unparseable. It matches the human-readable "7d" default.
const defaultTokenExpiry = 7 * 24 * time.Hour

// DefaultDataDir returns the platform-appropriate data directory.
func DefaultDataDir() string {
	if dir := os.Getenv("NARRAFORK_DATA_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Falling back to a bare relative ".narrafork" would silently scatter
		// data under whatever the current working directory happens to be. Use
		// an absolute, writable location instead and make the degradation loud.
		fallback := filepath.Join(os.TempDir(), "narrafork")
		slog.Warn("could not determine home directory; falling back to temp dir",
			"error", err, "dataDir", fallback)
		return fallback
	}
	return filepath.Join(home, ".narrafork")
}

// Load reads configuration from ~/.narrafork/settings.json,
// falling back to defaults for missing values.
func Load() (*Config, error) {
	dataDir := DefaultDataDir()

	cfg := &Config{
		DataDir: dataDir,
		Server: ServerConfig{
			Port: 7779,
			Host: "127.0.0.1",
		},
		Auth: AuthConfig{
			TokenExpiry: "7d",
		},
	}

	settingsPath := filepath.Join(dataDir, "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		// No settings file yet — ensure directory exists, then fall through so
		// env overrides and first-run secret generation still happen.
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return nil, err
		}
	} else {
		// Overlay values present in the JSON file onto the defaults.
		// Note: keys present in JSON override defaults; absent keys keep defaults.
		// (An explicit zero value in JSON, e.g. "port": 0, WILL override.)
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}
	cfg.DataDir = dataDir

	// Environment variable overrides (highest precedence). A secret supplied
	// via env is treated as external/ephemeral and is intentionally NOT
	// persisted to settings.json.
	applyEnvOverrides(cfg)

	// First-run: generate and persist a JWT secret if none is configured.
	if err := ensureJWTSecret(dataDir, cfg); err != nil {
		return nil, fmt.Errorf("ensure jwt secret: %w", err)
	}

	return cfg, nil
}

// applyEnvOverrides applies environment variables over file/default config.
func applyEnvOverrides(cfg *Config) {
	if dsn := os.Getenv("NARRAFORK_DATABASE_DSN"); dsn != "" {
		cfg.Database.DSN = dsn
	}
	if secret := os.Getenv("NARRAFORK_JWT_SECRET"); secret != "" {
		cfg.Auth.JWTSecret = secret
	}
}

// ensureJWTSecret generates and persists a JWT secret on first run.
//
// If a secret is already configured (from settings.json or the environment),
// it does nothing. Otherwise it generates a 32-byte hex secret, sets it on cfg,
// and writes it back into settings.json. The write is a careful merge: the
// existing file is read into a generic map so every key the TypeScript backend
// manages is preserved, and only the missing auth.jwtSecret is added. The file
// is written with 0o600 permissions because it now contains a secret.
func ensureJWTSecret(dataDir string, cfg *Config) error {
	if cfg.Auth.JWTSecret != "" {
		return nil
	}

	secret, err := generateHexSecret(32)
	if err != nil {
		return err
	}
	cfg.Auth.JWTSecret = secret

	settingsPath := filepath.Join(dataDir, "settings.json")

	// Read existing settings (if any) into a generic map to preserve all keys.
	raw := map[string]any{}
	data, err := os.ReadFile(settingsPath)
	switch {
	case err == nil:
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &raw); err != nil {
				return fmt.Errorf("parse settings.json for secret merge: %w", err)
			}
		}
	case os.IsNotExist(err):
		// Start from an empty document.
	default:
		return fmt.Errorf("read settings.json: %w", err)
	}

	// Navigate or create the nested "auth" object.
	authObj, _ := raw["auth"].(map[string]any)
	if authObj == nil {
		authObj = map[string]any{}
		raw["auth"] = authObj
	}

	// Never clobber an existing secret. If one appeared (e.g. written
	// concurrently by the TS backend), adopt it instead of overwriting.
	if existing, ok := authObj["jwtSecret"].(string); ok && existing != "" {
		cfg.Auth.JWTSecret = existing
		return nil
	}
	authObj["jwtSecret"] = secret

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings.json: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(settingsPath, out, 0o600); err != nil {
		return fmt.Errorf("write settings.json: %w", err)
	}
	// Tighten perms even if the file pre-existed with looser permissions, since
	// it now holds a secret. WriteFile only applies the mode on creation.
	if err := os.Chmod(settingsPath, 0o600); err != nil {
		return fmt.Errorf("chmod settings.json: %w", err)
	}
	return nil
}

// generateHexSecret returns a hex-encoded random secret of byteLen bytes.
// It uses crypto/rand directly to avoid an import cycle with internal/auth.
func generateHexSecret(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// DatabaseDSN returns the effective database DSN.
// Falls back to default SQLite path if not configured.
func (c *Config) DatabaseDSN() string {
	if c.Database.DSN != "" {
		return c.Database.DSN
	}
	return filepath.Join(c.DataDir, "narrafork.db")
}

// SettingsPath returns the path to the settings.json file.
func (c *Config) SettingsPath() string {
	return filepath.Join(c.DataDir, "settings.json")
}

// TokenExpiryDuration parses the configured TokenExpiry string into a
// time.Duration. It accepts everything time.ParseDuration does plus the
// human-friendly "d" (days) and "w" (weeks) suffixes that Go does not support
// natively. On any parse error it logs a warning and falls back to 7 days.
func (c *Config) TokenExpiryDuration() time.Duration {
	d, err := parseExtendedDuration(c.Auth.TokenExpiry)
	if err != nil {
		slog.Warn("invalid tokenExpiry; falling back to default",
			"value", c.Auth.TokenExpiry, "fallback", defaultTokenExpiry, "error", err)
		return defaultTokenExpiry
	}
	return d
}

// parseExtendedDuration parses a duration string, additionally supporting a
// trailing "d" (days) or "w" (weeks) unit by converting to hours. Standard Go
// duration strings (e.g. "168h", "30m", "1h30m") are delegated to
// time.ParseDuration.
func parseExtendedDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}

	switch s[len(s)-1] {
	case 'd', 'w':
		unit := 24 * time.Hour
		if s[len(s)-1] == 'w' {
			unit = 7 * 24 * time.Hour
		}
		n, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		return time.Duration(n * float64(unit)), nil
	default:
		return time.ParseDuration(s)
	}
}

// IsWSL returns true if running under Windows Subsystem for Linux.
func IsWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	// WSL kernels contain "microsoft" / "Microsoft" in the version string.
	return bytes.Contains(bytes.ToLower(data), []byte("microsoft"))
}
