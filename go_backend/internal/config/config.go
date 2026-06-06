package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"narrafork-go/internal/id"
)

const DefaultAgentModel = "kiro:claude-sonnet-4.5"

const defaultSummaryModel = "kiro:claude-haiku-4.5"

type Store struct {
	mu   sync.RWMutex
	path string
	raw  map[string]any
}

type Core struct {
	Server struct {
		Port        int    `json:"port"`
		Host        string `json:"host"`
		OpenBrowser string `json:"openBrowser"`
		TLS         struct {
			Enabled    bool   `json:"enabled"`
			CertFile   string `json:"certFile"`
			KeyFile    string `json:"keyFile"`
			CAFile     string `json:"caFile"`
			Passphrase string `json:"passphrase"`
		} `json:"tls"`
	} `json:"server"`
	Paths struct {
		DefaultProjectDir string `json:"defaultProjectDir"`
	} `json:"paths"`
	Auth struct {
		JWTSecret        string `json:"jwtSecret"`
		RegistrationOpen bool   `json:"registrationOpen"`
	} `json:"auth"`
	Agent struct {
		DefaultModel                string `json:"defaultModel"`
		DefaultPermissionMode       string `json:"defaultPermissionMode"`
		QuestionReflectionEnabled   bool   `json:"questionReflectionEnabled"`
		QuestionReflectionTimeoutMs int    `json:"questionReflectionTimeoutMs"`
	} `json:"agent"`
	Database struct {
		Driver                 string `json:"driver"`
		SQLitePath             string `json:"sqlitePath"`
		PostgresDSN            string `json:"postgresDsn"`
		PostgresSchema         string `json:"postgresSchema"`
		MaxOpenConns           int    `json:"maxOpenConns"`
		MaxIdleConns           int    `json:"maxIdleConns"`
		ConnMaxLifetimeSeconds int    `json:"connMaxLifetimeSeconds"`
		VerifyCanonical        bool   `json:"verifyCanonical"`
		TSOwnedSchema          bool   `json:"tsOwnedSchema"`
	} `json:"database"`
}

func Load() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	dir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create settings directory: %w", err)
	}

	settingsPath := filepath.Join(dir, "settings.json")
	raw := map[string]any{}
	if data, err := os.ReadFile(settingsPath); err == nil {
		if len(data) > 0 {
			if err := json.Unmarshal(data, &raw); err != nil {
				return nil, fmt.Errorf("parse settings.json: %w", err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read settings.json: %w", err)
	}

	legacyRaw := cloneMap(raw)
	defaults := defaultRaw(home)
	changed := mergeMissing(raw, defaults)
	if migrateLegacySettings(raw, legacyRaw) {
		changed = true
	}
	if getString(raw, "auth", "jwtSecret") == "" {
		ensureMap(raw, "auth")["jwtSecret"] = id.Must(48)
		changed = true
	}

	store := &Store{path: settingsPath, raw: raw}
	if changed {
		if err := store.saveLocked(); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func defaultRaw(home string) map[string]any {
	return map[string]any{
		"server": map[string]any{
			"port":        float64(7778),
			"host":        "localhost",
			"openBrowser": "browser",
		},
		"paths": map[string]any{
			"defaultProjectDir": filepath.Join(home, "projects"),
		},
		"auth": map[string]any{
			"jwtSecret":        "",
			"registrationOpen": true,
		},
		"agent": map[string]any{
			"defaultModel":                    DefaultAgentModel,
			"defaultPermissionMode":           "acceptEdits",
			"defaultStartInPlanMode":          false,
			"summaryModel":                    defaultSummaryModel,
			"customModels":                    []any{},
			"hiddenModels":                    []any{},
			"maxTurns":                        float64(200),
			"subagentModels":                  map[string]any{"explore": "", "plan": ""},
			"subagentAllowedModels":           map[string]any{"explore": []any{}, "plan": []any{}, "general": []any{}},
			"legacyEncoding":                  false,
			"freshShellEnv":                   false,
			"requestDumpEnabled":              false,
			"requestDumpErrorsOnly":           false,
			"requestDumpMaxSize":              float64(1024 * 1024),
			"modelContextWindows":             map[string]any{},
			"translateReasoning":              false,
			"defaultRelaxedPlan":              false,
			"planReflectionAutoApprove":       false,
			"questionReflectionEnabled":       false,
			"questionReflectionTimeoutMs":     float64(300000),
			"dangerReflectionEnabled":         true,
			"dangerSkipReadOnlyConfirmations": false,
			"maxTransientRetries":             float64(10),
			"silentToolCallThreshold":         float64(20),
			"retryBackoffCeilMs":              float64(20000),
			"firstTokenTimeoutMs":             float64(60000),
			"autoCompactKeepPairs":            float64(2),
			"autoCompactPruneThreshold":       float64(80),
		},
		"chapters": map[string]any{
			"maxActiveWorktrees":    float64(10),
			"maxActiveContainers":   float64(5),
			"worktreeSizeWarningMb": float64(500),
			"autoSaveOnDormant":     true,
			"dormantAfterMinutes":   float64(0),
		},
		"containers": map[string]any{
			"portRangeStart": float64(10000),
			"portRangeEnd":   float64(20000),
			"proxy":          map[string]any{"enabled": false, "port": float64(7780)},
		},
		"editor": map[string]any{
			"type": "vscode",
		},
		"routines": map[string]any{
			"disabledRoutines": []any{},
			"enabledRoutines":  []any{},
		},
		"customApiProviders": []any{},
		"openaiProviders":    []any{},
		"anthropicProviders": []any{},
		"codex": map[string]any{
			"loadBalancingMode":      "tier-balanced",
			"tierOrder":              []any{"pro", "prolite", "plus", "team", "free"},
			"defaultReasoningEffort": "medium",
			"useWebSocket":           true,
		},
		"update": map[string]any{
			"serverUrl":            "https://narrafork-update.b.domexie.cn",
			"product":              "narrafork",
			"channel":              "stable",
			"checkIntervalMinutes": float64(60),
			"autoDownload":         false,
		},
		"vnet": map[string]any{
			"enabled":             true,
			"allowAnonymousRelay": false,
			"maxPeersPerNetwork":  float64(64),
			"maxMessageBytes":     float64(1024 * 1024),
			"udp":                 map[string]any{"enabled": true, "host": "0.0.0.0", "port": float64(0)},
		},
		"shares": map[string]any{
			"defaultExpiryHours": float64(24),
			"maxFileSizeMb":      float64(4096),
		},
	}
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) Raw() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMap(s.raw)
}

func (s *Store) Core() Core {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var c Core
	c.Server.Port = getInt(s.raw, 7778, "server", "port")
	c.Server.Host = stringOr(getString(s.raw, "server", "host"), "localhost")
	c.Server.OpenBrowser = stringOr(getString(s.raw, "server", "openBrowser"), "browser")
	c.Server.TLS.Enabled = getBool(s.raw, false, "server", "tls", "enabled")
	c.Server.TLS.CertFile = getString(s.raw, "server", "tls", "certFile")
	c.Server.TLS.KeyFile = getString(s.raw, "server", "tls", "keyFile")
	c.Server.TLS.CAFile = getString(s.raw, "server", "tls", "caFile")
	c.Server.TLS.Passphrase = getString(s.raw, "server", "tls", "passphrase")
	c.Paths.DefaultProjectDir = getString(s.raw, "paths", "defaultProjectDir")
	c.Auth.JWTSecret = getString(s.raw, "auth", "jwtSecret")
	c.Auth.RegistrationOpen = getBool(s.raw, true, "auth", "registrationOpen")
	c.Agent.DefaultModel = stringOr(getString(s.raw, "agent", "defaultModel"), DefaultAgentModel)
	c.Agent.DefaultPermissionMode = stringOr(getString(s.raw, "agent", "defaultPermissionMode"), "acceptEdits")
	c.Agent.QuestionReflectionEnabled = getBool(s.raw, false, "agent", "questionReflectionEnabled")
	c.Agent.QuestionReflectionTimeoutMs = getInt(s.raw, 300000, "agent", "questionReflectionTimeoutMs")
	c.Database.Driver = getString(s.raw, "database", "driver")
	c.Database.SQLitePath = getString(s.raw, "database", "sqlitePath")
	c.Database.PostgresDSN = getString(s.raw, "database", "postgresDsn")
	c.Database.PostgresSchema = getString(s.raw, "database", "postgresSchema")
	c.Database.MaxOpenConns = getInt(s.raw, 0, "database", "maxOpenConns")
	c.Database.MaxIdleConns = getInt(s.raw, 0, "database", "maxIdleConns")
	c.Database.ConnMaxLifetimeSeconds = getInt(s.raw, 0, "database", "connMaxLifetimeSeconds")
	c.Database.VerifyCanonical = getBool(s.raw, true, "database", "verifyCanonical")
	c.Database.TSOwnedSchema = getBool(s.raw, true, "database", "tsOwnedSchema")
	return c
}

func (s *Store) Patch(patch map[string]any) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deepMerge(s.raw, patch)
	if getString(s.raw, "auth", "jwtSecret") == "" {
		ensureMap(s.raw, "auth")["jwtSecret"] = id.Must(48)
	}
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return cloneMap(s.raw), nil
}

func (s *Store) Replace(raw map[string]any) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.raw = cloneMap(raw)
	if getString(s.raw, "auth", "jwtSecret") == "" {
		ensureMap(s.raw, "auth")["jwtSecret"] = id.Must(48)
	}
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return cloneMap(s.raw), nil
}

func (s *Store) MCPServers() []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneServerList(s.raw["mcpServers"])
}

func (s *Store) SetMCPServers(servers []map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.raw["mcpServers"] = cloneServerListAsAny(servers)
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.raw, "", "\t")
	if err != nil {
		return fmt.Errorf("encode settings.json: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("write settings.json: %w", err)
	}
	return nil
}

func mergeMissing(dst, src map[string]any) bool {
	changed := false
	for k, v := range src {
		current, ok := dst[k]
		if !ok {
			dst[k] = cloneAny(v)
			changed = true
			continue
		}
		currentMap, okCurrent := current.(map[string]any)
		vMap, okV := v.(map[string]any)
		if okCurrent && okV {
			if mergeMissing(currentMap, vMap) {
				changed = true
			}
		}
	}
	return changed
}

func migrateLegacySettings(raw, legacyRaw map[string]any) bool {
	changed := false
	agent := ensureMap(raw, "agent")
	legacyAgent, _ := legacyRaw["agent"].(map[string]any)
	if editor, ok := raw["editor"].(map[string]any); ok {
		if legacyEncoding, exists := editor["legacyEncoding"]; exists {
			if legacyEnabled, ok := legacyEncoding.(bool); ok && legacyEnabled {
				if agent["legacyEncoding"] != true {
					agent["legacyEncoding"] = true
					changed = true
				}
			}
			delete(editor, "legacyEncoding")
			changed = true
		}
	}
	if legacyYOLO, exists := agent["yoloSkipReadOnlyConfirmations"]; exists {
		if _, hasDanger := legacyAgent["dangerSkipReadOnlyConfirmations"]; !hasDanger {
			agent["dangerSkipReadOnlyConfirmations"] = legacyYOLO
			changed = true
		}
		delete(agent, "yoloSkipReadOnlyConfirmations")
		changed = true
	}
	if _, exists := agent["smartInterruptionCheck"]; exists {
		delete(agent, "smartInterruptionCheck")
		changed = true
	}
	if stringValue(agent["defaultPermissionMode"]) == "plan" {
		agent["defaultPermissionMode"] = "default"
		agent["defaultStartInPlanMode"] = true
		changed = true
	}
	if pool, ok := agent["subagentAllowedModels"].([]any); ok {
		agent["subagentAllowedModels"] = map[string]any{
			"explore": cloneAny(pool),
			"plan":    cloneAny(pool),
			"general": cloneAny(pool),
		}
		changed = true
	}
	changed = migrateLegacyOpenAISettings(raw) || changed
	changed = normalizeProviderPrefixes(raw) || changed
	changed = migrateLegacyMCPBehaviors(raw) || changed
	changed = normalizeCodexReasoningEffort(raw) || changed
	changed = normalizeSettingsProxyURLs(raw) || changed
	return changed
}

func migrateLegacyOpenAISettings(raw map[string]any) bool {
	legacy, ok := raw["openai"].(map[string]any)
	if !ok {
		if _, exists := raw["openai"]; exists {
			delete(raw, "openai")
			return true
		}
		return false
	}
	changed := false
	if len(anySlice(raw["openaiProviders"])) == 0 && len(legacy) > 0 {
		provider := cloneMap(legacy)
		provider["id"] = firstString(stringValue(provider["id"]), id.Short())
		provider["name"] = firstString(stringValue(provider["name"]), "OpenAI")
		provider["prefix"] = firstString(stringValue(provider["prefix"]), "openai")
		provider["apiKey"] = firstString(
			stringValue(provider["apiKey"]),
			stringValue(provider["key"]),
			stringValue(provider["api_key"]),
		)
		provider["baseUrl"] = firstString(
			stringValue(provider["baseUrl"]),
			stringValue(provider["baseURL"]),
			stringValue(provider["base_url"]),
		)
		provider["defaultModel"] = firstString(
			stringValue(provider["defaultModel"]),
			stringValue(provider["model"]),
			stringValue(provider["modelName"]),
		)
		raw["openaiProviders"] = []any{provider}
		changed = true
	}
	delete(raw, "openai")
	changed = true
	return changed
}

func normalizeProviderPrefixes(raw map[string]any) bool {
	changed := false
	for field, fallbackPrefix := range map[string]string{
		"openaiProviders":    "openai",
		"customApiProviders": "custom",
	} {
		providers, ok := raw[field].([]any)
		if !ok {
			continue
		}
		for i, item := range providers {
			provider, ok := item.(map[string]any)
			if !ok || strings.TrimSpace(stringValue(provider["prefix"])) != "" {
				continue
			}
			prefix := fallbackPrefix
			if field == "customApiProviders" && i > 0 {
				prefix = fmt.Sprintf("%s%d", fallbackPrefix, i+1)
			}
			provider["prefix"] = prefix
			changed = true
		}
	}
	return changed
}

func migrateLegacyMCPBehaviors(raw map[string]any) bool {
	servers, ok := raw["mcpServers"].([]any)
	if !ok {
		return false
	}
	changed := false
	for _, item := range servers {
		server, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if server["defaultBehavior"] == "allow" {
			server["defaultBehavior"] = "readWrite"
			changed = true
		}
		permissions, ok := server["toolPermissions"].([]any)
		if !ok {
			continue
		}
		for _, permissionItem := range permissions {
			permission, ok := permissionItem.(map[string]any)
			if ok && permission["behavior"] == "allow" {
				permission["behavior"] = "readWrite"
				changed = true
			}
		}
	}
	return changed
}

func normalizeCodexReasoningEffort(raw map[string]any) bool {
	codex, ok := raw["codex"].(map[string]any)
	if !ok {
		return false
	}
	if value, exists := codex["defaultReasoningEffort"]; exists && value == nil {
		codex["defaultReasoningEffort"] = "medium"
		return true
	}
	return false
}

func normalizeSettingsProxyURLs(raw map[string]any) bool {
	changed := false
	for _, section := range []string{"codex", "kiro"} {
		if settings, ok := raw[section].(map[string]any); ok {
			changed = normalizeProxyField(settings, "proxy") || changed
		}
	}
	if agent, ok := raw["agent"].(map[string]any); ok {
		if policy, ok := agent["webFetchPolicy"].(map[string]any); ok {
			if proxy, ok := policy["proxy"].(map[string]any); ok {
				changed = normalizeProxyField(proxy, "url") || changed
			}
		}
	}
	for _, field := range []string{"customApiProviders", "openaiProviders", "anthropicProviders", "nkpProviders", "nugProviders", "clineProviders"} {
		for _, item := range anySlice(raw[field]) {
			provider, ok := item.(map[string]any)
			if !ok {
				continue
			}
			for _, proxyKey := range []string{"proxy", "proxyUrl", "proxyURL"} {
				changed = normalizeProxyField(provider, proxyKey) || changed
			}
		}
	}
	return changed
}

func normalizeProxyField(target map[string]any, key string) bool {
	value, ok := target[key].(string)
	if !ok {
		return false
	}
	normalized := normalizeProxyURL(value)
	if normalized == value {
		return false
	}
	target[key] = normalized
	return true
}

func normalizeProxyURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "://") {
		return trimmed
	}
	return "http://" + trimmed
}

func anySlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func firstString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		if incoming, ok := v.(map[string]any); ok {
			if existing, ok := dst[k].(map[string]any); ok {
				deepMerge(existing, incoming)
				continue
			}
		}
		dst[k] = cloneAny(v)
	}
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneAny(v)
	}
	return out
}

func cloneServerList(v any) []map[string]any {
	raw, ok := v.([]any)
	if !ok {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		server, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, cloneMap(server))
	}
	return out
}

func cloneServerListAsAny(servers []map[string]any) []any {
	out := make([]any, 0, len(servers))
	for _, server := range servers {
		if server == nil {
			continue
		}
		out = append(out, cloneMap(server))
	}
	return out
}

func cloneAny(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneAny(item)
		}
		return out
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, cloneMap(item))
		}
		return out
	default:
		return typed
	}
}

func ensureMap(m map[string]any, key string) map[string]any {
	if child, ok := m[key].(map[string]any); ok {
		return child
	}
	child := map[string]any{}
	m[key] = child
	return child
}

func getNested(m map[string]any, path ...string) any {
	var current any = m
	for _, p := range path {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = obj[p]
	}
	return current
}

func getString(m map[string]any, path ...string) string {
	v, _ := getNested(m, path...).(string)
	return v
}

func getBool(m map[string]any, fallback bool, path ...string) bool {
	v := getNested(m, path...)
	if b, ok := v.(bool); ok {
		return b
	}
	return fallback
}

func getInt(m map[string]any, fallback int, path ...string) int {
	switch v := getNested(m, path...).(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	}
	return fallback
}

func stringOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
