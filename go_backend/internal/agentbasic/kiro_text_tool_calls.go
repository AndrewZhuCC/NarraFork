package agentbasic

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"narrafork-go/internal/id"
)

// This file ports the TypeScript Kiro leaked-XML tool-call extraction
// (server/lib/agent/kiro-protocol.ts:534-717). Kiro/NKP models sometimes emit a
// tool call as XML inside assistantResponseEvent.content
// (`<invoke name="Edit"><parameter name="...">...</parameter></invoke>`) instead of
// the native toolUse/toolUses fields. Without extraction, Go would surface the raw
// XML as visible assistant text and never execute the tool. parseKiroEvent enables
// this only when tools are present (mirroring TS parseTextToolCalls gating), so
// title/summary generation keeps text verbatim.

var (
	kiroInvokeRe    = regexp.MustCompile(`(?is)<invoke\b([^>]*)>([\s\S]*?)</invoke>`)
	kiroParameterRe = regexp.MustCompile(`(?is)<parameter\b([^>]*)>([\s\S]*?)</parameter>`)
	kiroXMLEntityRe = regexp.MustCompile(`&(#x?[0-9a-fA-F]+|[a-zA-Z]+);`)
	// Precompiled matchers for the only attribute name the extractor ever reads
	// ("name", from both <invoke name=...> and <parameter name=...>). RE2 has no
	// backreferences, so the TS single regex with a (["']) backref is split into
	// double- and single-quote matchers. These are immutable and shared safely
	// across the per-session streaming goroutines (a previous lazy map cache was
	// written without a lock and could trigger a fatal "concurrent map writes").
	kiroNameAttrDoubleRe  = regexp.MustCompile(`(?is)\bname\s*=\s*"([\s\S]*?)"`)
	kiroNameAttrSingleRe  = regexp.MustCompile(`(?is)\bname\s*=\s*'([\s\S]*?)'`)
	kiroMalformedMarkerRe = regexp.MustCompile(`(?s)(\r?\n[ \t]*\r?\n[ \t]*)([^\r\n]*?)((?:\r?\n[ \t]*)*)$`)
)

const kiroMaxMalformedMarkerLength = 10

var kiroMalformedInvokeMarkers = map[string]bool{"call": true, "count": true}

var kiroXMLBooleanParameterNames = map[string]bool{
	"replace_all": true, "run_in_background": true, "strict_serial": true,
	"compress": true, "preview": true, "await": true, "doInterrupt": true, "multiSelect": true,
}

var kiroXMLNumberParameterNames = map[string]bool{
	"timeout": true, "limit": true, "offset": true, "head_limit": true,
	"maxChars": true, "max_length": true, "pageno": true, "duration": true, "count": true,
}

var kiroXMLJSONParameterNames = map[string]bool{
	"ids": true, "names": true, "paths": true, "todos": true, "questions": true,
	"answers": true, "annotations": true, "metadata": true, "headers": true,
	"options": true, "tool_uses": true,
}

// kiroDecodeXMLEntities mirrors TS decodeXmlEntities: named entities plus numeric
// (&#NNN;) and hex (&#xNN;) code points; invalid/out-of-range entities are kept.
func kiroDecodeXMLEntities(value string) string {
	return kiroXMLEntityRe.ReplaceAllStringFunc(value, func(entity string) string {
		body := strings.ToLower(entity[1 : len(entity)-1])
		switch body {
		case "amp":
			return "&"
		case "lt":
			return "<"
		case "gt":
			return ">"
		case "quot":
			return "\""
		case "apos":
			return "'"
		}
		if strings.HasPrefix(body, "#x") {
			if code, err := strconv.ParseInt(body[2:], 16, 64); err == nil && code >= 0 && code <= 0x10ffff {
				return string(rune(code))
			}
			return entity
		}
		if strings.HasPrefix(body, "#") {
			if code, err := strconv.ParseInt(body[1:], 10, 64); err == nil && code >= 0 && code <= 0x10ffff {
				return string(rune(code))
			}
			return entity
		}
		return entity
	})
}

// kiroReadXMLAttribute reads an XML attribute value, decoding entities. RE2 has no
// backreferences, so the TS single regex with a (["']) backref is split into
// double- and single-quote matchers; the leftmost match wins (mirrors a single
// regex). Returns ("", false) when the attribute is absent.
//
// The only attribute the extractor ever reads is "name", served by immutable
// precompiled matchers shared safely across goroutines. Any other name (not
// currently reached) compiles ad-hoc matchers on the stack — no shared mutable
// cache, so the function is concurrency-safe.
func kiroReadXMLAttribute(attrs, name string) (string, bool) {
	var dbl, sgl *regexp.Regexp
	if name == "name" {
		dbl, sgl = kiroNameAttrDoubleRe, kiroNameAttrSingleRe
	} else {
		quoted := regexp.QuoteMeta(name)
		dbl = regexp.MustCompile(`(?is)\b` + quoted + `\s*=\s*"([\s\S]*?)"`)
		sgl = regexp.MustCompile(`(?is)\b` + quoted + `\s*=\s*'([\s\S]*?)'`)
	}
	dLoc := dbl.FindStringSubmatchIndex(attrs)
	sLoc := sgl.FindStringSubmatchIndex(attrs)
	switch {
	case dLoc == nil && sLoc == nil:
		return "", false
	case sLoc == nil || (dLoc != nil && dLoc[0] <= sLoc[0]):
		return kiroDecodeXMLEntities(attrs[dLoc[2]:dLoc[3]]), true
	default:
		return kiroDecodeXMLEntities(attrs[sLoc[2]:sLoc[3]]), true
	}
}

// kiroStripMalformedInvokeMarker removes a short standalone marker line (e.g.
// "call"/"count") that Kiro sometimes prefixes before leaked XML, keeping the
// blank-line separator. Mirrors TS stripMalformedInvokeMarker.
func kiroStripMalformedInvokeMarker(text string) string {
	loc := kiroMalformedMarkerRe.FindStringSubmatchIndex(text)
	if loc == nil {
		return text
	}
	blankSeparator := text[loc[2]:loc[3]]
	marker := text[loc[4]:loc[5]]
	trimmed := strings.TrimSpace(marker)
	normalized := strings.ToLower(trimmed)
	isKnownMarker := kiroMalformedInvokeMarkers[normalized]
	if len(trimmed) == 0 || (len([]rune(trimmed)) > kiroMaxMalformedMarkerLength && !isKnownMarker) {
		return text
	}
	// Replace the whole trailing match (blankSeparator + marker + trailing newlines)
	// with just the blank separator.
	return text[:loc[0]] + blankSeparator
}

// kiroCoerceXMLParameter coerces a leaked XML parameter string to a typed value
// for known boolean/number/JSON parameter names (mirrors TS coerceKiroXmlParameter).
func kiroCoerceXMLParameter(name, value string) any {
	trimmed := strings.TrimSpace(value)
	if kiroXMLBooleanParameterNames[name] {
		switch strings.ToLower(trimmed) {
		case "true":
			return true
		case "false":
			return false
		}
	}
	if kiroXMLNumberParameterNames[name] {
		if kiroXMLNumberPattern.MatchString(trimmed) {
			if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
				return parsed
			}
		}
	}
	if kiroXMLJSONParameterNames[name] && (strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{")) {
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
			return decoded
		}
	}
	return value
}

var kiroXMLNumberPattern = regexp.MustCompile(`^-?(?:\d+|\d*\.\d+)$`)

// kiroParseTextToolCalls extracts leaked <invoke> tool calls from text, returning
// the XML-stripped text and the extracted tool calls (mirrors TS
// parseKiroTextToolCalls). When no invoke block is found, returns the text
// unchanged and a nil slice.
func kiroParseTextToolCalls(text string) (string, []ToolCall) {
	matches := kiroInvokeRe.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, nil
	}
	var rewritten strings.Builder
	cursor := 0
	toolUses := []ToolCall{}
	for _, m := range matches {
		matchStart, matchEnd := m[0], m[1]
		attrs := text[m[2]:m[3]]
		body := text[m[4]:m[5]]
		name, ok := kiroReadXMLAttribute(attrs, "name")
		if !ok || name == "" {
			continue
		}
		input := map[string]any{}
		for _, pm := range kiroParameterRe.FindAllStringSubmatchIndex(body, -1) {
			paramAttrs := body[pm[2]:pm[3]]
			paramValue := body[pm[4]:pm[5]]
			paramName, pok := kiroReadXMLAttribute(paramAttrs, "name")
			if !pok || paramName == "" {
				continue
			}
			input[paramName] = kiroCoerceXMLParameter(paramName, kiroDecodeXMLEntities(paramValue))
		}
		rewritten.WriteString(kiroStripMalformedInvokeMarker(text[cursor:matchStart]))
		args, err := json.Marshal(input)
		if err != nil {
			args = []byte("{}")
		}
		toolUses = append(toolUses, newFunctionToolCall("kiro_xml_"+id.Must(12), name, string(args)))
		cursor = matchEnd
	}
	if len(toolUses) == 0 {
		return text, nil
	}
	rewritten.WriteString(text[cursor:])
	return rewritten.String(), toolUses
}

// kiroStableToolInput returns a deterministic serialization of a tool call's
// JSON arguments (recursively sorting object keys) for duplicate detection,
// mirroring TS stableJson.
func kiroStableToolInput(argsJSON string) string {
	var decoded any
	if err := json.Unmarshal([]byte(argsJSON), &decoded); err != nil {
		return argsJSON
	}
	return kiroStableJSON(decoded)
}

func kiroStableJSON(value any) string {
	switch v := value.(type) {
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = kiroStableJSON(item)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(v))
		for _, k := range keys {
			keyJSON, _ := json.Marshal(k)
			parts = append(parts, string(keyJSON)+":"+kiroStableJSON(v[k]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		out, _ := json.Marshal(v)
		return string(out)
	}
}

// kiroAppendDedupedToolUses appends tool uses not already present (by name + stable
// input) to existing, mirroring TS appendDedupedToolUses.
func kiroAppendDedupedToolUses(existing []ToolCall, add []ToolCall) []ToolCall {
	if len(add) == 0 {
		return existing
	}
	next := append([]ToolCall(nil), existing...)
	for _, candidate := range add {
		dup := false
		candKey := candidate.Function.Name + "\x00" + kiroStableToolInput(candidate.Function.Arguments)
		for _, item := range next {
			if item.Function.Name+"\x00"+kiroStableToolInput(item.Function.Arguments) == candKey {
				dup = true
				break
			}
		}
		if !dup {
			next = append(next, candidate)
		}
	}
	return next
}

// kiroRequestHasTools reports whether a built Kiro chat request body declares tools
// (conversationState.currentMessage.userInputMessage.userInputMessageContext.tools),
// used by the plugin-bridge path to gate leaked-XML parsing like TS
// parseTextToolCalls: params.tools.length > 0.
func kiroRequestHasTools(request map[string]any) bool {
	state, _ := request["conversationState"].(map[string]any)
	if state == nil {
		return false
	}
	current, _ := state["currentMessage"].(map[string]any)
	if current == nil {
		return false
	}
	user, _ := current["userInputMessage"].(map[string]any)
	if user == nil {
		return false
	}
	ctx, _ := user["userInputMessageContext"].(map[string]any)
	if ctx == nil {
		return false
	}
	return len(readArray(ctx["tools"])) > 0
}
