package userprefs

import (
	"strings"
	"testing"
)

// TestValidateCommandParams locks the custom-command params validation against
// the TypeScript Zod schema in server/lib/validators/common.ts: params is an
// optional array of at most 20 items; each item requires name (1..50), and
// optionally description (<=500), required (bool), defaultValue (<=1000).
func TestValidateCommandParams(t *testing.T) {
	t.Run("absent params ok", func(t *testing.T) {
		if err := validateCommandParams(map[string]any{}, "cmd"); err != nil {
			t.Fatalf("absent params should be ok: %v", err)
		}
	})

	t.Run("valid params ok", func(t *testing.T) {
		cmd := map[string]any{"params": []any{
			map[string]any{"name": "city", "description": "target", "required": true, "defaultValue": "NYC"},
		}}
		if err := validateCommandParams(cmd, "cmd"); err != nil {
			t.Fatalf("valid params should pass: %v", err)
		}
	})

	t.Run("non-array rejected", func(t *testing.T) {
		if err := validateCommandParams(map[string]any{"params": "nope"}, "cmd"); err == nil {
			t.Fatal("non-array params must be rejected")
		}
	})

	t.Run("too many items rejected", func(t *testing.T) {
		items := make([]any, 21)
		for i := range items {
			items[i] = map[string]any{"name": "p"}
		}
		err := validateCommandParams(map[string]any{"params": items}, "cmd")
		if err == nil || !strings.Contains(err.Error(), "at most 20") {
			t.Fatalf("expected at-most-20 error, got %v", err)
		}
	})

	t.Run("non-object item rejected", func(t *testing.T) {
		err := validateCommandParams(map[string]any{"params": []any{"x"}}, "cmd")
		if err == nil || !strings.Contains(err.Error(), "params[0] must be an object") {
			t.Fatalf("expected object error, got %v", err)
		}
	})

	t.Run("missing name rejected", func(t *testing.T) {
		err := validateCommandParams(map[string]any{"params": []any{map[string]any{}}}, "cmd")
		if err == nil || !strings.Contains(err.Error(), "name is required") {
			t.Fatalf("expected name-required error, got %v", err)
		}
	})

	t.Run("name too long rejected", func(t *testing.T) {
		long := strings.Repeat("a", 51)
		err := validateCommandParams(map[string]any{"params": []any{map[string]any{"name": long}}}, "cmd")
		if err == nil || !strings.Contains(err.Error(), "at most 50") {
			t.Fatalf("expected name max-50 error, got %v", err)
		}
	})

	t.Run("defaultValue too long rejected", func(t *testing.T) {
		long := strings.Repeat("a", 1001)
		err := validateCommandParams(map[string]any{"params": []any{map[string]any{"name": "p", "defaultValue": long}}}, "cmd")
		if err == nil || !strings.Contains(err.Error(), "at most 1000") {
			t.Fatalf("expected defaultValue max-1000 error, got %v", err)
		}
	})

	t.Run("required wrong type rejected", func(t *testing.T) {
		err := validateCommandParams(map[string]any{"params": []any{map[string]any{"name": "p", "required": "yes"}}}, "cmd")
		if err == nil || !strings.Contains(err.Error(), "required must be a boolean") {
			t.Fatalf("expected required-bool error, got %v", err)
		}
	})
}

// TestValidateCommandModelOverride locks the modelOverride validation: optional
// object requiring model (1..100) and mode enum ("temporary"|"permanent").
func TestValidateCommandModelOverride(t *testing.T) {
	t.Run("absent ok", func(t *testing.T) {
		if err := validateCommandModelOverride(map[string]any{}, "cmd"); err != nil {
			t.Fatalf("absent modelOverride should be ok: %v", err)
		}
	})

	t.Run("valid temporary ok", func(t *testing.T) {
		cmd := map[string]any{"modelOverride": map[string]any{"model": "kiro:x", "mode": "temporary"}}
		if err := validateCommandModelOverride(cmd, "cmd"); err != nil {
			t.Fatalf("valid override should pass: %v", err)
		}
	})

	t.Run("valid permanent ok", func(t *testing.T) {
		cmd := map[string]any{"modelOverride": map[string]any{"model": "kiro:x", "mode": "permanent"}}
		if err := validateCommandModelOverride(cmd, "cmd"); err != nil {
			t.Fatalf("valid override should pass: %v", err)
		}
	})

	t.Run("non-object rejected", func(t *testing.T) {
		if err := validateCommandModelOverride(map[string]any{"modelOverride": "x"}, "cmd"); err == nil {
			t.Fatal("non-object modelOverride must be rejected")
		}
	})

	t.Run("missing model rejected", func(t *testing.T) {
		err := validateCommandModelOverride(map[string]any{"modelOverride": map[string]any{"mode": "temporary"}}, "cmd")
		if err == nil || !strings.Contains(err.Error(), "model is required") {
			t.Fatalf("expected model-required error, got %v", err)
		}
	})

	t.Run("model too long rejected", func(t *testing.T) {
		long := strings.Repeat("m", 101)
		err := validateCommandModelOverride(map[string]any{"modelOverride": map[string]any{"model": long, "mode": "temporary"}}, "cmd")
		if err == nil || !strings.Contains(err.Error(), "at most 100") {
			t.Fatalf("expected model max-100 error, got %v", err)
		}
	})

	t.Run("unsupported mode rejected", func(t *testing.T) {
		err := validateCommandModelOverride(map[string]any{"modelOverride": map[string]any{"model": "x", "mode": "forever"}}, "cmd")
		if err == nil || !strings.Contains(err.Error(), "mode is not supported") {
			t.Fatalf("expected mode-not-supported error, got %v", err)
		}
	})
}

// TestValidateCommandParamsCJKLengthNotByteCount is a regression test for the
// byte-vs-character length divergence: string length limits mirror the TS Zod
// schema's .max() which counts characters (JS .length / UTF-16 units), so
// multibyte CJK names up to the character limit must be accepted even though
// their UTF-8 byte length far exceeds it. Previously Go used len() (bytes) and
// wrongly rejected these. (CJK chars are BMP, so 1 rune = 1 UTF-16 unit here;
// the astral/emoji tests below lock the rune-vs-UTF-16 distinction separately.)
func TestValidateCommandParamsCJKLengthNotByteCount(t *testing.T) {
	// 50 CJK chars = 150 UTF-8 bytes; must pass the name(1..50) limit.
	name50 := strings.Repeat("城", 50)
	cmd := map[string]any{"params": []any{map[string]any{"name": name50}}}
	if err := validateCommandParams(cmd, "cmd"); err != nil {
		t.Fatalf("50 CJK-char name should pass character-count limit: %v", err)
	}

	// 51 CJK chars must still be rejected (limit is on characters, not bytes).
	name51 := strings.Repeat("城", 51)
	cmd2 := map[string]any{"params": []any{map[string]any{"name": name51}}}
	if err := validateCommandParams(cmd2, "cmd"); err == nil || !strings.Contains(err.Error(), "at most 50") {
		t.Fatalf("51 CJK-char name should be rejected, got %v", err)
	}

	// defaultValue: 1000 CJK chars (3000 bytes) must pass the <=1000 limit.
	dv1000 := strings.Repeat("市", 1000)
	cmd3 := map[string]any{"params": []any{map[string]any{"name": "p", "defaultValue": dv1000}}}
	if err := validateCommandParams(cmd3, "cmd"); err != nil {
		t.Fatalf("1000 CJK-char defaultValue should pass: %v", err)
	}
}

// TestValidateCommandModelOverrideCJKLengthNotByteCount confirms the
// modelOverride.model limit also counts characters, not UTF-8 bytes.
func TestValidateCommandModelOverrideCJKLengthNotByteCount(t *testing.T) {
	model100 := strings.Repeat("模", 100)
	cmd := map[string]any{"modelOverride": map[string]any{"model": model100, "mode": "temporary"}}
	if err := validateCommandModelOverride(cmd, "cmd"); err != nil {
		t.Fatalf("100 CJK-char model should pass character-count limit: %v", err)
	}
	model101 := strings.Repeat("模", 101)
	cmd2 := map[string]any{"modelOverride": map[string]any{"model": model101, "mode": "temporary"}}
	if err := validateCommandModelOverride(cmd2, "cmd"); err == nil || !strings.Contains(err.Error(), "at most 100") {
		t.Fatalf("101 CJK-char model should be rejected, got %v", err)
	}
}

// TestValidateCommandParamsAstralLengthUsesUTF16 locks the param-length metric
// to UTF-16 code units (JavaScript String.prototype.length), matching the TS
// Zod .max() checks in server/lib/validators/common.ts. Each emoji below is a
// single rune but TWO UTF-16 units, so the boundary differs from a rune-count
// implementation. param.name has no regex (unlike command.name) so emoji are
// character-set-legal and exercise only the length metric. A rune-count port
// would wrongly ACCEPT these (26 runes <= 50) while TS rejects them (52 > 50).
func TestValidateCommandParamsAstralLengthUsesUTF16(t *testing.T) {
	// name: 25 emoji = 50 UTF-16 units, exactly the max -> accepted.
	name25 := strings.Repeat("😀", 25)
	if err := validateCommandParams(map[string]any{"params": []any{map[string]any{"name": name25}}}, "cmd"); err != nil {
		t.Fatalf("25-emoji name (50 UTF-16 units) should pass max(50): %v", err)
	}
	// name: 26 emoji = 52 UTF-16 units, just over the max -> rejected.
	name26 := strings.Repeat("😀", 26)
	if err := validateCommandParams(map[string]any{"params": []any{map[string]any{"name": name26}}}, "cmd"); err == nil || !strings.Contains(err.Error(), "at most 50") {
		t.Fatalf("26-emoji name (52 UTF-16 units) must be rejected by max(50), got %v", err)
	}
	// defaultValue: 501 emoji = 1002 UTF-16 units, over the <=1000 limit -> rejected.
	dvOver := strings.Repeat("😀", 501)
	if err := validateCommandParams(map[string]any{"params": []any{map[string]any{"name": "p", "defaultValue": dvOver}}}, "cmd"); err == nil || !strings.Contains(err.Error(), "at most 1000") {
		t.Fatalf("501-emoji defaultValue (1002 UTF-16 units) must be rejected by max(1000), got %v", err)
	}
	// description: 251 emoji = 502 UTF-16 units, over the <=500 limit -> rejected.
	descOver := strings.Repeat("😀", 251)
	if err := validateCommandParams(map[string]any{"params": []any{map[string]any{"name": "p", "description": descOver}}}, "cmd"); err == nil || !strings.Contains(err.Error(), "at most 500") {
		t.Fatalf("251-emoji description (502 UTF-16 units) must be rejected by max(500), got %v", err)
	}
}

// TestValidateCommandModelOverrideAstralLengthUsesUTF16 locks modelOverride.model
// to the same UTF-16 metric (max 100). 51 emoji = 102 UTF-16 units > 100, which a
// rune-count port (51 <= 100) would wrongly accept.
func TestValidateCommandModelOverrideAstralLengthUsesUTF16(t *testing.T) {
	model50 := strings.Repeat("😀", 50) // 100 UTF-16 units, exactly the max
	if err := validateCommandModelOverride(map[string]any{"modelOverride": map[string]any{"model": model50, "mode": "temporary"}}, "cmd"); err != nil {
		t.Fatalf("50-emoji model (100 UTF-16 units) should pass max(100): %v", err)
	}
	model51 := strings.Repeat("😀", 51) // 102 UTF-16 units, over the max
	if err := validateCommandModelOverride(map[string]any{"modelOverride": map[string]any{"model": model51, "mode": "temporary"}}, "cmd"); err == nil || !strings.Contains(err.Error(), "at most 100") {
		t.Fatalf("51-emoji model (102 UTF-16 units) must be rejected by max(100), got %v", err)
	}
}

// integer within range and rewrites the JSON float64 to an int in place.
func TestOptionalIntRangeFieldCoercion(t *testing.T) {
	item := map[string]any{"limit": float64(5)}
	if err := optionalIntRangeField(item, "limit", "p", 1, 10); err != nil {
		t.Fatalf("in-range value should pass: %v", err)
	}
	if got, ok := item["limit"].(int); !ok || got != 5 {
		t.Fatalf("expected limit coerced to int 5, got %#v", item["limit"])
	}

	if err := optionalIntRangeField(map[string]any{"limit": float64(11)}, "limit", "p", 1, 10); err == nil {
		t.Fatal("out-of-range value must be rejected")
	}
	if err := optionalIntRangeField(map[string]any{"limit": float64(1.5)}, "limit", "p", 1, 10); err == nil {
		t.Fatal("non-integer value must be rejected")
	}
	if err := optionalIntRangeField(map[string]any{}, "limit", "p", 1, 10); err != nil {
		t.Fatalf("absent field should be ok: %v", err)
	}
}

// TestOptionalEnumField verifies the optional enum guard accepts allowed values
// and rejects others.
func TestOptionalEnumField(t *testing.T) {
	allowed := []string{"a", "b"}
	if err := optionalEnumField(map[string]any{"k": "a"}, "k", "p", allowed); err != nil {
		t.Fatalf("allowed value should pass: %v", err)
	}
	if err := optionalEnumField(map[string]any{}, "k", "p", allowed); err != nil {
		t.Fatalf("absent field should be ok: %v", err)
	}
	if err := optionalEnumField(map[string]any{"k": "c"}, "k", "p", allowed); err == nil {
		t.Fatal("disallowed value must be rejected")
	}
	if err := optionalEnumField(map[string]any{"k": 3}, "k", "p", allowed); err == nil {
		t.Fatal("non-string value must be rejected")
	}
}
