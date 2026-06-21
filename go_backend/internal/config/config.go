package config

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

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
		SummaryModel                string `json:"summaryModel"`
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
			"planModeAllowInlinePlan":         true,
			"planReflectionAutoApprove":       false,
			"questionReflectionEnabled":       false,
			"questionReflectionTimeoutMs":     float64(300000),
			"dangerReflectionLevel":           "standard",
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
			"useWebSearch":           true,
			"useImageGeneration":     true,
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
	c.Agent.SummaryModel = stringOr(getString(s.raw, "agent", "summaryModel"), defaultSummaryModel)
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
	if agentPatch, ok := patch["agent"].(map[string]any); ok {
		normalizeDangerReflectionPatchSettings(ensureMap(s.raw, "agent"), agentPatch)
	}
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
	if normalizeDangerReflectionSettings(agent, legacyAgent) {
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
	changed = migrateLegacyCodexOAuthCredentialPool(raw) || changed
	changed = normalizeProviderPrefixes(raw) || changed
	changed = normalizeCustomAPIProviderSettings(raw, legacyRaw) || changed
	changed = purgeStaleAgentModelReferences(raw) || changed
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

func migrateLegacyCodexOAuthCredentialPool(raw map[string]any) bool {
	providers, ok := raw["openaiProviders"].([]any)
	if !ok || len(providers) == 0 {
		return false
	}
	legacyCredentials := []map[string]any{}
	changed := false
	for _, item := range providers {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		oauth, hasOAuth := provider["codexOAuth"].(map[string]any)
		if !hasOAuth {
			continue
		}
		if credential := codexCredentialFromLegacyOAuth(oauth); credential != nil {
			legacyCredentials = append(legacyCredentials, credential)
		}
		delete(provider, "codexOAuth")
		delete(provider, "codexProxy")
		changed = true
	}
	if len(legacyCredentials) > 0 {
		writeLegacyCodexCredentials(legacyCredentials)
	}
	return changed
}

func codexCredentialFromLegacyOAuth(oauth map[string]any) map[string]any {
	refreshToken := strings.TrimSpace(stringValue(oauth["refreshToken"]))
	accessToken := strings.TrimSpace(stringValue(oauth["accessToken"]))
	if refreshToken == "" && accessToken == "" {
		return nil
	}
	tokenInfo := codexTokenInfoFromAccessToken(accessToken)
	accountID := firstNonEmptyString(stringValue(oauth["accountId"]), tokenInfo.accountID)
	credential := map[string]any{
		"id":       id.Short(),
		"priority": float64(0),
		"disabled": false,
	}
	if refreshToken != "" {
		credential["refreshToken"] = refreshToken
	}
	if accessToken != "" {
		credential["accessToken"] = accessToken
	}
	if expiresAt, ok := normalizedExpiresAtValue(oauth["expiresAt"]); ok {
		credential["expiresAt"] = expiresAt
	}
	if accountID != "" {
		credential["accountId"] = accountID
	}
	if tokenInfo.email != "" {
		credential["email"] = tokenInfo.email
	}
	if tokenInfo.sub != "" {
		credential["sub"] = tokenInfo.sub
	}
	if displayName := firstNonEmptyString(tokenInfo.email, accountID); displayName != "" {
		credential["displayName"] = displayName
	}
	return credential
}

func writeLegacyCodexCredentials(credentials []map[string]any) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	dir := filepath.Join(home, ".narrafork")
	path := filepath.Join(dir, "codex-credentials.json")
	existing := []map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &existing)
	}
	seen := map[string]struct{}{}
	for _, entry := range existing {
		for _, key := range codexCredentialDedupeKeys(entry) {
			seen[key] = struct{}{}
		}
	}
	added := false
	for _, credential := range credentials {
		keys := codexCredentialDedupeKeys(credential)
		if len(keys) == 0 || hasAnySeenKey(seen, keys) {
			continue
		}
		credential["priority"] = float64(len(existing))
		existing = append(existing, credential)
		added = true
		for _, key := range keys {
			seen[key] = struct{}{}
		}
	}
	if !added {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

func codexCredentialDedupeKeys(credential map[string]any) []string {
	keys := []string{}
	refreshToken := strings.TrimSpace(stringValue(credential["refreshToken"]))
	accessToken := strings.TrimSpace(stringValue(credential["accessToken"]))
	if refreshToken != "" {
		keys = append(keys, "rt:"+sha256Hex(refreshToken))
	}
	tokenInfo := codexTokenInfoFromAccessToken(accessToken)
	sub := firstNonEmptyString(stringValue(credential["sub"]), tokenInfo.sub)
	accountID := firstNonEmptyString(stringValue(credential["accountId"]), tokenInfo.accountID)
	email := firstNonEmptyString(stringValue(credential["email"]), tokenInfo.email)
	if sub != "" {
		keys = append(keys, "sub:"+sub)
	}
	if accountID != "" && email != "" {
		keys = append(keys, "account-email:"+accountID+":"+email)
	}
	if accessToken != "" {
		keys = append(keys, "at:"+sha256Hex(accessToken))
	}
	return keys
}

func hasAnySeenKey(seen map[string]struct{}, keys []string) bool {
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			return true
		}
	}
	return false
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type codexTokenInfo struct {
	accountID string
	email     string
	sub       string
}

func codexTokenInfoFromAccessToken(accessToken string) codexTokenInfo {
	claims := parseJWTClaims(accessToken)
	if len(claims) == 0 {
		return codexTokenInfo{}
	}
	return codexTokenInfo{
		accountID: firstNonEmptyString(
			stringValue(claims["chatgpt_account_id"]),
			codexNestedAuthAccountID(claims),
			codexFirstOrganizationID(claims),
		),
		email: strings.TrimSpace(stringValue(claims["email"])),
		sub:   strings.TrimSpace(stringValue(claims["sub"])),
	}
}

func parseJWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || strings.TrimSpace(parts[1]) == "" {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	return claims
}

func codexNestedAuthAccountID(claims map[string]any) string {
	auth, _ := claims["https://api.openai.com/auth"].(map[string]any)
	return strings.TrimSpace(stringValue(auth["chatgpt_account_id"]))
}

func codexFirstOrganizationID(claims map[string]any) string {
	organizations, _ := claims["organizations"].([]any)
	if len(organizations) == 0 {
		return ""
	}
	organization, _ := organizations[0].(map[string]any)
	return strings.TrimSpace(stringValue(organization["id"]))
}

func normalizedExpiresAtValue(value any) (float64, bool) {
	number, ok := numericValue(value)
	if !ok {
		return 0, false
	}
	if number > 0 && number < 100_000_000_000 {
		return number * 1000, true
	}
	return number, true
}

func numericValue(value any) (float64, bool) {
	var number float64
	switch v := value.(type) {
	case float64:
		number = v
	case int:
		number = float64(v)
	case int64:
		number = float64(v)
	case json.Number:
		parsed, err := v.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err == nil {
			number = parsed
			break
		}
		parsedTime, err := time.Parse(time.RFC3339Nano, trimmed)
		if err != nil {
			return 0, false
		}
		number = float64(parsedTime.UnixMilli())
	default:
		return 0, false
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return number, true
}

func normalizeCustomAPIProviderSettings(raw, legacyRaw map[string]any) bool {
	beforeCustom := cloneAny(raw["customApiProviders"])
	beforeOpenAI := cloneAny(raw["openaiProviders"])
	beforeAnthropic := cloneAny(raw["anthropicProviders"])

	customProviders, customWasArrayInLegacy := legacyRaw["customApiProviders"].([]any)
	if customWasArrayInLegacy {
		customProviders = anySlice(raw["customApiProviders"])
	} else {
		customProviders = deriveCustomAPIProvidersFromLegacy(
			anySlice(raw["openaiProviders"]),
			anySlice(raw["anthropicProviders"]),
		)
	}

	normalizedCustom := make([]any, 0, len(customProviders))
	for _, item := range customProviders {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		normalizedCustom = append(normalizedCustom, normalizeCustomAPIProvider(provider))
	}
	raw["customApiProviders"] = normalizedCustom
	raw["openaiProviders"] = customAPIProvidersToOpenAI(normalizedCustom)
	raw["anthropicProviders"] = customAPIProvidersToAnthropic(normalizedCustom)

	return !reflect.DeepEqual(beforeCustom, raw["customApiProviders"]) ||
		!reflect.DeepEqual(beforeOpenAI, raw["openaiProviders"]) ||
		!reflect.DeepEqual(beforeAnthropic, raw["anthropicProviders"])
}

func deriveCustomAPIProvidersFromLegacy(openaiProviders, anthropicProviders []any) []any {
	byID := map[string]map[string]any{}
	order := []string{}
	for _, item := range openaiProviders {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		converted := openAIProviderToCustomAPI(provider)
		providerID := stringValue(converted["id"])
		if _, exists := byID[providerID]; !exists {
			order = append(order, providerID)
		}
		byID[providerID] = converted
	}
	for _, item := range anthropicProviders {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		converted := anthropicProviderToCustomAPI(provider)
		providerID := stringValue(converted["id"])
		if existing, exists := byID[providerID]; exists {
			merged := cloneMap(existing)
			for key, value := range converted {
				merged[key] = value
			}
			for _, key := range []string{"codexAccountId", "codexWebSocket", "codexWebSearch", "codexImageGeneration"} {
				if value, ok := existing[key]; ok {
					merged[key] = value
				}
			}
			byID[providerID] = merged
			continue
		}
		order = append(order, providerID)
		byID[providerID] = converted
	}
	out := make([]any, 0, len(order))
	for _, providerID := range order {
		out = append(out, byID[providerID])
	}
	return out
}

func normalizeCustomAPIProvider(provider map[string]any) map[string]any {
	out := cloneMap(provider)
	if strings.TrimSpace(stringValue(out["protocol"])) == "" {
		out["protocol"] = "responses-compatible"
	}
	if value, exists := out["defaultReasoningEffort"]; exists && value == nil {
		delete(out, "defaultReasoningEffort")
	}
	setDefaultIfMissing(out, "codexAccountId", "")
	setDefaultIfMissing(out, "codexWebSocket", false)
	setDefaultIfMissing(out, "codexWebSearch", true)
	setDefaultIfMissing(out, "codexImageGeneration", true)
	setDefaultIfMissing(out, "tlsRejectUnauthorized", true)
	setDefaultIfMissing(out, "proxy", "")
	return out
}

func openAIProviderToCustomAPI(provider map[string]any) map[string]any {
	converted := map[string]any{
		"id":           stringValue(provider["id"]),
		"name":         stringValue(provider["name"]),
		"prefix":       stringValue(provider["prefix"]),
		"apiKey":       stringValue(provider["apiKey"]),
		"baseUrl":      stringValue(provider["baseUrl"]),
		"defaultModel": stringValue(provider["defaultModel"]),
		"protocol":     customAPIProtocolFromOpenAI(provider),
	}
	copyFieldIfPresent(converted, provider, "disabled")
	copyFieldIfPresent(converted, provider, "defaultContextWindow")
	copyFieldIfPresent(converted, provider, "codexAccountId")
	copyFieldIfPresent(converted, provider, "codexWebSocket")
	copyFieldIfPresent(converted, provider, "codexWebSearch")
	copyFieldIfPresent(converted, provider, "codexImageGeneration")
	return normalizeCustomAPIProvider(converted)
}

func anthropicProviderToCustomAPI(provider map[string]any) map[string]any {
	converted := map[string]any{
		"id":           stringValue(provider["id"]),
		"name":         stringValue(provider["name"]),
		"prefix":       stringValue(provider["prefix"]),
		"apiKey":       stringValue(provider["apiKey"]),
		"baseUrl":      stringValue(provider["baseUrl"]),
		"defaultModel": stringValue(provider["defaultModel"]),
		"protocol":     customAPIProtocolFromAnthropic(boolValue(provider["officialApi"])),
	}
	copyFieldIfPresent(converted, provider, "disabled")
	copyFieldIfPresent(converted, provider, "defaultContextWindow")
	copyFieldIfPresent(converted, provider, "defaultReasoningEffort")
	copyFieldIfPresent(converted, provider, "proxy")
	copyFieldIfPresent(converted, provider, "tlsRejectUnauthorized")
	return normalizeCustomAPIProvider(converted)
}

func customAPIProvidersToOpenAI(providers []any) []any {
	out := []any{}
	for _, item := range providers {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		apiMode := customAPIProtocolToOpenAIApiMode(stringValue(provider["protocol"]))
		if apiMode == "" {
			continue
		}
		converted := map[string]any{
			"id":           stringValue(provider["id"]),
			"name":         stringValue(provider["name"]),
			"prefix":       stringValue(provider["prefix"]),
			"apiKey":       stringValue(provider["apiKey"]),
			"baseUrl":      stringValue(provider["baseUrl"]),
			"defaultModel": stringValue(provider["defaultModel"]),
			"apiMode":      apiMode,
		}
		copyFieldIfPresent(converted, provider, "disabled")
		copyFieldIfPresent(converted, provider, "defaultContextWindow")
		copyFieldIfPresent(converted, provider, "codexAccountId")
		copyFieldIfPresent(converted, provider, "codexWebSocket")
		copyFieldIfPresent(converted, provider, "codexWebSearch")
		copyFieldIfPresent(converted, provider, "codexImageGeneration")
		out = append(out, converted)
	}
	return out
}

func customAPIProvidersToAnthropic(providers []any) []any {
	out := []any{}
	for _, item := range providers {
		provider, ok := item.(map[string]any)
		if !ok {
			continue
		}
		protocol := strings.ToLower(strings.TrimSpace(stringValue(provider["protocol"])))
		if protocol != "anthropic-official" && protocol != "anthropic-compatible" {
			continue
		}
		converted := map[string]any{
			"id":           stringValue(provider["id"]),
			"name":         stringValue(provider["name"]),
			"prefix":       stringValue(provider["prefix"]),
			"apiKey":       stringValue(provider["apiKey"]),
			"baseUrl":      stringValue(provider["baseUrl"]),
			"defaultModel": stringValue(provider["defaultModel"]),
			"officialApi":  protocol == "anthropic-official",
		}
		copyFieldIfPresent(converted, provider, "disabled")
		copyFieldIfPresent(converted, provider, "defaultContextWindow")
		copyFieldIfPresent(converted, provider, "proxy")
		copyFieldIfPresent(converted, provider, "tlsRejectUnauthorized")
		if value, ok := provider["defaultReasoningEffort"]; ok && value != nil {
			converted["defaultReasoningEffort"] = value
		}
		out = append(out, converted)
	}
	return out
}

func customAPIProtocolFromOpenAI(provider map[string]any) string {
	switch strings.ToLower(strings.TrimSpace(stringValue(provider["apiMode"]))) {
	case "codex":
		return "codex-native"
	case "completions":
		return "completions-compatible"
	case "responses":
		return "responses-compatible"
	default:
		if responsesAPI, ok := provider["responsesApi"].(bool); ok && !responsesAPI {
			return "completions-compatible"
		}
		return "responses-compatible"
	}
}

func customAPIProtocolFromAnthropic(officialAPI bool) string {
	if officialAPI {
		return "anthropic-official"
	}
	return "anthropic-compatible"
}

func customAPIProtocolToOpenAIApiMode(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "codex-native":
		return "codex"
	case "responses-compatible":
		return "responses"
	case "completions-compatible":
		return "completions"
	default:
		return ""
	}
}

func copyFieldIfPresent(dst, src map[string]any, key string) {
	if value, ok := src[key]; ok {
		dst[key] = value
	}
}

func setDefaultIfMissing(target map[string]any, key string, value any) {
	if current, ok := target[key]; !ok || current == nil {
		target[key] = value
	}
}

func purgeStaleAgentModelReferences(raw map[string]any) bool {
	agent, ok := raw["agent"].(map[string]any)
	if !ok {
		return false
	}
	activePrefixes := activeModelProviderPrefixes(raw)
	isStale := func(value string) bool {
		prefix, _, ok := strings.Cut(value, ":")
		return ok && prefix != "" && !activePrefixes[prefix]
	}

	changed := false
	if isStale(stringValue(agent["summaryModel"])) {
		agent["summaryModel"] = ""
		changed = true
	}
	if subagent, ok := agent["subagentModels"].(map[string]any); ok {
		for _, key := range []string{"explore", "plan"} {
			if isStale(stringValue(subagent[key])) {
				subagent[key] = ""
				changed = true
			}
		}
	}
	if hidden, ok := agent["hiddenModels"].([]any); ok {
		kept := make([]any, 0, len(hidden))
		for _, item := range hidden {
			if isStale(stringValue(item)) {
				changed = true
				continue
			}
			kept = append(kept, item)
		}
		agent["hiddenModels"] = kept
	}
	if windows, ok := agent["modelContextWindows"].(map[string]any); ok {
		kept := map[string]any{}
		for key, value := range windows {
			if isStale(key) {
				changed = true
				continue
			}
			kept[key] = value
		}
		agent["modelContextWindows"] = kept
	}
	if custom, ok := agent["customModels"].([]any); ok {
		kept := make([]any, 0, len(custom))
		for _, item := range custom {
			model, ok := item.(map[string]any)
			if ok && isStale(stringValue(model["value"])) {
				changed = true
				continue
			}
			kept = append(kept, item)
		}
		agent["customModels"] = kept
	}
	return changed
}

func activeModelProviderPrefixes(raw map[string]any) map[string]bool {
	active := map[string]bool{"kiro": true, "codex": true}
	for _, field := range []string{"openaiProviders", "anthropicProviders", "nkpProviders", "nugProviders", "clineProviders"} {
		for _, item := range anySlice(raw[field]) {
			provider, ok := item.(map[string]any)
			if !ok {
				continue
			}
			prefix := strings.TrimSpace(stringValue(provider["prefix"]))
			if prefix != "" {
				active[prefix] = true
			}
		}
	}
	return active
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

func boolValue(value any) bool {
	b, _ := value.(bool)
	return b
}

func dangerReflectionBool(value any, fallback bool) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return fallback
}

func normalizeDangerReflectionLevel(value any, legacyEnabled bool) string {
	switch strings.ToLower(strings.TrimSpace(stringValue(value))) {
	case "off", "light", "standard", "strict":
		return strings.ToLower(strings.TrimSpace(stringValue(value)))
	default:
		if legacyEnabled {
			return "standard"
		}
		return "off"
	}
}

func normalizeDangerReflectionSettings(agent, legacyAgent map[string]any) bool {
	legacyEnabled := dangerReflectionBool(legacyAgent["dangerReflectionEnabled"], true)
	_, hadLevel := legacyAgent["dangerReflectionLevel"]
	level := "standard"
	if hadLevel {
		level = normalizeDangerReflectionLevel(legacyAgent["dangerReflectionLevel"], legacyEnabled)
	} else if !legacyEnabled {
		level = "off"
	}
	return setDangerReflectionLevel(agent, level)
}

func normalizeDangerReflectionPatchSettings(agent, patch map[string]any) bool {
	if rawLevel, hasLevel := patch["dangerReflectionLevel"]; hasLevel {
		return setDangerReflectionLevel(agent, normalizeDangerReflectionLevel(rawLevel, dangerReflectionBool(agent["dangerReflectionEnabled"], true)))
	}
	if enabled, ok := patch["dangerReflectionEnabled"].(bool); ok {
		if enabled {
			return setDangerReflectionLevel(agent, "standard")
		}
		return setDangerReflectionLevel(agent, "off")
	}
	return false
}

func setDangerReflectionLevel(agent map[string]any, level string) bool {
	changed := false
	if agent["dangerReflectionLevel"] != level {
		agent["dangerReflectionLevel"] = level
		changed = true
	}
	enabled := level != "off"
	if agent["dangerReflectionEnabled"] != enabled {
		agent["dangerReflectionEnabled"] = enabled
		changed = true
	}
	return changed
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
