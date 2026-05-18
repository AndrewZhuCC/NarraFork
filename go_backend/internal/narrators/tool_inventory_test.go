package narrators

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestDisabledToolsFromTraitsParsesCustomTrait(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"version": 1, "tools": []string{"Read", "Write"}})
	trait := "custom-disabled-tools:" + base64.RawURLEncoding.EncodeToString(payload)
	tools := disabledToolsFromTraits([]string{"unrelated", trait})
	if len(tools) != 2 || tools[0] != "Read" || tools[1] != "Write" {
		t.Fatalf("disabled tools = %#v, want Read/Write", tools)
	}
}

func TestAnyStringSliceAcceptsEnabledToolsStringSlice(t *testing.T) {
	tools := anyStringSlice([]string{"Browser", "", "ShareFile"})
	if len(tools) != 2 || tools[0] != "Browser" || tools[1] != "ShareFile" {
		t.Fatalf("enabled tools = %#v, want Browser/ShareFile", tools)
	}
}
