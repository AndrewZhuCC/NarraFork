package simplecontent

import (
	"path/filepath"
	"testing"
)

// TestIsInsidePathSkillContainment pins the path-containment helper used to keep
// skill companion-file reads within the skill directory. Unlike the chapters
// variant, this one treats an exact root==target match as inside (the skill dir
// itself is a valid root), while "../" escapes and sibling shared-prefix paths
// are rejected.
func TestIsInsidePathSkillContainment(t *testing.T) {
	root := filepath.FromSlash("/skills/my-skill")

	cases := []struct {
		name   string
		target string
		want   bool
	}{
		{"exact root is inside", root, true},
		{"direct child", filepath.FromSlash("/skills/my-skill/SKILL.md"), true},
		{"nested child", filepath.FromSlash("/skills/my-skill/assets/diagram.png"), true},
		{"parent escape", filepath.FromSlash("/skills/other"), false},
		{"dotdot traversal", filepath.FromSlash("/skills/my-skill/../my-skill-evil/x"), false},
		{"sibling shared prefix", filepath.FromSlash("/skills/my-skill-evil/x"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isInsidePath(root, tc.target); got != tc.want {
				t.Fatalf("isInsidePath(%q, %q) = %v, want %v", root, tc.target, got, tc.want)
			}
		})
	}
}
