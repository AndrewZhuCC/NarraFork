package search

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSearchLimitMergesPerEntityWithoutTruncatingToLimitMatchTS verifies the
// limit semantics match the TypeScript route + search-service:
//  1. `limit` is applied PER ENTITY in SQL, then the per-entity results are
//     merged and returned WITHOUT a further truncation to `limit`. A previous Go
//     `results[:limit]` post-slice wrongly capped the merged multi-entity set.
//  2. The limit is only upper-clamped to 100 (TS Math.min(rawLimit,100));
//     zero/negative limits pass through to SQL LIMIT exactly like TS rather than
//     being clamped up to 100.
func TestSearchLimitMergesPerEntityWithoutTruncatingToLimitMatchTS(t *testing.T) {
	sqlDB := openSearchContractDB(t)
	defer sqlDB.Close()
	createSearchContractBaseTables(t, sqlDB)

	// Project + 3 matching chapters and 3 matching narrators (short query forces
	// the LIKE path, so no FTS tables are required).
	if _, err := sqlDB.Exec(`INSERT INTO projects (id, name, description) VALUES ('p', 'P', 'x')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := sqlDB.Exec(`INSERT INTO chapters (id, project_id, title, description, status, role, created_at, updated_at) VALUES (?, 'p', ?, 'zz match', 'active', 'branch', ?, ?)`,
			fmt.Sprintf("c%d", i), fmt.Sprintf("zz chapter %d", i), fmt.Sprintf("2026-05-2%dT00:00:00Z", i), fmt.Sprintf("2026-05-2%dT00:00:00Z", i)); err != nil {
			t.Fatalf("insert chapter %d: %v", i, err)
		}
		if _, err := sqlDB.Exec(`INSERT INTO narrators (id, chapter_id, title, status, model, last_message_at, created_at, updated_at) VALUES (?, ?, ?, 'idle', 'm', ?, ?, ?)`,
			fmt.Sprintf("n%d", i), fmt.Sprintf("c%d", i), fmt.Sprintf("zz narrator %d", i), fmt.Sprintf("2026-05-2%dT00:00:00Z", i), fmt.Sprintf("2026-05-2%dT00:00:00Z", i), fmt.Sprintf("2026-05-2%dT00:00:00Z", i)); err != nil {
			t.Fatalf("insert narrator %d: %v", i, err)
		}
	}

	resultsFor := func(t *testing.T, query string) []any {
		t.Helper()
		h := NewHandler(sqlDB)
		req := httptest.NewRequest(http.MethodGet, "/api/search?"+query, nil)
		w := httptest.NewRecorder()
		if err := h.search(w, req); err != nil {
			t.Fatalf("search error: %v", err)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		results, _ := body["results"].([]any)
		return results
	}

	// limit=2 across chapters+narrators: 2 chapters + 2 narrators = 4 merged
	// results, NOT truncated to 2.
	if got := len(resultsFor(t, "q=zz&entities=chapters,narrators&limit=2")); got != 4 {
		t.Fatalf("limit=2 over 2 entities should yield 4 merged results (2 each), got %d", got)
	}

	// No limit param -> per-entity 50, so all 3+3=6 are returned.
	if got := len(resultsFor(t, "q=zz&entities=chapters,narrators")); got != 6 {
		t.Fatalf("default limit should return all 6 matches, got %d", got)
	}

	// limit=0 -> SQLite LIMIT 0 -> zero results (matches TS Math.min(0,100)=0),
	// rather than the old Go behavior of clamping 0 up to 100.
	if got := len(resultsFor(t, "q=zz&entities=chapters,narrators&limit=0")); got != 0 {
		t.Fatalf("limit=0 must yield 0 results (SQL LIMIT 0), got %d", got)
	}

	// limit=200 -> upper-clamped to 100 -> all 6 still returned.
	if got := len(resultsFor(t, "q=zz&entities=chapters,narrators&limit=200")); got != 6 {
		t.Fatalf("limit=200 clamps to 100 and returns all 6, got %d", got)
	}

	// JS Number.parseInt prefix semantics: "2x" -> 2 (NOT a whole-string parse
	// failure that falls back to 50). With limit=2 per entity over 2 entities we
	// get 4 merged results. strconv.Atoi("2x") errors and would wrongly fall back
	// to 50 -> 6 results, so this asserts the jsParseInt10 leading-digit parse.
	if got := len(resultsFor(t, "q=zz&entities=chapters,narrators&limit=2x")); got != 4 {
		t.Fatalf(`limit="2x" must parse leading digits to 2 (4 merged results), got %d`, got)
	}

	// "12.9" -> 12 (parseInt stops at the dot), upper of per-entity matches so all
	// 6 returned; this also exercises the trailing-non-digit path.
	if got := len(resultsFor(t, "q=zz&entities=chapters,narrators&limit=12.9")); got != 6 {
		t.Fatalf(`limit="12.9" must parse to 12 and return all 6, got %d`, got)
	}

	// "120x" -> 120 -> clamped to 100 -> all 6 returned. Atoi would fall back to
	// 50 (also 6 here) so pair it with the "2x" case above which distinguishes the
	// two implementations.
	if got := len(resultsFor(t, "q=zz&entities=chapters,narrators&limit=120x")); got != 6 {
		t.Fatalf(`limit="120x" must parse to 120, clamp to 100, return all 6, got %d`, got)
	}

	// Non-numeric leading char -> NaN -> fall back to 50 -> all 6 returned.
	if got := len(resultsFor(t, "q=zz&entities=chapters,narrators&limit=abc")); got != 6 {
		t.Fatalf(`limit="abc" must fall back to 50 and return all 6, got %d`, got)
	}

	// "0x10" -> parseInt base 10 stops after the leading 0 -> 0 -> SQL LIMIT 0 ->
	// zero results (a base-prefix-naive parse would wrongly read it as hex 16).
	if got := len(resultsFor(t, "q=zz&entities=chapters,narrators&limit=0x10")); got != 0 {
		t.Fatalf(`limit="0x10" must parse to 0 (LIMIT 0, no rows), got %d`, got)
	}
}

// TestJSParseInt10MatchesNumberParseInt unit-tests the JS Number.parseInt(_, 10)
// helper directly across leading-prefix, sign, whitespace, and NaN cases.
func TestJSParseInt10MatchesNumberParseInt(t *testing.T) {
	cases := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"42", 42, true},
		{"120x", 120, true},
		{"12.9", 12, true},
		{"-5", -5, true},
		{"+7", 7, true},
		{"  9  ", 9, true},
		{"0x10", 0, true}, // base 10: stops at 'x'
		{"007", 7, true},
		{"", 0, false},
		{"abc", 0, false},
		{"x9", 0, false},
		{"   ", 0, false},
		{"-", 0, false},
	}
	for _, tc := range cases {
		got, ok := jsParseInt10(tc.in)
		if ok != tc.wantOK || (ok && got != tc.want) {
			t.Fatalf("jsParseInt10(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}
