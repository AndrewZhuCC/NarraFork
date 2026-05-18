package misccompat

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"narrafork-go/internal/httpx"
)

const (
	customSubagentFileName     = "SUBAGENT.md"
	maxCustomSubagentFileBytes = 256 * 1024
)

var builtinCustomSubagentTypes = map[string]bool{
	"explore": true,
	"plan":    true,
	"general": true,
	"review":  true,
}

type customSubagentDef struct {
	Name            string
	Description     string
	ToolAccess      string
	CustomTools     []string
	DefaultModel    string
	Prompt          string
	PromptTruncated bool
	Location        string
}

type customSubagentInput struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	ToolAccess   string   `json:"toolAccess"`
	CustomTools  []string `json:"customTools"`
	DefaultModel string   `json:"defaultModel"`
	Prompt       string   `json:"prompt"`
}

func listCustomSubagentDefs() ([]customSubagentDef, error) {
	root, err := customSubagentsRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []customSubagentDef{}, nil
		}
		return nil, err
	}
	defs := []customSubagentDef{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), customSubagentFileName)
		def, err := loadCustomSubagentFile(path, entry.Name())
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		defs = append(defs, def)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs, nil
}

func loadCustomSubagentDef(name string) (customSubagentDef, error) {
	dirName := sanitizeCustomSubagentDirName(name)
	if dirName == "" {
		return customSubagentDef{}, httpx.NotFound("Custom subagent")
	}
	root, err := customSubagentsRoot()
	if err != nil {
		return customSubagentDef{}, err
	}
	def, err := loadCustomSubagentFile(filepath.Join(root, dirName, customSubagentFileName), dirName)
	if err != nil {
		if os.IsNotExist(err) {
			return customSubagentDef{}, httpx.NotFound("Custom subagent")
		}
		return customSubagentDef{}, err
	}
	return def, nil
}

func createCustomSubagentDef(r *http.Request) (customSubagentDef, error) {
	input, err := decodeCustomSubagentInput(r)
	if err != nil {
		return customSubagentDef{}, err
	}
	if input.Name == "" {
		return customSubagentDef{}, httpx.BadRequest("name is required")
	}
	if builtinCustomSubagentTypes[input.Name] {
		return customSubagentDef{}, httpx.BadRequest("\"" + input.Name + "\" is a built-in subagent type and cannot be used")
	}
	dirName := sanitizeCustomSubagentDirName(input.Name)
	if dirName == "" {
		return customSubagentDef{}, httpx.BadRequest("Invalid subagent name")
	}
	root, err := customSubagentsRoot()
	if err != nil {
		return customSubagentDef{}, err
	}
	dir := filepath.Join(root, dirName)
	if _, err := os.Stat(dir); err == nil {
		return customSubagentDef{}, httpx.BadRequest("Custom subagent directory already exists: " + dirName)
	} else if !os.IsNotExist(err) {
		return customSubagentDef{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return customSubagentDef{}, err
	}
	path := filepath.Join(dir, customSubagentFileName)
	if err := os.WriteFile(path, []byte(formatCustomSubagentMarkdown(input)), 0o644); err != nil {
		return customSubagentDef{}, err
	}
	return loadCustomSubagentFile(path, dirName)
}

func updateCustomSubagentDef(r *http.Request, currentName string) (customSubagentDef, error) {
	existing, err := loadCustomSubagentDef(currentName)
	if err != nil {
		return customSubagentDef{}, err
	}
	input, err := decodeCustomSubagentInput(r)
	if err != nil {
		return customSubagentDef{}, err
	}
	if input.Name == "" {
		input.Name = strings.TrimSpace(currentName)
	}
	if input.Name == "" {
		return customSubagentDef{}, httpx.BadRequest("name is required")
	}
	oldDir := filepath.Dir(existing.Location)
	oldDirName := filepath.Base(oldDir)
	newDirName := sanitizeCustomSubagentDirName(input.Name)
	if newDirName == "" {
		return customSubagentDef{}, httpx.BadRequest("Invalid subagent name")
	}
	path := existing.Location
	if newDirName != oldDirName {
		if builtinCustomSubagentTypes[input.Name] {
			return customSubagentDef{}, httpx.BadRequest("\"" + input.Name + "\" is a built-in subagent type and cannot be used")
		}
		root, err := customSubagentsRoot()
		if err != nil {
			return customSubagentDef{}, err
		}
		newDir := filepath.Join(root, newDirName)
		if _, err := os.Stat(newDir); err == nil {
			return customSubagentDef{}, httpx.BadRequest("Custom subagent directory already exists: " + newDirName)
		} else if !os.IsNotExist(err) {
			return customSubagentDef{}, err
		}
		if err := os.Rename(oldDir, newDir); err != nil {
			return customSubagentDef{}, err
		}
		path = filepath.Join(newDir, customSubagentFileName)
	}
	if err := os.WriteFile(path, []byte(formatCustomSubagentMarkdown(input)), 0o644); err != nil {
		return customSubagentDef{}, err
	}
	return loadCustomSubagentFile(path, newDirName)
}

func deleteCustomSubagentDef(name string) error {
	def, err := loadCustomSubagentDef(name)
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Dir(def.Location))
}

func decodeCustomSubagentInput(r *http.Request) (customSubagentInput, error) {
	var input customSubagentInput
	if err := httpx.DecodeLooseJSON(r, &input); err != nil {
		return input, err
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.ToolAccess = validateCustomSubagentToolAccess(input.ToolAccess)
	input.DefaultModel = strings.TrimSpace(input.DefaultModel)
	input.Prompt = strings.TrimSpace(input.Prompt)
	if input.CustomTools == nil {
		input.CustomTools = []string{}
	}
	tools := make([]string, 0, len(input.CustomTools))
	for _, tool := range input.CustomTools {
		if tool != "" {
			tools = append(tools, tool)
		}
	}
	input.CustomTools = tools
	return input, nil
}

func validateCustomSubagentToolAccess(value string) string {
	if value == "general" || value == "custom" {
		return value
	}
	return "readOnly"
}

func customSubagentResponses(defs []customSubagentDef) []map[string]any {
	items := make([]map[string]any, 0, len(defs))
	for _, def := range defs {
		items = append(items, customSubagentResponse(def))
	}
	return items
}

func customSubagentResponse(def customSubagentDef) map[string]any {
	return map[string]any{
		"name":            def.Name,
		"description":     def.Description,
		"toolAccess":      def.ToolAccess,
		"customTools":     def.CustomTools,
		"defaultModel":    def.DefaultModel,
		"prompt":          def.Prompt,
		"promptTruncated": def.PromptTruncated,
	}
}

func customSubagentsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".narrafork", "subagents"), nil
}

func loadCustomSubagentFile(path, dirName string) (customSubagentDef, error) {
	raw, truncated, err := readCustomSubagentFileLimited(path)
	if err != nil {
		return customSubagentDef{}, err
	}
	frontmatter, content := splitCustomSubagentFrontmatter(raw)
	def := customSubagentDef{
		Name:            dirName,
		ToolAccess:      "readOnly",
		CustomTools:     []string{},
		DefaultModel:    "",
		Prompt:          strings.TrimSpace(content),
		PromptTruncated: truncated,
		Location:        path,
	}
	if name := strings.TrimSpace(frontmatter["name"]); name != "" {
		def.Name = name
	}
	def.Description = strings.TrimSpace(frontmatter["description"])
	def.ToolAccess = validateCustomSubagentToolAccess(frontmatter["toolAccess"])
	def.CustomTools = frontmatterArray(frontmatter, "customTools")
	def.DefaultModel = strings.TrimSpace(frontmatter["defaultModel"])
	return def, nil
}

func readCustomSubagentFileLimited(path string) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxCustomSubagentFileBytes+1))
	if err != nil {
		return "", false, err
	}
	if len(data) > maxCustomSubagentFileBytes {
		return string(data[:maxCustomSubagentFileBytes]) + "\n[truncated]", true, nil
	}
	return string(data), false, nil
}

func splitCustomSubagentFrontmatter(raw string) (map[string]string, string) {
	data := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return data, raw
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return data, raw
	}
	for i := 1; i < end; i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if value == "" && i+1 < end {
			items := []string{}
			for i+1 < end {
				next := strings.TrimSpace(lines[i+1])
				if !strings.HasPrefix(next, "-") {
					break
				}
				item := strings.TrimSpace(strings.TrimPrefix(next, "-"))
				items = append(items, parseCustomSubagentScalar(item))
				i++
			}
			data[key] = strings.Join(items, "\x00")
			continue
		}
		data[key] = parseCustomSubagentScalar(value)
	}
	return data, strings.Join(lines[end+1:], "\n")
}

func frontmatterArray(data map[string]string, key string) []string {
	value := data[key]
	if value == "" {
		return []string{}
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		return parseInlineCustomSubagentArray(value)
	}
	parts := strings.Split(value, "\x00")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			items = append(items, part)
		}
	}
	return items
}

func parseInlineCustomSubagentArray(value string) []string {
	var jsonItems []string
	if err := json.Unmarshal([]byte(value), &jsonItems); err == nil {
		return jsonItems
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
	if inner == "" {
		return []string{}
	}
	parts := strings.Split(inner, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		item := parseCustomSubagentScalar(strings.TrimSpace(part))
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func parseCustomSubagentScalar(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		var decoded string
		if err := json.Unmarshal([]byte(value), &decoded); err == nil {
			return decoded
		}
	}
	if len(value) >= 2 && strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	return value
}

func formatCustomSubagentMarkdown(input customSubagentInput) string {
	var builder strings.Builder
	builder.WriteString("---\n")
	builder.WriteString("name: ")
	builder.WriteString(yamlQuotedString(input.Name))
	builder.WriteString("\n")
	builder.WriteString("description: ")
	builder.WriteString(yamlQuotedString(input.Description))
	builder.WriteString("\n")
	builder.WriteString("toolAccess: ")
	builder.WriteString(yamlQuotedString(input.ToolAccess))
	builder.WriteString("\n")
	if input.ToolAccess == "custom" && len(input.CustomTools) > 0 {
		builder.WriteString("customTools:\n")
		for _, tool := range input.CustomTools {
			builder.WriteString("  - ")
			builder.WriteString(yamlQuotedString(tool))
			builder.WriteString("\n")
		}
	}
	if input.DefaultModel != "" {
		builder.WriteString("defaultModel: ")
		builder.WriteString(yamlQuotedString(input.DefaultModel))
		builder.WriteString("\n")
	}
	builder.WriteString("---\n\n")
	builder.WriteString(input.Prompt)
	builder.WriteString("\n")
	return builder.String()
}

func yamlQuotedString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

func sanitizeCustomSubagentDirName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var builder strings.Builder
	lastDash := false
	for _, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || (r >= '\u4e00' && r <= '\u9fff')
		if valid {
			if r == '-' && lastDash {
				continue
			}
			builder.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}
