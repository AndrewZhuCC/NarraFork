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
	ExternalTools            []ExternalToolSpec
	MCPExternalToolInjection bool
}

type ToolCapability struct {
	Name      string         `json:"name"`
	Category  string         `json:"category"`
	Source    string         `json:"source,omitempty"`
	Supported bool           `json:"supported"`
	Enabled   bool           `json:"enabled"`
	Reason    string         `json:"reason,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type ToolInventoryMetadata struct {
	Scope        string           `json:"scope"`
	Tools        []ToolCapability `json:"tools"`
	Capabilities map[string]any   `json:"capabilities"`
}

var optionalToolNames = map[string]bool{
	"Terminal":  true,
	"ShareFile": true,
	"Browser":   true,
	"Recall":    true,
}

type unsupportedOptionalTool struct {
	Name   string
	Reason string
}

var unsupportedOptionalTools = []unsupportedOptionalTool{
	{
		Name:   "ForkNarrator",
		Reason: "Chapter and narrator fork APIs exist, but the ForkNarrator agent tool is not implemented in Go narrator sessions yet.",
	},
	{
		Name:   "NarraForkAdmin",
		Reason: "Admin routes exist, but the NarraForkAdmin agent tool is not implemented in Go narrator sessions yet.",
	},
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
	for _, spec := range append(toolSpecs(), externalToolSpecs(opts.ExternalTools)...) {
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

func externalToolSpecs(tools []ExternalToolSpec) []toolSpec {
	out := []toolSpec{}
	for _, tool := range normalizeExternalTools(tools) {
		description := tool.Description
		if description == "" {
			description = tool.Name
		}
		out = append(out, toolSpec{Name: tool.Name, Description: description, Parameters: tool.Parameters})
	}
	return out
}

func externalToolMetadata(tools []ExternalToolSpec, name string) map[string]any {
	for _, tool := range normalizeExternalTools(tools) {
		if tool.Name == name {
			return metadataWithCapabilityContract(cloneMetadata(tool.Metadata))
		}
	}
	return nil
}

func metadataWithCapabilityContract(metadata map[string]any) map[string]any {
	out := map[string]any{
		"schemaCompatible":  true,
		"runtimeCompatible": "full",
		"knownDegraded":     []string{},
		"fallbackBehavior":  "none",
		"frontendRendering": "tool_output_text",
	}
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func staticToolMetadata(name string) map[string]any {
	switch name {
	case "Read":
		return metadataWithCapabilityContract(map[string]any{
			"parity":              "partial",
			"schemaCompatible":    true,
			"runtimeCompatible":   "partial",
			"knownDegraded":       []string{"pdfPages", "imageContent"},
			"fallbackBehavior":    "explicit_error",
			"frontendRendering":   "text_or_directory_listing",
			"textFiles":           true,
			"directories":         true,
			"offsetLimit":         true,
			"pdfPages":            false,
			"pagesSchemaOnly":     true,
			"imageContent":        false,
			"binaryFileRejected":  true,
			"binaryRejectMessage": "binary file cannot be read as text",
			"reason":              "Go Read supports bounded text file and directory reads; TS PDF page extraction is schema-compatible but rejected at runtime until a safe PDF extraction runtime is implemented. Go additionally rejects files containing NUL bytes with an explicit 'binary file cannot be read as text' message (a defensive Go-only guard); TS has no binary detection and would return UTF-8-decoded text with replacement characters for such files.",
		})
	case "Bash":
		return metadataWithCapabilityContract(map[string]any{
			"runInBackground":           true,
			"bashBackground":            true,
			"narratorContextOnly":       true,
			"nonNarratorExecuteTool":    "foreground-only",
			"nonNarratorBackgroundNote": "Bash run_in_background requires narrator context; non-narrator ExecuteTool only supports foreground Bash commands.",
		})
	case "Await":
		return metadataWithCapabilityContract(map[string]any{
			"supportedTypes":            []string{"agent", "bash"},
			"awaitAgent":                true,
			"awaitBash":                 true,
			"awaitBashWaitForText":      true,
			"narratorContextOnly":       true,
			"nonNarratorExecuteTool":    false,
			"nonNarratorBackgroundNote": "Await is handled by narrator context for agent and bash background tasks.",
		})
	case "WebFetch":
		return metadataWithCapabilityContract(map[string]any{
			"parity":            "partial",
			"schemaCompatible":  true,
			"runtimeCompatible": "partial",
			"knownDegraded":     []string{"smart", "screenshot"},
			"fallbackBehavior":  "explicit_error/raw_text",
			"frontendRendering": "raw_text",
			"mode":              "readability+raw+dom-http",
			"defaultMode":       "readability",
			"supportedModes":    []string{"readability", "raw", "dom"},
			"unsupportedModes":  []string{"smart", "screenshot"},
			"readabilityMode":   "heuristic",
			"protocols":         []string{"http", "https", "data"},
			"policy":            "permission-policy-and-proxy-consumed",
			"reason":            "Go WebFetch supports readability (default; heuristic main-content extraction, not byte-identical to Mozilla Readability), raw HTTP/HTTPS/data fetches, and dom mode (no-browser HTTP fetch + HTML clean/selector); smart/screenshot are rejected explicitly instead of being silently ignored.",
		})
	case "WebSearch":
		return metadataWithCapabilityContract(map[string]any{
			"parity":            "partial",
			"schemaCompatible":  true,
			"runtimeCompatible": "partial",
			"knownDegraded":     []string{"provider_mcp_search"},
			"fallbackBehavior":  "standalone_duckduckgo",
			"frontendRendering": "tool_output_text",
			"searchBackend":     "duckduckgo-instant-answer",
			"providerMcpSearch": false,
			"reason":            "Go WebSearch uses a standalone DuckDuckGo Instant Answer query; TypeScript routes WebSearch through a provider-backed MCP search chain (kiro -> nug -> nkp). Results differ but the tool stays usable without configured providers.",
		})
	case "Grep":
		return metadataWithCapabilityContract(map[string]any{
			"parity":             "partial",
			"schemaCompatible":   true,
			"runtimeCompatible":  "partial",
			"knownDegraded":      []string{"pcre_regex", "gitignore_rules", "unbounded_head_limit", "truncation_suffix"},
			"fallbackBehavior":   "native_regexp_walkdir",
			"frontendRendering":  "tool_output_text",
			"searchBackend":      "go-regexp-walkdir",
			"regexEngine":        "re2",
			"outputModes":        []string{"content", "files_with_matches", "count"},
			"defaultHeadLimit":   200,
			"maxHeadLimit":       1000,
			"maxFileBytes":       1 * 1024 * 1024,
			"respectsGitignore":  false,
			"includesHidden":     true,
			"truncationSuffix":   false,
			"inaccessibleSuffix": false,
			"reason":             "Go Grep is a native regexp (RE2) + filepath.WalkDir fallback, not a ripgrep wrapper. Divergences from TS rg: RE2 lacks PCRE features (backreferences/lookaround); head_limit defaults to 200 and is capped at 1000 (a backend main-thread performance bound vs TS unbounded 0); files >1MB are skipped; no .gitignore/ignore-hierarchy is applied (WalkDir traverses everything); and TS's '(Results limited to N entries. M more available.)' / '(Some paths were inaccessible and skipped)' suffixes are not emitted. dotfiles ARE included, matching TS rg --hidden.",
		})
	case "EndPipeline":
		return metadataWithCapabilityContract(map[string]any{
			"parity":                "full",
			"schemaCompatible":      true,
			"runtimeCompatible":     "full",
			"knownDegraded":         []string{"sort_collation"},
			"fallbackBehavior":      "ts_rule_engine_port",
			"frontendRendering":     "tool_output_text",
			"supportedRuleStages":   []string{"from", "cat", "grep", "head", "tail", "sort", "uniq", "cut"},
			"unsupportedRuleStages": []string{},
			"resultEnvelope":        "pipeline-result-sections",
			"quoteAwareParser":      true,
			"sortCollation":         "code-point",
			"reason":                "Go StartPipeline/EndPipeline now port the TS pipeline-rules engine (server/lib/agent/pipeline-rules.ts): a quote-aware stage tokenizer plus from/cat/grep (-i/-v + regex)/head/tail/sort (-r)/uniq/cut (-d/-f with ranges), and the 'Pipeline result / Aliases used / Captured / Rule' sections envelope (with plain format), matching TS clipText/clipFinal UTF-16 truncation and the formatCapturedPreview capture block. Rule errors surface as 'Pipeline rule error: <message>'. The sole minor divergence: sort uses Go Unicode code-point order rather than TS localeCompare ICU collation (no Go stdlib collator); identical for same-case ASCII, may differ for mixed-case/non-ASCII.",
		})
	case "Recall":
		return metadataWithCapabilityContract(map[string]any{
			"supported":              true,
			"readOnly":               true,
			"scope":                  "self",
			"crossNarrator":          false,
			"searchMode":             "bounded-like",
			"maxSearchLimit":         50,
			"maxReadLimit":           50,
			"maxBatchQueries":        10,
			"maxToolCallOutputChars": 4000,
			"nonNarratorExecuteTool": false,
			"reason":                 "Go Recall implements a safe self-scoped/read-only subset for the current narrator only, using bounded LIKE search without FTS.",
		})
	case "Browser":
		return metadataWithCapabilityContract(map[string]any{
			"parity":                "partial",
			"schemaCompatible":      true,
			"runtimeCompatible":     "partial",
			"knownDegraded":         []string{"imageContentBlock", "sharePreview"},
			"fallbackBehavior":      "file_or_base64_artifact",
			"frontendRendering":     "file_or_base64_artifact",
			"runtime":               "go-rod",
			"screenshotPreviewMode": "inline-base64-or-file-artifact",
			"sharePreview":          false,
			"imageContentBlock":     false,
			"fileOutput":            true,
			"traceFormat":           "chrome-devtools-json",
			"traceShare":            true,
			"traceFileOutput":       true,
			"traceShareUrl":         "/api/shares/{shareId}",
			"reason":                "Go Browser screenshots return JSON/base64 or an explicit file_path artifact; TS additionally returns an image content block and temporary share preview metadata. Browser perf_stop writes Chrome trace JSON and creates a functional share URL when file_path is omitted.",
		})
	default:
		return metadataWithCapabilityContract(nil)
	}
}

func unsupportedOptionalToolCapabilityMetadata() map[string]any {
	return map[string]any{
		"schemaCompatible":  false,
		"runtimeCompatible": "unsupported",
		"knownDegraded":     []string{"optional_unsupported"},
		"fallbackBehavior":  "hidden_or_explicit_error",
		"frontendRendering": "hidden",
	}
}

func unsupportedOptionalToolMetadata() []map[string]any {
	items := make([]map[string]any, 0, len(unsupportedOptionalTools))
	for _, tool := range unsupportedOptionalTools {
		items = append(items, map[string]any{
			"name":      tool.Name,
			"category":  ToolCategoryOptional,
			"supported": false,
			"enabled":   false,
			"reason":    tool.Reason,
			"metadata":  unsupportedOptionalToolCapabilityMetadata(),
		})
	}
	return items
}

func ToolInventoryMetadataForOptions(opts ToolInventoryOptions) ToolInventoryMetadata {
	scope := normalizeToolScopeName(opts.Scope)
	external := normalizeExternalTools(opts.ExternalTools)
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
			Metadata:  staticToolMetadata(spec.Name),
		})
	}
	if scope != ToolScopeReflection {
		for _, tool := range unsupportedOptionalTools {
			capabilities = append(capabilities, ToolCapability{
				Name:      tool.Name,
				Category:  ToolCategoryOptional,
				Source:    "static",
				Supported: false,
				Enabled:   false,
				Reason:    tool.Reason,
				Metadata:  unsupportedOptionalToolCapabilityMetadata(),
			})
		}
	}
	for _, tool := range external {
		capabilities = append(capabilities, ToolCapability{
			Name:      tool.Name,
			Category:  ToolCategoryMCP,
			Source:    firstNonEmpty(tool.Source, "mcp"),
			Supported: true,
			Enabled:   enabled[tool.Name],
			Metadata:  metadataWithCapabilityContract(cloneMetadata(tool.Metadata)),
		})
	}
	supportedExternal := opts.MCPExternalToolInjection || len(external) > 0
	reason := "stdio MCP external tools can be injected into Go narrator agent loops when servers are connected"
	if len(external) == 0 {
		reason = "no connected stdio MCP tools are currently injected"
	}
	if !supportedExternal {
		reason = "external MCP tool injection is not configured for this Go narrator runtime"
		capabilities = append(capabilities, ToolCapability{
			Name:      "mcp__external_tools",
			Category:  ToolCategoryMCP,
			Source:    "mcp",
			Supported: false,
			Enabled:   false,
			Reason:    reason,
			Metadata: metadataWithCapabilityContract(map[string]any{
				"schemaCompatible":  false,
				"runtimeCompatible": "unsupported",
				"knownDegraded":     []string{"mcp_external_tools_unavailable"},
				"fallbackBehavior":  "explicit_error",
			}),
		})
	}
	return ToolInventoryMetadata{
		Scope: scope,
		Tools: capabilities,
		Capabilities: map[string]any{
			"externalMCPToolInjection": map[string]any{
				"supported": supportedExternal,
				"reason":    reason,
				"toolCount": len(external),
			},
			"unsupportedOptionalTools": unsupportedOptionalToolMetadata(),
		},
	}
}

func ToolMenuCapabilities() []map[string]any {
	return ToolMenuCapabilitiesForOptions(ToolInventoryOptions{})
}

func ToolMenuCapabilitiesForOptions(opts ToolInventoryOptions) []map[string]any {
	items := []map[string]any{}
	seen := map[string]bool{}
	for _, spec := range append(toolSpecs(), externalToolSpecs(opts.ExternalTools)...) {
		category := toolCategory(spec)
		if category == ToolCategoryReflection || seen[spec.Name] {
			continue
		}
		seen[spec.Name] = true
		item := map[string]any{
			"name":        spec.Name,
			"description": spec.Description,
			"category":    category,
			"supported":   true,
		}
		if metadata := staticToolMetadata(spec.Name); metadata != nil {
			item["metadata"] = metadata
		}
		if category == ToolCategoryMCP {
			item["source"] = "mcp"
			if metadata := externalToolMetadata(opts.ExternalTools, spec.Name); metadata != nil {
				item["metadata"] = metadata
			}
		}
		items = append(items, item)
	}
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
