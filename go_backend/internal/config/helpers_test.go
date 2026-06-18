package config

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestDeepMergeRecursesAndClones covers deepMerge: nested maps merge recursively,
// scalar/array values from src overwrite dst, and merged-in values are deep-cloned
// so later mutations of src do not leak into dst.
func TestDeepMergeRecursesAndClones(t *testing.T) {
	dst := map[string]any{
		"a": map[string]any{"keep": 1, "override": "old"},
		"b": "dst-only",
	}
	srcNested := map[string]any{"x": 1}
	src := map[string]any{
		"a": map[string]any{"override": "new", "added": srcNested},
		"c": "src-only",
	}
	deepMerge(dst, src)

	a := dst["a"].(map[string]any)
	if a["keep"] != 1 || a["override"] != "new" {
		t.Fatalf("nested merge wrong: %#v", a)
	}
	if dst["b"] != "dst-only" || dst["c"] != "src-only" {
		t.Fatalf("top-level merge wrong: %#v", dst)
	}

	// Mutating the original src nested map must not affect the merged copy.
	srcNested["x"] = 999
	added := a["added"].(map[string]any)
	if added["x"] != 1 {
		t.Fatalf("merged value should be deep-cloned, got %#v after src mutation", added)
	}
}

// TestGetNestedAccessors covers getNested/getString/getBool/getInt path traversal
// and type coercion with fallbacks.
func TestGetNestedAccessors(t *testing.T) {
	m := map[string]any{
		"server": map[string]any{
			"host":    "localhost",
			"tls":     map[string]any{"enabled": true},
			"port":    float64(7779),
			"workers": json.Number("4"),
			"intval":  9,
		},
	}

	if got := getString(m, "server", "host"); got != "localhost" {
		t.Fatalf("getString = %q", got)
	}
	// Missing path -> empty string.
	if got := getString(m, "server", "missing"); got != "" {
		t.Fatalf("getString missing should be empty, got %q", got)
	}
	// Traversing through a non-map -> empty.
	if got := getString(m, "server", "host", "deeper"); got != "" {
		t.Fatalf("getString through non-map should be empty, got %q", got)
	}

	if got := getBool(m, false, "server", "tls", "enabled"); got != true {
		t.Fatalf("getBool = %v", got)
	}
	if got := getBool(m, true, "server", "tls", "missing"); got != true {
		t.Fatalf("getBool fallback should apply, got %v", got)
	}

	// getInt coerces float64, int, and json.Number; falls back otherwise.
	if got := getInt(m, -1, "server", "port"); got != 7779 {
		t.Fatalf("getInt float64 = %d", got)
	}
	if got := getInt(m, -1, "server", "intval"); got != 9 {
		t.Fatalf("getInt int = %d", got)
	}
	if got := getInt(m, -1, "server", "workers"); got != 4 {
		t.Fatalf("getInt json.Number = %d", got)
	}
	if got := getInt(m, 42, "server", "host"); got != 42 {
		t.Fatalf("getInt on non-number should fall back, got %d", got)
	}
}

// TestEnsureMapAndCloneAny covers ensureMap (creates a child map when absent or
// wrong type) and cloneAny (deep-clones nested maps/slices).
func TestEnsureMapAndCloneAny(t *testing.T) {
	m := map[string]any{"existing": map[string]any{"k": "v"}, "scalar": 5}

	// Existing map is returned as-is.
	if child := ensureMap(m, "existing"); child["k"] != "v" {
		t.Fatalf("ensureMap should return existing child, got %#v", child)
	}
	// Absent key -> new empty map installed.
	child := ensureMap(m, "fresh")
	if child == nil || len(child) != 0 {
		t.Fatalf("ensureMap should create empty child, got %#v", child)
	}
	child["added"] = true
	if !reflect.DeepEqual(m["fresh"], map[string]any{"added": true}) {
		t.Fatalf("ensureMap child should be wired into parent, got %#v", m["fresh"])
	}
	// Wrong-typed key -> replaced with a fresh map.
	if c := ensureMap(m, "scalar"); c == nil || len(c) != 0 {
		t.Fatalf("ensureMap should replace non-map value, got %#v", c)
	}

	// cloneAny isolates nested structures.
	orig := map[string]any{"nested": map[string]any{"arr": []any{1, 2}}}
	cloned := cloneAny(orig).(map[string]any)
	orig["nested"].(map[string]any)["arr"].([]any)[0] = 99
	if cloned["nested"].(map[string]any)["arr"].([]any)[0] != 1 {
		t.Fatalf("cloneAny should deep-clone nested slices, got %#v", cloned)
	}
}
