package agentbasic

import (
	"sort"
	"strings"

	"narrafork-go/internal/config"
)

// defaultModelContextWindow is the fallback context window (tokens) used when a
// model is not found in user overrides, provider config, or the built-in table.
// Mirrors the TS getModelContextWindow `?? 128_000` default.
const defaultModelContextWindow = 128000

// builtinModelContextWindows mirrors the TS BUILTIN_CONTEXT_WINDOWS table
// (server/lib/settings/provider.ts). Codex entries use their contextLength.
var builtinModelContextWindows = map[string]int{
	// OpenAI models
	"gpt-4o":        128000,
	"gpt-4o-mini":   128000,
	"gpt-4-turbo":   128000,
	"gpt-4":         8192,
	"gpt-3.5-turbo": 16385,
	"o1":            200000,
	"o1-mini":       128000,
	"o3-mini":       200000,
	// Codex models (contextLength)
	"gpt-5-codex":         256000,
	"gpt-5.1-codex":       272000,
	"gpt-5.1-codex-max":   272000,
	"gpt-5.1-codex-mini":  272000,
	"gpt-5.2-codex":       272000,
	"gpt-5.2":             272000,
	"gpt-5.5":             272000,
	"gpt-5.4":             272000,
	"gpt-5.4-mini":        400000,
	"gpt-5.3-codex-spark": 128000,
	"gpt-5.3-codex":       272000,
	// Common third-party models
	"deepseek-chat":     64000,
	"deepseek-reasoner": 64000,
	// Claude models (via OpenAI-compatible gateways)
	"claude-3-5-sonnet": 200000,
	"claude-3-opus":     200000,
	"claude-sonnet-4":   200000,
	"claude-opus-4":     200000,
	// Claude 4.6 models — 1M context
	"claude-sonnet-4-6": 1000000,
	"claude-opus-4-6":   1000000,
	"claude-sonnet-4.6": 1000000,
	"claude-opus-4.6":   1000000,
	// Claude 4.5 models (Kiro exposes these; 200k context)
	"claude-sonnet-4-5": 200000,
	"claude-sonnet-4.5": 200000,
	"claude-haiku-4-5":  200000,
	"claude-haiku-4.5":  200000,
	"claude-opus-4-5":   200000,
	"claude-opus-4.5":   200000,
	// Anthropic native API models
	"claude-sonnet-4-20250514":   200000,
	"claude-opus-4-20250514":     200000,
	"claude-haiku-4-20250414":    200000,
	"claude-3-5-sonnet-20241022": 200000,
	"claude-3-5-haiku-20241022":  200000,
	"claude-3-opus-20240229":     200000,
}

// builtinModelContextSortedPatterns caches the table keys sorted by descending
// length for longest-prefix fuzzy matching (mirrors the TS sort).
var builtinModelContextSortedPatterns = func() []string {
	patterns := make([]string, 0, len(builtinModelContextWindows))
	for pattern := range builtinModelContextWindows {
		patterns = append(patterns, pattern)
	}
	sort.Slice(patterns, func(i, j int) bool { return len(patterns[i]) > len(patterns[j]) })
	return patterns
}()

// builtinModelContextWindow resolves a model's context window from the built-in
// table by exact match then longest-prefix fuzzy match, mirroring the TS
// getBuiltinModelContextWindow. Returns (window, true) on a hit.
func builtinModelContextWindow(model string) (int, bool) {
	bare := bareModelID(model)
	if window, ok := builtinModelContextWindows[bare]; ok {
		return window, true
	}
	normalized := strings.ToLower(bare)
	for _, pattern := range builtinModelContextSortedPatterns {
		if strings.HasPrefix(normalized, pattern) {
			return builtinModelContextWindows[pattern], true
		}
	}
	return 0, false
}

// bareModelID strips a "provider:" prefix from a model reference.
func bareModelID(model string) string {
	if _, bare := splitModel(model); bare != "" {
		return bare
	}
	return model
}

// resolveModelContextWindow resolves the effective context window (tokens) for a
// model, mirroring the TS getModelContextWindow precedence: per-model user
// override (agent.modelContextWindows) > provider config defaultContextWindow >
// built-in table > defaultModelContextWindow.
func resolveModelContextWindow(settings *config.Store, model, providerPrefix string) int {
	bare := bareModelID(model)
	fullModelValue := bare
	if providerPrefix != "" {
		fullModelValue = providerPrefix + ":" + bare
	}

	if settings != nil {
		raw := settings.Raw()
		agent, _ := raw["agent"].(map[string]any)
		if overrides, ok := agent["modelContextWindows"].(map[string]any); ok && len(overrides) > 0 {
			if window := intValue(overrides[fullModelValue], 0); window > 0 {
				return window
			}
			if model != fullModelValue {
				if window := intValue(overrides[model], 0); window > 0 {
					return window
				}
			}
		}
		// Provider config defaultContextWindow (non-kiro), mirroring TS.
		if providerPrefix != "" && providerPrefix != "kiro" {
			if window := providerDefaultContextWindow(raw, providerPrefix); window > 0 {
				return window
			}
		}
	}

	if window, ok := builtinModelContextWindow(model); ok {
		return window
	}
	return defaultModelContextWindow
}

// providerDefaultContextWindow looks up a configured provider's
// defaultContextWindow by matching prefix across the OpenAI/Anthropic provider
// arrays (mirrors TS getOpenaiProviderConfig/getAnthropicProviderConfig
// defaultContextWindow lookup).
func providerDefaultContextWindow(raw map[string]any, providerPrefix string) int {
	for _, key := range []string{"openaiProviders", "anthropicProviders", "customApiProviders"} {
		for _, item := range readArray(raw[key]) {
			provider, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if strings.TrimSpace(stringValue(provider["prefix"])) != providerPrefix {
				continue
			}
			if window := intValue(provider["defaultContextWindow"], 0); window > 0 {
				return window
			}
		}
	}
	return 0
}
