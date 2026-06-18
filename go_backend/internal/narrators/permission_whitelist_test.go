package narrators

import (
	"path/filepath"
	"testing"
)

// TestPermissionLevelCoversMatchesTSHierarchy verifies the access-level coverage
// hierarchy mirrors the TypeScript allPathsWhitelisted level order
// ["readOnly", "readWrite", "full"] compared with >=: a rule grants a request
// when the rule level is at least the required level. Unknown levels (order 0)
// never cover.
func TestPermissionLevelCoversMatchesTSHierarchy(t *testing.T) {
	cases := []struct {
		level    string
		required string
		want     bool
	}{
		{"readOnly", "readOnly", true},
		{"readWrite", "readOnly", true},
		{"full", "readOnly", true},
		{"readOnly", "readWrite", false},
		{"readWrite", "readWrite", true},
		{"full", "readWrite", true},
		{"readOnly", "full", false},
		{"readWrite", "full", false},
		{"full", "full", true},
		{"superuser", "readOnly", false}, // unknown level -> order 0 -> never covers
		{"", "readOnly", false},
	}
	for _, tc := range cases {
		if got := permissionLevelCovers(tc.level, tc.required); got != tc.want {
			t.Fatalf("permissionLevelCovers(%q,%q)=%v want %v", tc.level, tc.required, got, tc.want)
		}
	}
}

// TestPathInsideOrEqualRejectsTraversalAndPrefixSiblings verifies the path
// containment helper used by the tool-permission whitelist: a target equal to or
// nested under base is inside; a traversal escape or a prefix-sibling (/repo vs
// /repo-evil) is not.
func TestPathInsideOrEqualRejectsTraversalAndPrefixSiblings(t *testing.T) {
	base := filepath.FromSlash("/work/repo")
	cases := []struct {
		target string
		want   bool
	}{
		{filepath.FromSlash("/work/repo"), true},
		{filepath.FromSlash("/work/repo/src/main.go"), true},
		{filepath.FromSlash("/work/repo/."), true},
		{filepath.FromSlash("/work"), false},
		{filepath.FromSlash("/work/repo/../secret"), false},
		{filepath.FromSlash("/work/repo-evil/file"), false}, // prefix sibling must not match
		{filepath.FromSlash("/other/place"), false},
	}
	for _, tc := range cases {
		if got := pathInsideOrEqual(base, tc.target); got != tc.want {
			t.Fatalf("pathInsideOrEqual(%q,%q)=%v want %v", base, tc.target, got, tc.want)
		}
	}
}

// TestAllPathsWhitelistedRequiresEveryPathAndSufficientLevel verifies the
// all-or-nothing multi-path whitelist semantics (mirrors TS paths.every): every
// requested path must be covered by some rule at a sufficient level; if any path
// is uncovered or under-leveled, the whole request is denied. Empty paths or
// empty rules deny.
func TestAllPathsWhitelistedRequiresEveryPathAndSufficientLevel(t *testing.T) {
	cwd := filepath.FromSlash("/work/repo")
	rules := []permissionPathRule{
		{Path: filepath.FromSlash("/work/repo/src"), Level: "readWrite"},
		{Path: filepath.FromSlash("/work/repo/docs"), Level: "readOnly"},
	}

	// Both paths under readWrite rule, required readWrite -> allowed.
	if !allPathsWhitelisted(cwd, []string{"src/a.go", "src/b.go"}, rules, "readWrite") {
		t.Fatal("two paths under readWrite rule should be allowed for readWrite")
	}
	// docs is readOnly only; requesting readWrite on docs -> denied.
	if allPathsWhitelisted(cwd, []string{"docs/readme.md"}, rules, "readWrite") {
		t.Fatal("readOnly rule must not grant readWrite")
	}
	// docs readOnly request -> allowed.
	if !allPathsWhitelisted(cwd, []string{"docs/readme.md"}, rules, "readOnly") {
		t.Fatal("readOnly rule should grant readOnly")
	}
	// Mixed: one covered, one outside any rule -> denied (every must hold).
	if allPathsWhitelisted(cwd, []string{"src/a.go", "elsewhere/x"}, rules, "readWrite") {
		t.Fatal("a path outside all rules must deny the whole request")
	}
	// Traversal escape from a covered prefix -> denied.
	if allPathsWhitelisted(cwd, []string{"src/../../secret"}, rules, "readWrite") {
		t.Fatal("traversal escaping the rule prefix must be denied")
	}
	// Empty paths or empty rules -> denied.
	if allPathsWhitelisted(cwd, nil, rules, "readOnly") {
		t.Fatal("empty paths must deny")
	}
	if allPathsWhitelisted(cwd, []string{"src/a.go"}, nil, "readOnly") {
		t.Fatal("empty rules must deny")
	}
}
