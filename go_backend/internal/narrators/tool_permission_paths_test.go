package narrators

import (
	"reflect"
	"testing"
)

// TestToolPermissionPathsMatchesTSExtractToolPaths verifies the tool->paths
// extraction used by blacklist/whitelist path enforcement mirrors the
// TypeScript extractToolPaths, in particular the Browser screenshot case that
// was previously missing in Go (so a Browser screenshot bypassed path-permission
// gating). Tool names are lowercased before this function is called.
func TestToolPermissionPathsMatchesTSExtractToolPaths(t *testing.T) {
	cwd := "/work/repo"
	cases := []struct {
		name string
		tool string
		args map[string]any
		want []string
	}{
		{"read file_path", "read", map[string]any{"file_path": "/etc/passwd"}, []string{"/etc/passwd"}},
		{"write file_path", "write", map[string]any{"file_path": "out.txt"}, []string{"out.txt"}},
		{"edit file_path", "edit", map[string]any{"file_path": "a.go"}, []string{"a.go"}},
		{"notebookedit file_path", "notebookedit", map[string]any{"file_path": "n.ipynb"}, []string{"n.ipynb"}},
		{"read missing file_path", "read", map[string]any{}, nil},
		{"glob with path", "glob", map[string]any{"path": "src"}, []string{"src"}},
		{"grep default path", "grep", map[string]any{}, []string{"."}},
		// Browser screenshot file_path must be extracted (parity fix).
		{"browser screenshot path", "browser", map[string]any{"action": "screenshot", "file_path": "/tmp/shot.png"}, []string{"/tmp/shot.png"}},
		// Non-screenshot browser actions write no file -> no path.
		{"browser non-screenshot", "browser", map[string]any{"action": "click", "file_path": "/tmp/x"}, nil},
		{"browser screenshot no file_path", "browser", map[string]any{"action": "screenshot"}, nil},
		// Unknown tools -> no paths.
		{"unknown tool", "askuserquestion", map[string]any{"file_path": "/x"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toolPermissionPaths(cwd, tc.tool, tc.args)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("toolPermissionPaths(%q,%v)=%#v want %#v", tc.tool, tc.args, got, tc.want)
			}
		})
	}
}
