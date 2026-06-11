package config

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultAgentModelMatchesTypeScriptDefault(t *testing.T) {
	if DefaultAgentModel != "kiro:claude-sonnet-4.5" {
		t.Fatalf("DefaultAgentModel = %q, want TypeScript default", DefaultAgentModel)
	}
	raw := defaultRaw(t.TempDir())
	agent, ok := raw["agent"].(map[string]any)
	if !ok {
		t.Fatalf("defaultRaw missing agent settings: %#v", raw)
	}
	if agent["defaultModel"] != DefaultAgentModel {
		t.Fatalf("defaultRaw defaultModel = %#v, want %q", agent["defaultModel"], DefaultAgentModel)
	}
	store := &Store{raw: map[string]any{}}
	if got := store.Core().Agent.DefaultModel; got != DefaultAgentModel {
		t.Fatalf("Core fallback defaultModel = %q, want %q", got, DefaultAgentModel)
	}
	if got := store.Core().Agent.SummaryModel; got != defaultSummaryModel {
		t.Fatalf("Core fallback summaryModel = %q, want %q", got, defaultSummaryModel)
	}
}

func TestDefaultRawMatchesTypeScriptReadinessDefaults(t *testing.T) {
	raw := defaultRaw("/home/test")
	expectDefaultValue(t, raw, "server.port", float64(7778))
	expectDefaultValue(t, raw, "server.host", "localhost")
	expectDefaultValue(t, raw, "server.openBrowser", "browser")
	expectDefaultValue(t, raw, "paths.defaultProjectDir", "/home/test/projects")
	expectDefaultValue(t, raw, "auth.jwtSecret", "")
	expectDefaultValue(t, raw, "auth.registrationOpen", true)

	expectDefaultValue(t, raw, "agent.defaultModel", "kiro:claude-sonnet-4.5")
	expectDefaultValue(t, raw, "agent.defaultPermissionMode", "acceptEdits")
	expectDefaultValue(t, raw, "agent.defaultStartInPlanMode", false)
	expectDefaultValue(t, raw, "agent.summaryModel", "kiro:claude-haiku-4.5")
	expectDefaultValue(t, raw, "agent.maxTurns", float64(200))
	expectDefaultValue(t, raw, "agent.requestDumpMaxSize", float64(1024*1024))
	expectDefaultValue(t, raw, "agent.questionReflectionEnabled", false)
	expectDefaultValue(t, raw, "agent.questionReflectionTimeoutMs", float64(300000))
	expectDefaultValue(t, raw, "agent.dangerReflectionLevel", "standard")
	expectDefaultValue(t, raw, "agent.dangerReflectionEnabled", true)
	expectDefaultValue(t, raw, "agent.maxTransientRetries", float64(10))
	expectDefaultValue(t, raw, "agent.firstTokenTimeoutMs", float64(60000))
	expectDefaultValue(t, raw, "agent.autoCompactPruneThreshold", float64(80))

	expectDefaultValue(t, raw, "chapters.maxActiveWorktrees", float64(10))
	expectDefaultValue(t, raw, "chapters.maxActiveContainers", float64(5))
	expectDefaultValue(t, raw, "chapters.worktreeSizeWarningMb", float64(500))
	expectDefaultValue(t, raw, "chapters.autoSaveOnDormant", true)
	expectDefaultValue(t, raw, "chapters.dormantAfterMinutes", float64(0))

	expectDefaultValue(t, raw, "containers.portRangeStart", float64(10000))
	expectDefaultValue(t, raw, "containers.portRangeEnd", float64(20000))
	expectDefaultValue(t, raw, "containers.proxy.enabled", false)
	expectDefaultValue(t, raw, "containers.proxy.port", float64(7780))
	expectDefaultValue(t, raw, "editor.type", "vscode")
	expectDefaultValue(t, raw, "codex.loadBalancingMode", "tier-balanced")
	expectDefaultValue(t, raw, "codex.defaultReasoningEffort", "medium")
	expectDefaultValue(t, raw, "codex.useWebSocket", true)
	expectDefaultValue(t, raw, "codex.useWebSearch", true)
	expectDefaultValue(t, raw, "codex.useImageGeneration", true)
	expectDefaultValue(t, raw, "update.serverUrl", "https://narrafork-update.b.domexie.cn")
	expectDefaultValue(t, raw, "update.product", "narrafork")
	expectDefaultValue(t, raw, "update.channel", "stable")
	expectDefaultValue(t, raw, "update.checkIntervalMinutes", float64(60))
	expectDefaultValue(t, raw, "update.autoDownload", false)
	expectDefaultValue(t, raw, "vnet.enabled", true)
	expectDefaultValue(t, raw, "vnet.allowAnonymousRelay", false)
	expectDefaultValue(t, raw, "vnet.maxPeersPerNetwork", float64(64))
	expectDefaultValue(t, raw, "vnet.maxMessageBytes", float64(1024*1024))
	expectDefaultValue(t, raw, "vnet.udp.enabled", true)
	expectDefaultValue(t, raw, "vnet.udp.host", "0.0.0.0")
	expectDefaultValue(t, raw, "vnet.udp.port", float64(0))
	expectDefaultValue(t, raw, "shares.defaultExpiryHours", float64(24))
	expectDefaultValue(t, raw, "shares.maxFileSizeMb", float64(4096))

	expectNoDefaultValue(t, raw, "server.tls")
	expectNoDefaultValue(t, raw, "agent.contextThresholds")
	expectNoDefaultValue(t, raw, "nkpProviders")
	expectNoDefaultValue(t, raw, "nugProviders")
	expectNoDefaultValue(t, raw, "clineProviders")
	expectNoDefaultValue(t, raw, "kiro")
	expectNoDefaultValue(t, raw, "mcpServers")
}

func TestCoreReadsOptionalDatabaseSettings(t *testing.T) {
	store := &Store{raw: map[string]any{
		"database": map[string]any{
			"driver":                 "postgres",
			"sqlitePath":             "/tmp/narrafork.db",
			"postgresDsn":            "postgres://example/db",
			"postgresSchema":         "narrafork_app",
			"maxOpenConns":           float64(12),
			"maxIdleConns":           float64(4),
			"connMaxLifetimeSeconds": float64(60),
			"verifyCanonical":        false,
			"tsOwnedSchema":          false,
		},
	}}
	core := store.Core()
	if core.Database.Driver != "postgres" || core.Database.SQLitePath != "/tmp/narrafork.db" || core.Database.PostgresDSN != "postgres://example/db" || core.Database.PostgresSchema != "narrafork_app" {
		t.Fatalf("unexpected database strings: %#v", core.Database)
	}
	if core.Database.MaxOpenConns != 12 || core.Database.MaxIdleConns != 4 || core.Database.ConnMaxLifetimeSeconds != 60 {
		t.Fatalf("unexpected database pool settings: %#v", core.Database)
	}
	if core.Database.VerifyCanonical || core.Database.TSOwnedSchema {
		t.Fatalf("database boolean settings not read: %#v", core.Database)
	}
}

func TestCoreReadsOptionalTLSSettings(t *testing.T) {
	store := &Store{raw: map[string]any{
		"server": map[string]any{
			"tls": map[string]any{
				"enabled":    true,
				"certFile":   "/tmp/cert.pem",
				"keyFile":    "/tmp/key.pem",
				"caFile":     "/tmp/ca.pem",
				"passphrase": "secret",
			},
		},
	}}
	core := store.Core()
	if !core.Server.TLS.Enabled || core.Server.TLS.CertFile != "/tmp/cert.pem" || core.Server.TLS.KeyFile != "/tmp/key.pem" || core.Server.TLS.CAFile != "/tmp/ca.pem" || core.Server.TLS.Passphrase != "secret" {
		t.Fatalf("unexpected TLS settings: %#v", core.Server.TLS)
	}
}

func TestLoadMigratesLegacySettingsAndPersistsResult(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("create settings dir: %v", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	legacy := []byte(`{
		"editor":{"legacyEncoding":true,"theme":"dark"},
		"agent":{
			"yoloSkipReadOnlyConfirmations":true,
			"smartInterruptionCheck":true,
			"defaultPermissionMode":"plan",
			"subagentAllowedModels":["kiro:model-a","openai:model-b"],
			"webFetchPolicy":{"proxy":{"mode":"custom","url":"web-proxy.example.test:8081"}},
			"unknownAgentField":"kept"
		},
		"openai":{
			"key":"sk-legacy",
			"baseURL":"https://legacy.example.test/v1",
			"modelPrefix":"legacy-model-prefix",
			"defaultModel":"gpt-legacy",
			"unknownOpenAI":"kept"
		},
		"customApiProviders":[{
			"id":"custom-1",
			"name":"Custom One",
			"apiKey":"sk-custom",
			"baseUrl":"https://custom.example.test/v1",
			"defaultModel":"model",
			"protocol":"responses-compatible",
			"proxyUrl":"provider-proxy.example.test:8082",
			"proxyURL":"provider-proxy.example.test:8083"
		}],
		"mcpServers":[{"defaultBehavior":"allow","toolPermissions":[{"toolName":"write","behavior":"allow"}],"unknownMcp":"kept"}],
		"codex":{"defaultReasoningEffort":null,"proxy":"codex-proxy.example.test:8084"},
		"kiro":{"proxy":"kiro-proxy.example.test:8085"},
		"unknownRoot":{"still":true}
	}`)
	if err := os.WriteFile(settingsPath, legacy, 0o600); err != nil {
		t.Fatalf("write legacy settings: %v", err)
	}

	store, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	raw := store.Raw()
	assertMigratedLegacySettings(t, raw)

	var persisted map[string]any
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read persisted settings: %v", err)
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("decode persisted settings: %v", err)
	}
	assertMigratedLegacySettings(t, persisted)
}

func TestLoadMigratesLegacyDangerReflectionEnabledFalse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("create settings dir: %v", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"agent":{"dangerReflectionEnabled":false}}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	store, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	agent, _ := store.Raw()["agent"].(map[string]any)
	if agent["dangerReflectionLevel"] != "off" || agent["dangerReflectionEnabled"] != false {
		t.Fatalf("legacy disabled danger reflection not migrated to off: %#v", agent)
	}
}

func TestLoadAddsMissingOpenAIProviderPrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("create settings dir: %v", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"openaiProviders":[{"id":"o1","name":"OpenAI","apiKey":"sk","baseUrl":"https://api.example.test","defaultModel":"gpt"}]}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	store, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	providers, _ := store.Raw()["openaiProviders"].([]any)
	provider, _ := providers[0].(map[string]any)
	if provider["prefix"] != "openai" {
		t.Fatalf("openai provider prefix = %#v, want openai", provider["prefix"])
	}
}

func TestLoadSyncsLegacyProviderArraysIntoCustomAPIProviders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("create settings dir: %v", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	legacy := []byte(`{
		"openaiProviders":[{"id":"o1","name":"Open","prefix":"legacy-open","apiKey":"sk-open","baseUrl":"https://open.example.test","defaultModel":"gpt-test","responsesApi":false,"codexAccountId":"acct_1","codexWebSocket":true}],
		"anthropicProviders":[{"id":"a1","name":"Anth","prefix":"legacy-anth","apiKey":"sk-anth","baseUrl":"https://anth.example.test","defaultModel":"claude-test","officialApi":true,"defaultReasoningEffort":"high","proxy":"anth-proxy.example.test:8080","tlsRejectUnauthorized":false}]
	}`)
	if err := os.WriteFile(settingsPath, legacy, 0o600); err != nil {
		t.Fatalf("write legacy settings: %v", err)
	}

	store, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	raw := store.Raw()
	customProviders, _ := raw["customApiProviders"].([]any)
	if len(customProviders) != 2 {
		t.Fatalf("expected two custom providers derived from legacy arrays, got %#v", customProviders)
	}
	openaiCustom, _ := customProviders[0].(map[string]any)
	if openaiCustom["id"] != "o1" || openaiCustom["protocol"] != "completions-compatible" || openaiCustom["codexAccountId"] != "acct_1" || openaiCustom["codexWebSocket"] != true || openaiCustom["codexWebSearch"] != true || openaiCustom["codexImageGeneration"] != true {
		t.Fatalf("unexpected OpenAI-derived custom provider: %#v", openaiCustom)
	}
	anthropicCustom, _ := customProviders[1].(map[string]any)
	if anthropicCustom["id"] != "a1" || anthropicCustom["protocol"] != "anthropic-official" || anthropicCustom["defaultReasoningEffort"] != "high" || anthropicCustom["proxy"] != "http://anth-proxy.example.test:8080" || anthropicCustom["tlsRejectUnauthorized"] != false {
		t.Fatalf("unexpected Anthropic-derived custom provider: %#v", anthropicCustom)
	}
	openaiProviders, _ := raw["openaiProviders"].([]any)
	openai, _ := openaiProviders[0].(map[string]any)
	if openai["id"] != "o1" || openai["apiMode"] != "completions" || openai["codexAccountId"] != "acct_1" || openai["codexWebSocket"] != true {
		t.Fatalf("legacy OpenAI array not synced from normalized custom provider: %#v", openai)
	}
}

func TestLoadMigratesLegacyCodexOAuthIntoCredentialPool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("create settings dir: %v", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	jwtAccessToken := unsignedTestJWT(map[string]any{
		"email": "codex-jwt@example.com",
		"sub":   "codex-jwt-sub",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "codex-jwt-account",
		},
	})
	jwtAccessTokenJSON, err := json.Marshal(jwtAccessToken)
	if err != nil {
		t.Fatalf("encode jwt access token: %v", err)
	}
	legacy := []byte(`{
		"openaiProviders":[
			{
				"id":"o1","name":"Open","prefix":"legacy-open","apiKey":"sk-open",
				"baseUrl":"https://open.example.test","defaultModel":"gpt-test",
				"codexOAuth":{"refreshToken":"refresh-1","accessToken":"access-1","expiresAt":1700000000000,"accountId":"account-1"},
				"codexProxy":"http://legacy-proxy.example.test"
			},
			{
				"id":"o2","name":"Open Duplicate","prefix":"legacy-open-2","apiKey":"sk-open-2",
				"baseUrl":"https://open2.example.test","defaultModel":"gpt-test-2",
				"codexOAuth":{"refreshToken":"refresh-1","accessToken":"access-duplicate","accountId":"account-duplicate"},
				"codexProxy":"http://legacy-proxy-duplicate.example.test"
			},
			{
				"id":"o3","name":"Open JWT","prefix":"legacy-open-3","apiKey":"sk-open-3",
				"baseUrl":"https://open3.example.test","defaultModel":"gpt-test-3",
				"codexOAuth":{"accessToken":` + string(jwtAccessTokenJSON) + `,"expiresAt":1700000000}
			}
		]
	}`)
	if err := os.WriteFile(settingsPath, legacy, 0o600); err != nil {
		t.Fatalf("write legacy settings: %v", err)
	}

	store, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	raw := store.Raw()
	openaiProviders, _ := raw["openaiProviders"].([]any)
	if len(openaiProviders) != 3 {
		t.Fatalf("expected three OpenAI providers after migration, got %#v", openaiProviders)
	}
	for _, item := range openaiProviders {
		provider, _ := item.(map[string]any)
		if _, exists := provider["codexOAuth"]; exists {
			t.Fatalf("legacy codexOAuth should be removed from provider: %#v", provider)
		}
		if _, exists := provider["codexProxy"]; exists {
			t.Fatalf("legacy codexProxy should be removed from provider: %#v", provider)
		}
	}
	customProviders, _ := raw["customApiProviders"].([]any)
	for _, item := range customProviders {
		provider, _ := item.(map[string]any)
		if _, exists := provider["codexOAuth"]; exists {
			t.Fatalf("legacy codexOAuth should not be copied into custom providers: %#v", provider)
		}
	}
	credentialsPath := filepath.Join(settingsDir, "codex-credentials.json")
	credentialsData, err := os.ReadFile(credentialsPath)
	if err != nil {
		t.Fatalf("read codex credentials: %v", err)
	}
	var credentials []map[string]any
	if err := json.Unmarshal(credentialsData, &credentials); err != nil {
		t.Fatalf("decode codex credentials: %v", err)
	}
	if len(credentials) != 2 {
		t.Fatalf("expected duplicate legacy credentials to import once plus JWT credential, got %#v", credentials)
	}
	credential := credentials[0]
	priority, priorityOK := numericValue(credential["priority"])
	if credential["refreshToken"] != "refresh-1" || credential["accessToken"] != "access-1" || credential["accountId"] != "account-1" || credential["displayName"] != "account-1" || credential["disabled"] != false || !priorityOK || priority != 0 {
		t.Fatalf("unexpected migrated codex credential: %#v", credential)
	}
	if _, ok := credential["id"].(string); !ok {
		t.Fatalf("migrated codex credential missing id: %#v", credential)
	}
	jwtCredential := credentials[1]
	jwtPriority, jwtPriorityOK := numericValue(jwtCredential["priority"])
	jwtExpiresAt, jwtExpiresAtOK := numericValue(jwtCredential["expiresAt"])
	if jwtCredential["accessToken"] != jwtAccessToken || jwtCredential["accountId"] != "codex-jwt-account" || jwtCredential["email"] != "codex-jwt@example.com" || jwtCredential["sub"] != "codex-jwt-sub" || jwtCredential["displayName"] != "codex-jwt@example.com" || !jwtPriorityOK || jwtPriority != 1 || !jwtExpiresAtOK || jwtExpiresAt != 1700000000000 {
		t.Fatalf("unexpected migrated JWT codex credential: %#v", jwtCredential)
	}

	var persisted map[string]any
	persistedData, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read persisted settings: %v", err)
	}
	if err := json.Unmarshal(persistedData, &persisted); err != nil {
		t.Fatalf("decode persisted settings: %v", err)
	}
	persistedOpenAIProviders, _ := persisted["openaiProviders"].([]any)
	for _, item := range persistedOpenAIProviders {
		provider, _ := item.(map[string]any)
		if _, exists := provider["codexOAuth"]; exists {
			t.Fatalf("persisted provider kept legacy codexOAuth: %#v", provider)
		}
		if _, exists := provider["codexProxy"]; exists {
			t.Fatalf("persisted provider kept legacy codexProxy: %#v", provider)
		}
	}
}

func unsignedTestJWT(claims map[string]any) string {
	header, _ := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func TestLoadPurgesStaleAgentModelReferences(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsDir := filepath.Join(home, ".narrafork")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("create settings dir: %v", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	legacy := []byte(`{
		"customApiProviders":[{"id":"keep","name":"Keep","prefix":"keepp","apiKey":"sk-keep","baseUrl":"https://keep.example.test","defaultModel":"keep-model","protocol":"responses-compatible"}],
		"agent":{
			"summaryModel":"oldp:model-a",
			"subagentModels":{"explore":"oldp:model-b","plan":"keepp:model-c"},
			"hiddenModels":["oldp:model-a","keepp:model-c","kiro:claude-sonnet-4.5"],
			"modelContextWindows":{"oldp:model-a":128000,"keepp:model-c":200000},
			"customModels":[{"value":"oldp:model-a","label":"Old"},{"value":"codex:gpt-5.3-codex","label":"Codex"}]
		}
	}`)
	if err := os.WriteFile(settingsPath, legacy, 0o600); err != nil {
		t.Fatalf("write legacy settings: %v", err)
	}

	store, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	agent, _ := store.Raw()["agent"].(map[string]any)
	if agent["summaryModel"] != "" {
		t.Fatalf("stale summary model not purged: %#v", agent["summaryModel"])
	}
	subagent, _ := agent["subagentModels"].(map[string]any)
	if subagent["explore"] != "" || subagent["plan"] != "keepp:model-c" {
		t.Fatalf("stale subagent model refs not purged selectively: %#v", subagent)
	}
	hidden, _ := agent["hiddenModels"].([]any)
	if len(hidden) != 2 || hidden[0] != "keepp:model-c" || hidden[1] != "kiro:claude-sonnet-4.5" {
		t.Fatalf("stale hidden models not purged: %#v", hidden)
	}
	windows, _ := agent["modelContextWindows"].(map[string]any)
	if _, exists := windows["oldp:model-a"]; exists || windows["keepp:model-c"] != float64(200000) {
		t.Fatalf("stale model context windows not purged: %#v", windows)
	}
	customModels, _ := agent["customModels"].([]any)
	customModel, _ := customModels[0].(map[string]any)
	if len(customModels) != 1 || customModel["value"] != "codex:gpt-5.3-codex" {
		t.Fatalf("stale custom models not purged: %#v", customModels)
	}
}

func assertMigratedLegacySettings(t *testing.T, raw map[string]any) {
	t.Helper()
	editor, _ := raw["editor"].(map[string]any)
	if _, exists := editor["legacyEncoding"]; exists || editor["theme"] != "dark" {
		t.Fatalf("editor legacyEncoding not migrated or unknown field lost: %#v", editor)
	}
	agent, _ := raw["agent"].(map[string]any)
	if agent["legacyEncoding"] != true || agent["dangerSkipReadOnlyConfirmations"] != true {
		t.Fatalf("agent legacy booleans not migrated: %#v", agent)
	}
	if agent["dangerReflectionLevel"] != "standard" || agent["dangerReflectionEnabled"] != true {
		t.Fatalf("danger reflection settings not normalized: %#v", agent)
	}
	if _, exists := agent["yoloSkipReadOnlyConfirmations"]; exists {
		t.Fatalf("legacy yolo setting should be removed: %#v", agent)
	}
	if _, exists := agent["smartInterruptionCheck"]; exists {
		t.Fatalf("smartInterruptionCheck should be removed: %#v", agent)
	}
	if agent["defaultPermissionMode"] != "default" || agent["defaultStartInPlanMode"] != true {
		t.Fatalf("plan permission mode not migrated: %#v", agent)
	}
	if agent["unknownAgentField"] != "kept" {
		t.Fatalf("unknown agent field lost: %#v", agent)
	}
	allowed, _ := agent["subagentAllowedModels"].(map[string]any)
	for _, role := range []string{"explore", "plan", "general"} {
		models, _ := allowed[role].([]any)
		if len(models) != 2 || models[0] != "kiro:model-a" || models[1] != "openai:model-b" {
			t.Fatalf("subagentAllowedModels[%s] not migrated: %#v", role, allowed)
		}
	}
	webFetchProxy, _ := nestedDefault(raw, []string{"agent", "webFetchPolicy", "proxy", "url"})
	if webFetchProxy != "http://web-proxy.example.test:8081" {
		t.Fatalf("web fetch proxy URL not normalized: %#v", webFetchProxy)
	}
	if _, exists := raw["openai"]; exists {
		t.Fatalf("legacy openai root should be removed: %#v", raw["openai"])
	}
	customProviders, _ := raw["customApiProviders"].([]any)
	custom, _ := customProviders[0].(map[string]any)
	if custom["id"] != "custom-1" || custom["prefix"] != "custom" || custom["protocol"] != "responses-compatible" || custom["proxyUrl"] != "http://provider-proxy.example.test:8082" || custom["proxyURL"] != "http://provider-proxy.example.test:8083" {
		t.Fatalf("custom provider prefix/proxy not normalized: %#v", custom)
	}
	if custom["codexAccountId"] != "" || custom["codexWebSocket"] != false || custom["codexWebSearch"] != true || custom["codexImageGeneration"] != true || custom["tlsRejectUnauthorized"] != true || custom["proxy"] != "" {
		t.Fatalf("custom provider TS defaults not materialized: %#v", custom)
	}
	openaiProviders, _ := raw["openaiProviders"].([]any)
	openai, _ := openaiProviders[0].(map[string]any)
	if openai["id"] != "custom-1" || openai["apiKey"] != "sk-custom" || openai["baseUrl"] != "https://custom.example.test/v1" || openai["prefix"] != "custom" || openai["apiMode"] != "responses" {
		t.Fatalf("legacy OpenAI-compatible array not derived from custom provider: %#v", openai)
	}
	mcpServers, _ := raw["mcpServers"].([]any)
	mcp, _ := mcpServers[0].(map[string]any)
	permissions, _ := mcp["toolPermissions"].([]any)
	permission, _ := permissions[0].(map[string]any)
	if mcp["defaultBehavior"] != "readWrite" || permission["behavior"] != "readWrite" || mcp["unknownMcp"] != "kept" {
		t.Fatalf("MCP behavior migration failed: %#v", mcp)
	}
	codex, _ := raw["codex"].(map[string]any)
	if codex["defaultReasoningEffort"] != "medium" || codex["proxy"] != "http://codex-proxy.example.test:8084" {
		t.Fatalf("codex settings not normalized: %#v", codex)
	}
	kiro, _ := raw["kiro"].(map[string]any)
	if kiro["proxy"] != "http://kiro-proxy.example.test:8085" {
		t.Fatalf("kiro proxy not normalized: %#v", kiro)
	}
	unknownRoot, _ := raw["unknownRoot"].(map[string]any)
	if unknownRoot["still"] != true {
		t.Fatalf("unknown root lost: %#v", raw["unknownRoot"])
	}
}

func expectDefaultValue(t *testing.T, raw map[string]any, path string, want any) {
	t.Helper()
	got, ok := nestedDefault(raw, splitPath(path))
	if !ok {
		t.Fatalf("defaultRaw missing %s", path)
	}
	if got != want {
		t.Fatalf("defaultRaw %s = %#v, want %#v", path, got, want)
	}
}

func expectNoDefaultValue(t *testing.T, raw map[string]any, path string) {
	t.Helper()
	if got, ok := nestedDefault(raw, splitPath(path)); ok {
		t.Fatalf("defaultRaw should not write non-TS default %s, got %#v", path, got)
	}
}

func nestedDefault(raw map[string]any, path []string) (any, bool) {
	var current any = raw
	for _, segment := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = m[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func splitPath(path string) []string {
	out := []string{}
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '.' {
			out = append(out, path[start:i])
			start = i + 1
		}
	}
	return out
}
