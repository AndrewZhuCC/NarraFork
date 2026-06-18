package gatewayruntime

import (
	"reflect"
	"testing"
)

func TestStringValueCoercion(t *testing.T) {
	if got := stringValue("  hi  "); got != "hi" {
		t.Fatalf("stringValue trims: got %q", got)
	}
	if got := stringValue(42); got != "" {
		t.Fatalf("stringValue non-string: got %q", got)
	}
	if got := stringValue(nil); got != "" {
		t.Fatalf("stringValue nil: got %q", got)
	}
}

func TestBoolValueCoercion(t *testing.T) {
	if !boolValue(true) {
		t.Fatal("boolValue(true) should be true")
	}
	if boolValue("true") {
		t.Fatal("boolValue non-bool should be false")
	}
	if boolValue(nil) {
		t.Fatal("boolValue(nil) should be false")
	}
}

func TestIntFromAny(t *testing.T) {
	// JSON numbers decode as float64; native ints also supported.
	if got := intFromAny(float64(20)); got != 20 {
		t.Fatalf("intFromAny(float64): got %d", got)
	}
	if got := intFromAny(7); got != 7 {
		t.Fatalf("intFromAny(int): got %d", got)
	}
	if got := intFromAny("9"); got != 0 {
		t.Fatalf("intFromAny(string) should be 0: got %d", got)
	}
	if got := intFromAny(nil); got != 0 {
		t.Fatalf("intFromAny(nil) should be 0: got %d", got)
	}
}

func TestFirstNonEmptyString(t *testing.T) {
	if got := firstNonEmptyString("", "  ", "x", "y"); got != "x" {
		t.Fatalf("firstNonEmptyString: got %q", got)
	}
	if got := firstNonEmptyString("  a  "); got != "a" {
		t.Fatalf("firstNonEmptyString trims: got %q", got)
	}
	if got := firstNonEmptyString("", "   "); got != "" {
		t.Fatalf("firstNonEmptyString all empty: got %q", got)
	}
}

func TestSplitList(t *testing.T) {
	got := splitList("  a , b,, c ,")
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitList: got %#v want %#v", got, want)
	}
	if got := splitList("   "); got != nil {
		t.Fatalf("splitList blank should be nil: got %#v", got)
	}
}

func TestStringSlice(t *testing.T) {
	got := stringSlice([]any{"a", "  b  ", "", 3, "c"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stringSlice: got %#v want %#v", got, want)
	}
	if got := stringSlice("not-a-slice"); got != nil {
		t.Fatalf("stringSlice non-slice should be nil: got %#v", got)
	}
}

func TestPlatformConfigFromMap(t *testing.T) {
	// botToken is used as a fallback when token is empty (firstNonEmptyString).
	cfg := platformConfigFromMap(map[string]any{
		"platform":     "telegram",
		"enabled":      true,
		"botToken":     "fallback-token",
		"apiBase":      "https://api.example",
		"allowedUsers": []any{"alice", "bob"},
	})
	if cfg.Platform != "telegram" || !cfg.Enabled || cfg.Token != "fallback-token" || cfg.APIBase != "https://api.example" {
		t.Fatalf("platformConfigFromMap fields: %#v", cfg)
	}
	if !reflect.DeepEqual(cfg.AllowedUsers, []string{"alice", "bob"}) {
		t.Fatalf("platformConfigFromMap allowedUsers: %#v", cfg.AllowedUsers)
	}

	// Explicit token wins over botToken.
	cfg2 := platformConfigFromMap(map[string]any{"token": "primary", "botToken": "fallback"})
	if cfg2.Token != "primary" {
		t.Fatalf("token should win over botToken: got %q", cfg2.Token)
	}
}

func TestUpsertPlatform(t *testing.T) {
	items := []platformConfig{{Platform: "telegram", Token: "old"}}
	// Replace existing by platform key.
	items = upsertPlatform(items, platformConfig{Platform: "telegram", Token: "new"})
	if len(items) != 1 || items[0].Token != "new" {
		t.Fatalf("upsertPlatform replace: %#v", items)
	}
	// Append new platform.
	items = upsertPlatform(items, platformConfig{Platform: "webhook", Enabled: true})
	if len(items) != 2 || items[1].Platform != "webhook" {
		t.Fatalf("upsertPlatform append: %#v", items)
	}
}

func TestGatewayMessageFromPayload(t *testing.T) {
	id, role, text := gatewayMessageFromPayload(map[string]any{
		"type":    "message",
		"message": map[string]any{"id": "m1", "role": "assistant", "contentText": "hello"},
	})
	if id != "m1" || role != "assistant" || text != "hello" {
		t.Fatalf("gatewayMessageFromPayload: %q %q %q", id, role, text)
	}

	// Non-message type yields empties.
	if id, role, text := gatewayMessageFromPayload(map[string]any{"type": "status"}); id != "" || role != "" || text != "" {
		t.Fatalf("non-message payload should be empty: %q %q %q", id, role, text)
	}

	// Unmarshalable payload yields empties.
	if id, _, _ := gatewayMessageFromPayload(make(chan int)); id != "" {
		t.Fatalf("unmarshalable payload should be empty, got id %q", id)
	}
}

func TestRateLimiterAllowSlidingWindow(t *testing.T) {
	r := newRateLimiter(2)
	if !r.Allow("k") || !r.Allow("k") {
		t.Fatal("first two events under limit should be allowed")
	}
	if r.Allow("k") {
		t.Fatal("third event within the minute should be denied")
	}
	// A different key has an independent budget.
	if !r.Allow("other") {
		t.Fatal("separate key should have its own budget")
	}
}
