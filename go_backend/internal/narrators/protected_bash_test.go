package narrators

import (
	"path/filepath"
	"testing"
)

// TestProtectedBashCommandDenyBlocksGitAndStructuralPaths locks the protected-path
// hard-deny for destructive bash commands (parity with the TypeScript
// resolveProtectedPathDeny / isStructuralPath): destructive commands (rm, rmdir,
// shred) targeting a .git directory, the project root, an ancestor of the
// project root, or the .worktrees directory are denied, as are shell redirects
// writing into .git. Destructive ops on ordinary in-project paths are NOT denied
// here (they flow to the regular permission/danger path).
func TestProtectedBashCommandDenyBlocksGitAndStructuralPaths(t *testing.T) {
	projectGit := filepath.FromSlash("/work/project")
	cwd := filepath.Join(projectGit, ".worktrees", "wt")

	denied := []struct {
		name    string
		command string
	}{
		{"rm inside .git", "rm -rf " + filepath.Join(cwd, ".git", "objects")},
		{"rm the .git dir", "rm -rf " + filepath.Join(cwd, ".git")},
		{"redirect into .git", "echo x > " + filepath.Join(cwd, ".git", "config")},
		{"rm project root", "rm -rf " + projectGit},
		{"rm ancestor of project root", "rm -rf " + filepath.Dir(projectGit)},
		{"rm worktrees dir", "rm -rf " + filepath.Join(projectGit, ".worktrees")},
		{"rmdir project root", "rmdir " + projectGit},
		{"shred .git file", "shred " + filepath.Join(cwd, ".git", "index")},
	}
	for _, tc := range denied {
		t.Run("deny/"+tc.name, func(t *testing.T) {
			if reason := protectedBashCommandDeny(tc.command, cwd, projectGit); reason == "" {
				t.Fatalf("expected protected-path deny for %q, got allow", tc.command)
			}
		})
	}

	allowed := []struct {
		name    string
		command string
	}{
		{"rm in-project file", "rm -rf " + filepath.Join(cwd, "build")},
		{"rm relative file", "rm scratch.txt"},
		{"redirect to in-project file", "echo x > " + filepath.Join(cwd, "out.log")},
		{"cat .git (read, not destructive)", "cat " + filepath.Join(cwd, ".git", "config")},
		{"ls project root (read)", "ls -la " + projectGit},
	}
	for _, tc := range allowed {
		t.Run("allow/"+tc.name, func(t *testing.T) {
			if reason := protectedBashCommandDeny(tc.command, cwd, projectGit); reason != "" {
				t.Fatalf("did not expect protected-path deny for %q, got: %s", tc.command, reason)
			}
		})
	}
}

// TestStructuralProtectedPathReasonClassification locks the structural-path
// classifier: project root, ancestor of project root, and the .worktrees
// directory are protected; in-project subpaths and unrelated paths are not.
func TestStructuralProtectedPathReasonClassification(t *testing.T) {
	projectGit := filepath.FromSlash("/work/project")
	cases := []struct {
		path      string
		protected bool
	}{
		{projectGit, true},
		{filepath.Dir(projectGit), true},                       // /work ancestor
		{filepath.FromSlash("/"), true},                        // root ancestor
		{filepath.Join(projectGit, ".worktrees"), true},        // worktrees dir
		{filepath.Join(projectGit, "src"), false},              // in-project subpath
		{filepath.Join(projectGit, ".worktrees", "wt"), false}, // inside a worktree
		{filepath.FromSlash("/elsewhere"), false},              // unrelated
	}
	for _, tc := range cases {
		got := structuralProtectedPathReason(tc.path, projectGit) != ""
		if got != tc.protected {
			t.Fatalf("structuralProtectedPathReason(%q) protected=%v want %v", tc.path, got, tc.protected)
		}
	}
}
