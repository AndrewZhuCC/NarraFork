package jsondb

import (
	"database/sql"
	"encoding/json"
	"strings"
)

func DecodeNull(raw sql.NullString) any {
	if !raw.Valid {
		return nil
	}
	return DecodeString(raw.String)
}

func DecodeString(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return raw
	}
	return value
}

func StringArray(raw sql.NullString) []string {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return []string{}
	}
	items := []string{}
	if err := json.Unmarshal([]byte(raw.String), &items); err != nil {
		return []string{}
	}
	if items == nil {
		return []string{}
	}
	return items
}

func ObjectOrNil(raw sql.NullString) any {
	if parsed, ok := DecodeNull(raw).(map[string]any); ok {
		return parsed
	}
	return nil
}

func ArrayOrNil(raw sql.NullString) any {
	if parsed, ok := DecodeNull(raw).([]any); ok {
		return parsed
	}
	return nil
}

func ObjectMap(value any) map[string]any {
	if parsed, ok := DecodeValue(value).(map[string]any); ok {
		return parsed
	}
	return map[string]any{}
}

func Array(value any) []any {
	if parsed, ok := DecodeValue(value).([]any); ok {
		return parsed
	}
	return []any{}
}

func DecodeValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return DecodeString(typed)
	case []byte:
		return DecodeString(string(typed))
	default:
		return typed
	}
}
