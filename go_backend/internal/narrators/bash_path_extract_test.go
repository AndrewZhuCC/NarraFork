package narrators

import (
	"path/filepath"
	"sort"
	"testing"
)

// TestBashCommandFilePathsExtractsRedirectsAndPathArgs locks the bash command
// path extraction used by the blacklist/whitelist permission gate. It is Go's
// own heuristic (the TypeScript side uses a WASM bash parser's pre-extracted
// _filePaths), so this test documents the heuristic's deterministic behavior:
// redirect targets (>, >>, 2>, &>, and attached forms like >out) and path-like
// argument tokens (absolute, ./relative, or containing '/') are extracted and
// resolved against the tool cwd; bare non-path tokens (e.g. "secret" with no
// slash) and flags are NOT extracted. The heuristic may under-extract bare
// non-path-like filenames compared to the TS parser; dangerous commands are
// independently caught by the command-pattern / catastrophic denies.
func TestBashCommandFilePathsExtractsRedirectsAndPathArgs(t *testing.T) {
	cwd := filepath.FromSlash("/work/repo")
	abs := func(rel string) string { return filepath.Join(cwd, rel) }

	cases := []struct {
		name    string
		command string
		want    []string
	}{
		{
			"redirect spaced",
			"echo hi > out/log.txt",
			[]string{abs("out/log.txt")},
		},
		{
			// Bare (non-slash) redirect target is extracted ONLY via the redirect
			// logic, since looksLikeShellPathToken would skip a slashless token.
			"redirect bare target",
			"echo hi > bare.log",
			[]string{abs("bare.log")},
		},
		{
			"redirect attached",
			"echo hi >./logs/a.txt",
			[]string{abs("logs/a.txt")},
		},
		{
			"append and stderr redirects",
			"cmd >> a/x.log 2> b/err.log",
			[]string{abs("a/x.log"), abs("b/err.log")},
		},
		{
			"path-like arg extracted",
			"cat ./src/main.go",
			[]string{abs("src/main.go")},
		},
		{
			"absolute path arg",
			"cat /etc/passwd",
			[]string{filepath.FromSlash("/etc/passwd")},
		},
		{
			"bare non-path token not extracted",
			"echo hello",
			nil,
		},
		{
			"flags not extracted",
			"ls -la",
			nil,
		},
		{
			"multiple subcommands",
			"cat a/x && cat b/y",
			[]string{abs("a/x"), abs("b/y")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bashCommandFilePaths(cwd, tc.command)
			if !equalStringSetsBash(got, tc.want) {
				t.Fatalf("bashCommandFilePaths(%q)=%#v want %#v", tc.command, got, tc.want)
			}
		})
	}
}

// TestLooksLikeShellPathTokenHeuristic locks the path-token heuristic: only
// absolute, dot-relative, or slash-containing tokens (without '=' and not flags
// or shell operators) are treated as paths.
func TestLooksLikeShellPathTokenHeuristic(t *testing.T) {
	yes := []string{"/etc/passwd", "./a.txt", "../b", "src/main.go", "a/b/c"}
	no := []string{"echo", "hello", "-la", "--force", "&&", "||", ";", "|", ">", "KEY=val", ""}
	for _, tok := range yes {
		if !looksLikeShellPathToken(tok) {
			t.Fatalf("looksLikeShellPathToken(%q)=false want true", tok)
		}
	}
	for _, tok := range no {
		if looksLikeShellPathToken(tok) {
			t.Fatalf("looksLikeShellPathToken(%q)=true want false", tok)
		}
	}
}

func equalStringSetsBash(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string{}, a...)
	bc := append([]string{}, b...)
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}
