package vnet

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestValueCoercionHelpers covers StringValue/IntValue/Int64Value, which decode
// loosely-typed JSON WS payload fields. JSON numbers arrive as float64 (or
// json.Number), so the coercion must handle those plus int/int64, and reject
// other types by returning the zero value.
func TestValueCoercionHelpers(t *testing.T) {
	if got := StringValue("  hi  "); got != "hi" {
		t.Fatalf("StringValue should trim, got %q", got)
	}
	if got := StringValue(123); got != "" {
		t.Fatalf("StringValue on non-string should be empty, got %q", got)
	}

	for _, tc := range []struct {
		in   any
		want int
	}{
		{42, 42},
		{int64(7), 7},
		{float64(9), 9}, // JSON numbers decode to float64
		{json.Number("11"), 11},
		{"nope", 0},
		{nil, 0},
	} {
		if got := IntValue(tc.in); got != tc.want {
			t.Fatalf("IntValue(%#v) = %d, want %d", tc.in, got, tc.want)
		}
	}

	for _, tc := range []struct {
		in   any
		want int64
	}{
		{int(5), 5},
		{int64(123456789012), 123456789012},
		{float64(1700000000000), 1700000000000},
		{json.Number("999"), 999},
		{"x", 0},
	} {
		if got := Int64Value(tc.in); got != tc.want {
			t.Fatalf("Int64Value(%#v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestParseEndpoints covers endpoint parsing: only relay/udp transports survive,
// non-object items and unknown transports are skipped, and field coercion uses
// the value helpers (port from float64, observedAt from float64).
func TestParseEndpoints(t *testing.T) {
	// Non-array input yields nil.
	if got := ParseEndpoints("not an array"); got != nil {
		t.Fatalf("ParseEndpoints on non-array should be nil, got %#v", got)
	}

	input := []any{
		map[string]any{"transport": "relay", "address": "1.2.3.4", "port": float64(8080), "observedAt": float64(1700)},
		map[string]any{"transport": "udp", "address": "5.6.7.8", "port": float64(9090)},
		map[string]any{"transport": "carrier-pigeon", "address": "nope"}, // unknown transport -> skipped
		"not-an-object", // skipped
	}
	got := ParseEndpoints(input)
	want := []Endpoint{
		{Transport: TransportRelay, Address: "1.2.3.4", Port: 8080, ObservedAt: 1700},
		{Transport: TransportUDP, Address: "5.6.7.8", Port: 9090, ObservedAt: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseEndpoints = %#v, want %#v", got, want)
	}
}

// TestJSONByteLength covers the byte-length helper used for bounded-message
// enforcement: marshalable values return their JSON length, unmarshalable values
// return MaxInt (so they exceed any limit).
func TestJSONByteLength(t *testing.T) {
	if got := JSONByteLength(map[string]any{"a": 1}); got != len(`{"a":1}`) {
		t.Fatalf("JSONByteLength of object = %d, want %d", got, len(`{"a":1}`))
	}
	if got := JSONByteLength("hi"); got != len(`"hi"`) {
		t.Fatalf("JSONByteLength of string = %d, want %d", got, len(`"hi"`))
	}
	// A value that cannot be marshaled (channel) returns a huge length.
	if got := JSONByteLength(make(chan int)); got <= 0 {
		t.Fatalf("JSONByteLength of unmarshalable value should be large, got %d", got)
	}
}
