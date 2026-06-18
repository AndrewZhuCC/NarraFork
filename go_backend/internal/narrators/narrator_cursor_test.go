package narrators

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"testing"
)

// TestNarratorSortColumnMatchesTS pins narratorSortColumn to the TS
// sortColumnMap (server/routes/narrators.ts): updatedAt/createdAt/title/
// messageCount are recognized, anything else falls back to updatedAt.
func TestNarratorSortColumnMatchesTS(t *testing.T) {
	cases := map[string][2]string{
		"updatedAt":    {"updatedAt", "updated_at"},
		"createdAt":    {"createdAt", "created_at"},
		"title":        {"title", "title"},
		"messageCount": {"messageCount", "message_count"},
		"bogus":        {"updatedAt", "updated_at"},
		"":             {"updatedAt", "updated_at"},
	}
	for in, want := range cases {
		sortBy, col := narratorSortColumn(in)
		if sortBy != want[0] || col != want[1] {
			t.Fatalf("narratorSortColumn(%q) = (%q,%q), want (%q,%q)", in, sortBy, col, want[0], want[1])
		}
	}
}

// TestNarratorCursorWhereMatchesTS pins the keyset-pagination WHERE clause to the
// TS cursorWhere: or(cmp(column, v), and(eq(column, v), cmp(id, cursorId))).
// Empty/invalid/garbage cursors yield no clause (TS try/catch -> ignore).
func TestNarratorCursorWhereMatchesTS(t *testing.T) {
	// Empty cursor -> no clause.
	if where, args := narratorCursorWhere("", "updated_at", "<", false); where != "" || args != nil {
		t.Fatalf("empty cursor should yield no clause, got %q %v", where, args)
	}
	// Non-base64 garbage -> no clause.
	if where, _ := narratorCursorWhere("!!!notbase64!!!", "updated_at", "<", false); where != "" {
		t.Fatalf("garbage cursor should yield no clause, got %q", where)
	}
	// Valid-base64 but missing id -> no clause.
	noID := base64.RawURLEncoding.EncodeToString([]byte(`{"v":"2026-01-01"}`))
	if where, _ := narratorCursorWhere(noID, "updated_at", "<", false); where != "" {
		t.Fatalf("cursor without id should yield no clause, got %q", where)
	}

	// String value (desc -> cmp "<").
	strCursor := base64.RawURLEncoding.EncodeToString([]byte(`{"v":"2026-01-02T00:00:00Z","id":"nar9"}`))
	where, args := narratorCursorWhere(strCursor, "updated_at", "<", false)
	wantWhere := "(updated_at < ? OR (updated_at = ? AND id < ?))"
	if where != wantWhere {
		t.Fatalf("string cursor where = %q, want %q", where, wantWhere)
	}
	if !reflect.DeepEqual(args, []any{"2026-01-02T00:00:00Z", "2026-01-02T00:00:00Z", "nar9"}) {
		t.Fatalf("string cursor args = %#v", args)
	}

	// Numeric value (asc -> cmp ">"); JSON number decodes to float64 -> int.
	numCursor := base64.RawURLEncoding.EncodeToString([]byte(`{"v":42,"id":"nar1"}`))
	where, args = narratorCursorWhere(numCursor, "message_count", ">", true)
	wantWhere = "(message_count > ? OR (message_count = ? AND id > ?))"
	if where != wantWhere {
		t.Fatalf("numeric cursor where = %q, want %q", where, wantWhere)
	}
	if !reflect.DeepEqual(args, []any{42, 42, "nar1"}) {
		t.Fatalf("numeric cursor args = %#v, want [42 42 nar1]", args)
	}
}

// TestEncodeNarratorCursorRoundTrip verifies encodeNarratorCursor produces an
// unpadded base64url JSON {v,id} that decodes back to the sort value keyed by
// sortBy, matching the TS nextCursor shape (lastItem[sortBy], id) and remaining
// cross-backend decodable (key lookup by name, order-independent).
func TestEncodeNarratorCursorRoundTrip(t *testing.T) {
	title := "Hello"
	n := Narrator{ID: "narX", UpdatedAt: "2026-02-02T00:00:00Z", CreatedAt: "2026-01-01T00:00:00Z", Title: &title, MessageCount: 7}

	decode := func(enc any) map[string]any {
		s, ok := enc.(string)
		if !ok {
			t.Fatalf("cursor not a string: %#v", enc)
		}
		raw, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatalf("cursor not valid base64url: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("cursor not valid JSON: %v", err)
		}
		return m
	}

	for _, tc := range []struct {
		sortBy string
		wantV  any
	}{
		{"updatedAt", "2026-02-02T00:00:00Z"},
		{"createdAt", "2026-01-01T00:00:00Z"},
		{"title", "Hello"},
		{"messageCount", float64(7)},
	} {
		m := decode(encodeNarratorCursor(tc.sortBy, n))
		if m["id"] != "narX" {
			t.Fatalf("%s: cursor id = %v, want narX", tc.sortBy, m["id"])
		}
		if m["v"] != tc.wantV {
			t.Fatalf("%s: cursor v = %#v, want %#v", tc.sortBy, m["v"], tc.wantV)
		}
	}

	// Nil title -> empty-string value (matches TS coalescing).
	nilTitle := Narrator{ID: "narY", UpdatedAt: "u", Title: nil}
	m := decode(encodeNarratorCursor("title", nilTitle))
	if m["v"] != "" {
		t.Fatalf("nil title cursor v = %#v, want empty string", m["v"])
	}
}
