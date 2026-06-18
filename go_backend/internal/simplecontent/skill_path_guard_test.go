package simplecontent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAssertSkillPathInsideProjectRejectsTraversal verifies the project-skill
// path containment guard (mirrors the TypeScript "Skill path must be inside the
// project repository" check): a target inside the repo is accepted, while a
// path-traversal target that escapes the repo root is rejected with 400.
func TestAssertSkillPathInsideProjectRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".skills"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Inside the repo -> accepted.
	if err := assertSkillPathInsideProject(root, filepath.Join(root, ".skills", "my-skill")); err != nil {
		t.Fatalf("inside-repo target should be accepted, got %v", err)
	}

	// Traversal escaping the repo root -> rejected.
	escape := filepath.Join(root, "..", "outside-skill")
	err := assertSkillPathInsideProject(root, escape)
	if err == nil {
		t.Fatalf("traversal target %q should be rejected", escape)
	}
	if !strings.Contains(err.Error(), "inside the project repository") {
		t.Fatalf("unexpected error for traversal: %v", err)
	}

	// A sibling directory sharing a name prefix must not be treated as inside
	// (guards against the "/repo" vs "/repo-evil" prefix-match pitfall).
	sibling := root + "-evil"
	if err := assertSkillPathInsideProject(root, filepath.Join(sibling, "skill")); err == nil {
		t.Fatalf("prefix-sibling path %q should be rejected", sibling)
	}
}

// TestAssertNoSkillSymlinkAncestorRejectsSymlinkComponent verifies the symlink
// ancestor guard (mirrors the TypeScript assertNoSymlinkAncestor): if any
// existing path component between the project root and the target is a symlink,
// it is rejected; an all-real path is accepted.
func TestAssertNoSkillSymlinkAncestorRejectsSymlinkComponent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is unreliable on Windows test runners")
	}
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}

	// All-real ancestor chain -> accepted.
	if err := assertNoSkillSymlinkAncestor(root, filepath.Join(realDir, "skill")); err != nil {
		t.Fatalf("all-real ancestor chain should be accepted, got %v", err)
	}

	// Introduce a symlinked ancestor: root/linked -> root/real, target under it.
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(realDir, linked); err != nil {
		t.Skipf("cannot create symlink in test env: %v", err)
	}
	err := assertNoSkillSymlinkAncestor(root, filepath.Join(linked, "skill"))
	if err == nil {
		t.Fatalf("symlinked ancestor %q should be rejected", linked)
	}
	if !strings.Contains(err.Error(), "symbolic links") {
		t.Fatalf("unexpected error for symlink ancestor: %v", err)
	}
}
