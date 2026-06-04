package agentbasic

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type toolFieldConfig struct {
	Short []string
	Large []string
}

type extractedJSONFieldsResult struct {
	Fields      map[string]string
	ActiveName  string
	ActiveStart int
}

type decodedJSONStringFragment struct {
	Text     string
	Consumed int
}

var toolFieldConfigs = map[string]toolFieldConfig{
	"Write":               {Short: []string{"file_path"}, Large: []string{"content"}},
	"Edit":                {Short: []string{"file_path", "replace_all"}, Large: []string{"old_string", "new_string"}},
	"Bash":                {Short: []string{"description"}, Large: []string{"command"}},
	"Grep":                {Short: []string{"pattern", "path", "glob", "output_mode", "type"}},
	"Glob":                {Short: []string{"pattern", "path"}},
	"Read":                {Short: []string{"file_path", "offset", "limit"}},
	"Agent":               {Short: []string{"description", "subagent_type", "model", "reasoning_effort"}, Large: []string{"prompt"}},
	"Task":                {Short: []string{"description", "subagent_type", "model", "reasoning_effort"}, Large: []string{"prompt"}},
	"Await":               {Short: []string{"type", "id", "timeout", "wait_for_text"}},
	"Send":                {Short: []string{"id", "ids", "name", "names", "doInterrupt", "await", "timeout"}, Large: []string{"message"}},
	"WebSearch":           {Short: []string{"query"}},
	"WebFetch":            {Short: []string{"url", "mode"}},
	"Skill":               {Short: []string{"skill"}},
	"ExitPlanMode":        {Large: []string{"plan"}},
	"GoalCompleteConfirm": {Short: []string{"confirm"}, Large: []string{"evidence", "reflection"}},
	"GoalCompleteRevise":  {Short: []string{"confirm"}, Large: []string{"feedback", "nextSteps"}},
	"StartPipeline":       {Short: []string{"label", "maxPreviewChars"}},
	"EndPipeline":         {Short: []string{"aliases", "format", "maxChars"}, Large: []string{"rule"}},
	"AskUserQuestion":     {},
}

var jsonObjectKeyPattern = regexp.MustCompile(`"(\w+)"\s*:\s*`)
var jsonPrimitivePattern = regexp.MustCompile(`^(true|false|null|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)`)

func enrichStreamingToolChunk(acc *toolChunkAccum, chunk *ToolUseChunk, cwd string) *ToolUseChunk {
	if acc == nil || chunk == nil {
		return chunk
	}
	wanted := wantedToolFields(acc.Name)
	if len(wanted) > 0 {
		result := extractJSONFields(acc.Input, wanted)
		if len(result.Fields) > 0 {
			if acc.ExtractedFields == nil {
				acc.ExtractedFields = map[string]string{}
			}
			for key, value := range result.Fields {
				acc.ExtractedFields[key] = value
			}
			if value := result.Fields["file_path"]; value != "" && acc.ExtractedFilePath == "" {
				acc.ExtractedFilePath = value
			}
		}
		if result.ActiveName != "" {
			if acc.ActiveStreamingField != result.ActiveName {
				acc.StreamingFieldYielded = 0
			}
			acc.ActiveStreamingField = result.ActiveName
		}
		if acc.StreamingMetadata == nil && acc.Name == "Edit" && acc.ExtractedFields["old_string"] != "" && firstNonEmpty(acc.ExtractedFilePath, acc.ExtractedFields["file_path"]) != "" {
			acc.StreamingMetadata = resolveStreamingEditMetadata(acc, cwd)
		}
	}
	out := *chunk
	out.ToolUseID = acc.ID
	out.Name = acc.Name
	out.OutputIndex = acc.OutputIndex
	out.InputCharsTotal = len(acc.Input)
	if acc.ExtractedFilePath != "" {
		out.ExtractedFilePath = acc.ExtractedFilePath
		out.ContentCharsReceived = contentCharsReceived(acc.Input, acc.ExtractedFilePath)
	}
	if len(acc.ExtractedFields) > 0 {
		out.ExtractedFields = cloneStringMap(acc.ExtractedFields)
	}
	if len(acc.StreamingMetadata) > 0 {
		out.ProviderSpecificState = cloneAnyMap(acc.StreamingMetadata)
	}
	if field := streamingFieldDelta(acc, wanted); field != nil {
		out.StreamingFieldName = field.Name
		out.StreamingFieldDelta = field.Delta
	}
	return &out
}

func wantedToolFields(toolName string) map[string]bool {
	config, ok := toolFieldConfigs[toolName]
	if !ok {
		return nil
	}
	wanted := map[string]bool{}
	for _, key := range config.Short {
		wanted[key] = true
	}
	for _, key := range config.Large {
		wanted[key] = true
	}
	return wanted
}

func extractJSONFields(raw string, wanted map[string]bool) extractedJSONFieldsResult {
	fields := map[string]string{}
	matches := jsonObjectKeyPattern.FindAllStringSubmatchIndex(raw, -1)
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		key := raw[match[2]:match[3]]
		if !wanted[key] {
			continue
		}
		afterColon := match[1]
		if afterColon >= len(raw) {
			continue
		}
		if raw[afterColon] != '"' {
			if primitive := jsonPrimitivePattern.FindString(raw[afterColon:]); primitive != "" {
				fields[key] = primitive
			}
			continue
		}
		valueStart := afterColon + 1
		closeQuote := findJSONClosingQuote(raw, valueStart)
		if closeQuote >= 0 {
			fields[key] = unescapeJSONString(raw[valueStart:closeQuote])
			continue
		}
		return extractedJSONFieldsResult{Fields: fields, ActiveName: key, ActiveStart: valueStart}
	}
	return extractedJSONFieldsResult{Fields: fields}
}

func findJSONClosingQuote(s string, start int) int {
	for i := start; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == '"' {
			return i
		}
	}
	return -1
}

func decodeJSONStringFragment(s string) decodedJSONStringFragment {
	var b strings.Builder
	i := 0
	for i < len(s) {
		ch := s[i]
		if ch != '\\' {
			b.WriteByte(ch)
			i++
			continue
		}
		if i+1 >= len(s) {
			break
		}
		escaped := s[i+1]
		switch escaped {
		case '"':
			b.WriteByte('"')
			i += 2
		case '\\':
			b.WriteByte('\\')
			i += 2
		case '/':
			b.WriteByte('/')
			i += 2
		case 'b':
			b.WriteByte('\b')
			i += 2
		case 'f':
			b.WriteByte('\f')
			i += 2
		case 'n':
			b.WriteByte('\n')
			i += 2
		case 'r':
			b.WriteByte('\r')
			i += 2
		case 't':
			b.WriteByte('\t')
			i += 2
		case 'u':
			if i+6 > len(s) {
				return decodedJSONStringFragment{Text: b.String(), Consumed: i}
			}
			var r rune
			ok := true
			for _, c := range s[i+2 : i+6] {
				r <<= 4
				switch {
				case c >= '0' && c <= '9':
					r += c - '0'
				case c >= 'a' && c <= 'f':
					r += c - 'a' + 10
				case c >= 'A' && c <= 'F':
					r += c - 'A' + 10
				default:
					ok = false
				}
			}
			if !ok {
				b.WriteByte('\\')
				b.WriteByte(escaped)
				i += 2
			} else {
				b.WriteRune(r)
				i += 6
			}
		default:
			b.WriteByte('\\')
			b.WriteByte(escaped)
			i += 2
		}
	}
	return decodedJSONStringFragment{Text: b.String(), Consumed: i}
}

func unescapeJSONString(s string) string {
	decoded := decodeJSONStringFragment(s)
	if decoded.Consumed == len(s) {
		return decoded.Text
	}
	return decoded.Text + s[decoded.Consumed:]
}

type streamingFieldResult struct {
	Name  string
	Delta string
}

func streamingFieldDelta(acc *toolChunkAccum, wanted map[string]bool) *streamingFieldResult {
	if acc.ActiveStreamingField == "" || len(wanted) == 0 {
		return nil
	}
	result := extractJSONFields(acc.Input, wanted)
	if result.ActiveName != acc.ActiveStreamingField || result.ActiveStart < 0 || result.ActiveStart > len(acc.Input) {
		return nil
	}
	fullRaw := acc.Input[result.ActiveStart:]
	if len(fullRaw) <= acc.StreamingFieldYielded {
		return nil
	}
	decoded := decodeJSONStringFragment(fullRaw[acc.StreamingFieldYielded:])
	acc.StreamingFieldYielded += decoded.Consumed
	if decoded.Text == "" {
		return nil
	}
	return &streamingFieldResult{Name: acc.ActiveStreamingField, Delta: decoded.Text}
}

func contentCharsReceived(raw, filePath string) int {
	contentChars := len(raw)
	if filePath != "" {
		fieldSize := len("\"file_path\":\"" + filePath + "\",")
		contentChars -= fieldSize
	}

	if contentChars < 0 {
		return 0
	}
	return contentChars
}

func resolveStreamingEditMetadata(acc *toolChunkAccum, cwd string) map[string]any {
	filePath := firstNonEmpty(acc.ExtractedFilePath, acc.ExtractedFields["file_path"])
	oldString := acc.ExtractedFields["old_string"]
	if filePath == "" || oldString == "" {
		return nil
	}
	resolved, err := resolveWithin(cwd, filePath)
	if err != nil {
		return map[string]any{"matchStatus": "unmatched", "matchError": err.Error()}
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return map[string]any{"matchStatus": "unmatched", "matchError": err.Error()}
	}
	content := normalizeStreamingLineEndings(string(data))
	normalizedOld := normalizeStreamingLineEndings(oldString)
	idx := strings.Index(content, normalizedOld)
	if idx < 0 {
		return map[string]any{"matchStatus": "unmatched", "matchError": "old_string not found"}
	}
	if acc.ExtractedFields["replace_all"] != "true" && strings.Count(content, normalizedOld) > 1 {
		return map[string]any{"matchStatus": "unmatched", "matchError": "old_string has multiple matches"}
	}
	startLine := 1 + strings.Count(content[:idx], "\n")
	lineCount := strings.Count(normalizedOld, "\n") + 1
	return map[string]any{"matchStatus": "matched", "startLine": startLine, "endLine": startLine + lineCount - 1, "filePath": filepath.ToSlash(resolved)}
}

func normalizeStreamingLineEndings(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
