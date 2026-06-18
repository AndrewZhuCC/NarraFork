package chapters

import (
	"path/filepath"
	"testing"
)

// TestIsInsidePathContainmentSemantics pins the security-relevant path-containment
// helper used for compose-file resolution, copy src/dest validation, and worktree
// safety. A child strictly nested under base is inside; the base itself (rel ".")
// and any "../" escape or sibling-with-shared-prefix is not.
func TestIsInsidePathContainmentSemantics(t *testing.T) {
	base := filepath.FromSlash("/srv/repo/.worktrees")

	cases := []struct {
		name      string
		candidate string
		want      bool
	}{
		{"direct child", filepath.FromSlash("/srv/repo/.worktrees/feature"), true},
		{"nested child", filepath.FromSlash("/srv/repo/.worktrees/feature/src/main.go"), true},
		{"base itself is not inside", base, false},
		{"parent escape", filepath.FromSlash("/srv/repo/secret"), false},
		{"dotdot traversal", filepath.FromSlash("/srv/repo/.worktrees/../escape"), false},
		{"sibling with shared prefix", filepath.FromSlash("/srv/repo/.worktrees-evil/x"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isInsidePath(base, tc.candidate); got != tc.want {
				t.Fatalf("isInsidePath(%q, %q) = %v, want %v", base, tc.candidate, got, tc.want)
			}
		})
	}
}

// TestIsInsidePathMustResolvesRelativeInputs covers isInsidePathMust which absolves
// inputs before the containment check, so relative candidates are evaluated
// against the resolved base.
func TestIsInsidePathMustResolvesRelativeInputs(t *testing.T) {
	base := t.TempDir()
	inside := filepath.Join(base, "child", "file.txt")
	if !isInsidePathMust(base, inside) {
		t.Fatalf("expected %q inside %q", inside, base)
	}
	outside := filepath.Join(base, "..", "sibling.txt")
	if isInsidePathMust(base, outside) {
		t.Fatalf("expected %q outside %q", outside, base)
	}
}
