package config

import (
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
	openaiProviders, _ := raw["openaiProviders"].([]any)
	openai, _ := openaiProviders[0].(map[string]any)
	if openai["apiKey"] != "sk-legacy" || openai["key"] != "sk-legacy" || openai["baseUrl"] != "https://legacy.example.test/v1" || openai["baseURL"] != "https://legacy.example.test/v1" || openai["prefix"] != "openai" || openai["modelPrefix"] != "legacy-model-prefix" || openai["unknownOpenAI"] != "kept" {
		t.Fatalf("legacy openai provider fields not preserved: %#v", openai)
	}
	customProviders, _ := raw["customApiProviders"].([]any)
	custom, _ := customProviders[0].(map[string]any)
	if custom["prefix"] != "custom" || custom["proxyUrl"] != "http://provider-proxy.example.test:8082" || custom["proxyURL"] != "http://provider-proxy.example.test:8083" {
		t.Fatalf("custom provider prefix/proxy not normalized: %#v", custom)
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
