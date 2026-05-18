package agentbasic

import (
	"sort"
	"strings"
)

const (
	ToolCategoryCore       = "core"
	ToolCategoryOptional   = "optional"
	ToolCategoryReview     = "review"
	ToolCategoryReflection = "reflection"
	ToolCategoryMCP        = "mcp"

	ToolScopeNormal     = "normal"
	ToolScopeReview     = "review"
	ToolScopeReflection = "reflection"
)

type ToolInventoryOptions struct {
	Scope                    string
	EnabledTools             []string
	DisabledTools            []string
	MCPExternalToolInjection bool
}

type ToolCapability struct {
	Name      string `json:"name"`
	Category  string `json:"category"`
	Source    string `json:"source,omitempty"`
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Reason    string `json:"reason,omitempty"`
}

type ToolInventoryMetadata struct {
	Scope        string           `json:"scope"`
	Tools        []ToolCapability `json:"tools"`
	Capabilities map[string]any   `json:"capabilities"`
}

var optionalToolNames = map[string]bool{
	"ShareFile": true,
	"Browser":   true,
}

var reviewToolNames = map[string]bool{
	"ConcludeReview": true,
}

func WithEnabledTools(tools []string) GenerateOption {
	return func(opts *generateOptions) {
		opts.enabledTools = normalizedToolNames(tools)
	}
}

func WithDisabledTools(tools []string) GenerateOption {
	return func(opts *generateOptions) {
		opts.disabledTools = normalizedToolNames(tools)
	}
}

func WithToolInventoryEvent(enabled bool) GenerateOption {
	return func(opts *generateOptions) {
		opts.emitToolInventory = enabled
	}
}

func encodedToolScope(base string, enabledTools []string, disabledTools []string) string {
	parts := []string{normalizeToolScopeName(base)}
	if len(enabledTools) > 0 {
		parts = append(parts, "enabled="+strings.Join(normalizedToolNames(enabledTools), ","))
	}
	if len(disabledTools) > 0 {
		parts = append(parts, "disabled="+strings.Join(normalizedToolNames(disabledTools), ","))
	}
	return strings.Join(parts, "|")
}

func toolInventoryOptionsFromScope(scope string) ToolInventoryOptions {
	parts := strings.Split(scope, "|")
	opts := ToolInventoryOptions{Scope: normalizeToolScopeName(firstToolScopePart(parts))}
	for _, part := range parts[1:] {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "enabled":
			opts.EnabledTools = splitToolNames(value)
		case "disabled":
			opts.DisabledTools = splitToolNames(value)
		}
	}
	return opts
}

func firstToolScopePart(parts []string) string {
	if len(parts) == 0 {
		return ToolScopeNormal
	}
	return parts[0]
}

func normalizeToolScopeName(scope string) string {
	switch strings.TrimSpace(scope) {
	case ToolScopeReview:
		return ToolScopeReview
	case ToolScopeReflection:
		return ToolScopeReflection
	default:
		return ToolScopeNormal
	}
}

func splitToolNames(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return normalizedToolNames(strings.Split(value, ","))
}

func normalizedToolNames(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		name := strings.TrimSpace(value)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func toolCategory(spec toolSpec) string {
	if spec.Visibility == ToolScopeReflection {
		return ToolCategoryReflection
	}
	if optionalToolNames[spec.Name] {
		return ToolCategoryOptional
	}
	if reviewToolNames[spec.Name] || spec.Visibility == ToolScopeReview {
		return ToolCategoryReview
	}
	if strings.HasPrefix(spec.Name, "mcp__") {
		return ToolCategoryMCP
	}
	return ToolCategoryCore
}

func resolveToolSpecs(opts ToolInventoryOptions) []toolSpec {
	scope := normalizeToolScopeName(opts.Scope)
	enabled := toolNameSet(opts.EnabledTools)
	disabled := toolNameSet(opts.DisabledTools)
	out := []toolSpec{}
	for _, spec := range toolSpecs() {
		category := toolCategory(spec)
		if disabled[spec.Name] {
			continue
		}
		switch scope {
		case ToolScopeReflection:
			if category != ToolCategoryReflection {
				continue
			}
		case ToolScopeReview:
			if category == ToolCategoryReflection {
				continue
			}
			if category == ToolCategoryOptional && !enabled[spec.Name] {
				continue
			}
		default:
			if category == ToolCategoryReflection || category == ToolCategoryReview {
				continue
			}
			if category == ToolCategoryOptional && !enabled[spec.Name] {
				continue
			}
		}
		out = append(out, spec)
	}
	return out
}

func toolNameSet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, value := range normalizedToolNames(values) {
		set[value] = true
	}
	return set
}

func ToolInventoryMetadataForOptions(opts ToolInventoryOptions) ToolInventoryMetadata {
	scope := normalizeToolScopeName(opts.Scope)
	enabled := map[string]bool{}
	for _, spec := range resolveToolSpecs(opts) {
		enabled[spec.Name] = true
	}
	capabilities := []ToolCapability{}
	for _, spec := range toolSpecs() {
		category := toolCategory(spec)
		if category == ToolCategoryReflection && scope != ToolScopeReflection {
			continue
		}
		capabilities = append(capabilities, ToolCapability{
			Name:      spec.Name,
			Category:  category,
			Source:    "static",
			Supported: true,
			Enabled:   enabled[spec.Name],
		})
	}
	capabilities = append(capabilities, ToolCapability{
		Name:      "mcp__external_tools",
		Category:  ToolCategoryMCP,
		Source:    "mcp",
		Supported: opts.MCPExternalToolInjection,
		Enabled:   false,
		Reason:    "external MCP tools are discoverable through /api/mcp/tools but are not injected into Go narrator agent runtime yet",
	})
	return ToolInventoryMetadata{
		Scope: scope,
		Tools: capabilities,
		Capabilities: map[string]any{
			"externalMCPToolInjection": map[string]any{
				"supported": opts.MCPExternalToolInjection,
				"reason":    "Go compatibility backend does not execute external MCP tools inside narrator agent loops yet",
			},
		},
	}
}

func ToolMenuCapabilities() []map[string]any {
	items := []map[string]any{}
	seen := map[string]bool{}
	for _, spec := range toolSpecs() {
		category := toolCategory(spec)
		if category == ToolCategoryReflection || seen[spec.Name] {
			continue
		}
		seen[spec.Name] = true
		items = append(items, map[string]any{
			"name":        spec.Name,
			"description": spec.Description,
			"category":    category,
			"supported":   true,
		})
	}
	items = append(items, map[string]any{
		"name":        "mcp__external_tools",
		"description": "External MCP tools discovered from configured MCP servers.",
		"category":    ToolCategoryMCP,
		"supported":   false,
		"reason":      "Go narrator runtime does not inject or execute external MCP tools yet",
	})
	sort.Slice(items, func(i, j int) bool {
		ci := strings.TrimSpace(stringValue(items[i]["category"]))
		cj := strings.TrimSpace(stringValue(items[j]["category"]))
		if ci != cj {
			return ci < cj
		}
		return strings.TrimSpace(stringValue(items[i]["name"])) < strings.TrimSpace(stringValue(items[j]["name"]))
	})
	return items
}
