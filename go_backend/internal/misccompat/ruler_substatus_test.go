package misccompat

import (
	"reflect"
	"testing"
)

// TestParseRulerSubstatusMatchesTS pins parseRulerSubstatus to the TypeScript
// parseSubstatus (server/lib/narrator-utils.ts): only a JSON array of strings
// yields tags; null/empty and ANY parse failure or non-array value yields no
// tags. Crucially it must NOT lenient-split a non-JSON CSV string (the prior Go
// fallback returned ["error","unread"] for "error,unread", diverging from TS []).
func TestParseRulerSubstatusMatchesTS(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", nil},
		{"whitespace", "   ", nil},
		{"literal-null", "null", nil},
		{"empty-array", "[]", []string{}},
		{"single", `["error"]`, []string{"error"}},
		{"multi", `["manual_override","unread"]`, []string{"manual_override", "unread"}},
		// Non-JSON CSV must NOT be lenient-split (TS returns []).
		{"csv-not-split", "error,unread", nil},
		{"bare-word", "error", nil},
		// Non-array JSON values yield no tags (TS `Array.isArray` guard).
		{"json-object", `{"a":1}`, nil},
		{"json-string", `"error"`, nil},
		{"json-number", `5`, nil},
		// Array of non-strings fails []string unmarshal -> nil (TS would keep the
		// array but .includes(<status string>) never matches, so display status is
		// unaffected either way).
		{"array-of-numbers", `[1,2]`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRulerSubstatus(tc.raw)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseRulerSubstatus(%q) = %#v, want %#v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestResolveRulerNarratorDisplayStatusMatchesTS pins the display-status
// resolution to the TS resolveNarratorDisplayStatus (server/routes/ruler.ts):
// reflecting wins; working/waiting pass through; any non-idle status passes
// through; for idle the priority is error > suspended > manual_override >
// interrupted > unread, else idle. With the parseRulerSubstatus fix a malformed
// CSV substatus no longer promotes idle to an error/unread display.
func TestResolveRulerNarratorDisplayStatusMatchesTS(t *testing.T) {
	cases := []struct {
		name      string
		status    string
		substatus string
		want      string
	}{
		{"empty-status", "", `["error"]`, ""},
		{"reflecting-wins-over-working", "working", `["reflecting"]`, "reflecting"},
		{"reflecting-wins-over-idle-error", "idle", `["reflecting","error"]`, "reflecting"},
		{"working-passthrough", "working", `[]`, "working"},
		{"waiting-passthrough", "waiting", `[]`, "waiting"},
		{"non-idle-passthrough", "merged", `["unread"]`, "merged"},
		{"idle-error", "idle", `["error"]`, "error"},
		{"idle-priority-error-over-unread", "idle", `["unread","error"]`, "error"},
		{"idle-suspended", "idle", `["suspended"]`, "suspended"},
		{"idle-manual-override", "idle", `["manual_override"]`, "manual_override"},
		{"idle-interrupted", "idle", `["interrupted"]`, "interrupted"},
		{"idle-unread", "idle", `["unread"]`, "unread"},
		{"idle-no-tags", "idle", `[]`, "idle"},
		// Malformed CSV substatus: TS parseSubstatus returns [] so idle stays idle.
		{"idle-malformed-csv-stays-idle", "idle", "error,unread", "idle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveRulerNarratorDisplayStatus(tc.status, tc.substatus)
			if got != tc.want {
				t.Fatalf("resolveRulerNarratorDisplayStatus(%q,%q) = %q, want %q", tc.status, tc.substatus, got, tc.want)
			}
		})
	}
}
