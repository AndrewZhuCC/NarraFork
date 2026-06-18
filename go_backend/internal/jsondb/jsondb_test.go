package jsondb

import (
	"database/sql"
	"reflect"
	"testing"
)

func valid(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
func invalid() sql.NullString       { return sql.NullString{Valid: false} }

// TestDecodeNullAndString covers DecodeNull/DecodeString: invalid NullString and
// empty/whitespace strings decode to nil, valid JSON decodes to its Go value, and
// non-JSON text falls back to the raw string.
func TestDecodeNullAndString(t *testing.T) {
	if DecodeNull(invalid()) != nil {
		t.Fatalf("invalid NullString should decode to nil")
	}
	if DecodeNull(valid("   ")) != nil {
		t.Fatalf("whitespace should decode to nil")
	}
	if got := DecodeString(`{"a":1}`); !reflect.DeepEqual(got, map[string]any{"a": float64(1)}) {
		t.Fatalf("object decode = %#v", got)
	}
	if got := DecodeString(`[1,2]`); !reflect.DeepEqual(got, []any{float64(1), float64(2)}) {
		t.Fatalf("array decode = %#v", got)
	}
	// Non-JSON text falls back to the raw input string.
	if got := DecodeString("not json"); got != "not json" {
		t.Fatalf("non-JSON should fall back to raw string, got %#v", got)
	}
}

// TestStringArray covers the []string decoder: invalid/empty -> empty slice,
// valid JSON array -> parsed, malformed -> empty slice (never nil).
func TestStringArray(t *testing.T) {
	if got := StringArray(invalid()); len(got) != 0 || got == nil {
		t.Fatalf("invalid should yield empty non-nil slice, got %#v", got)
	}
	if got := StringArray(valid("")); len(got) != 0 || got == nil {
		t.Fatalf("empty should yield empty non-nil slice, got %#v", got)
	}
	if got := StringArray(valid(`["x","y"]`)); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Fatalf("array decode = %#v", got)
	}
	if got := StringArray(valid(`{"not":"array"}`)); len(got) != 0 || got == nil {
		t.Fatalf("malformed should yield empty non-nil slice, got %#v", got)
	}
	if got := StringArray(valid("null")); len(got) != 0 || got == nil {
		t.Fatalf("json null should yield empty non-nil slice, got %#v", got)
	}
}

// TestObjectAndArrayOrNil covers ObjectOrNil/ArrayOrNil: matching type returns the
// parsed value, mismatched type or invalid input returns nil.
func TestObjectAndArrayOrNil(t *testing.T) {
	if got := ObjectOrNil(valid(`{"k":"v"}`)); !reflect.DeepEqual(got, map[string]any{"k": "v"}) {
		t.Fatalf("ObjectOrNil object = %#v", got)
	}
	if ObjectOrNil(valid(`[1,2]`)) != nil {
		t.Fatalf("ObjectOrNil on array should be nil")
	}
	if ObjectOrNil(invalid()) != nil {
		t.Fatalf("ObjectOrNil on invalid should be nil")
	}
	if got := ArrayOrNil(valid(`[1,2]`)); !reflect.DeepEqual(got, []any{float64(1), float64(2)}) {
		t.Fatalf("ArrayOrNil array = %#v", got)
	}
	if ArrayOrNil(valid(`{"k":"v"}`)) != nil {
		t.Fatalf("ArrayOrNil on object should be nil")
	}
}

// TestObjectMapAndArrayAndDecodeValue covers ObjectMap/Array/DecodeValue across
// string, []byte, nil, and already-decoded inputs, with non-nil empty defaults.
func TestObjectMapAndArrayAndDecodeValue(t *testing.T) {
	if got := ObjectMap(`{"a":1}`); !reflect.DeepEqual(got, map[string]any{"a": float64(1)}) {
		t.Fatalf("ObjectMap from string = %#v", got)
	}
	if got := ObjectMap("not json"); len(got) != 0 || got == nil {
		t.Fatalf("ObjectMap fallback should be empty non-nil map, got %#v", got)
	}
	if got := Array([]byte(`[true,false]`)); !reflect.DeepEqual(got, []any{true, false}) {
		t.Fatalf("Array from []byte = %#v", got)
	}
	if got := Array(nil); len(got) != 0 || got == nil {
		t.Fatalf("Array(nil) should be empty non-nil slice, got %#v", got)
	}

	// DecodeValue passes through already-decoded non-string values unchanged.
	already := map[string]any{"x": 1}
	if got := DecodeValue(already); !reflect.DeepEqual(got, already) {
		t.Fatalf("DecodeValue should pass through decoded value, got %#v", got)
	}
	if DecodeValue(nil) != nil {
		t.Fatalf("DecodeValue(nil) should be nil")
	}
	if got := DecodeValue([]byte(`{"b":2}`)); !reflect.DeepEqual(got, map[string]any{"b": float64(2)}) {
		t.Fatalf("DecodeValue from []byte = %#v", got)
	}
}
