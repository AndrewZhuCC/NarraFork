package agentbasic

import (
	"encoding/json"
	"strings"
)

func isParsableJSON(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	var decoded any
	return json.Unmarshal([]byte(trimmed), &decoded) == nil
}
