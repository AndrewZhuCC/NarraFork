package config

import "testing"

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
