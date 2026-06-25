package narrators

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// This file replaces the previous naive string-splitting shell tokenizer with an
// AST-backed parser (mvdan.cc/sh/v3/syntax, pure Go, CGO-free) so the Go bash
// security analysis matches the TypeScript tree-sitter-based analyzeBashCommand
// (server/lib/agent/bash-analyze.ts). The previous splitShellCommands replaced
// `&& || ; |` with newlines and whitespace-split, which mis-parsed quoted
// strings (a `|` inside "..."), command substitution `$(...)`, and redirects.
//
// The AST approach mirrors tree-sitter semantics: each shell "command" maps to a
// syntax.CallExpr, and a token's text is the verbatim source slice of the word
// (matching tree-sitter node.text, which preserves quotes). Command substitution
// and subshells are walked too, so a catastrophic command hidden in `$(...)` is
// seen as its own command — closing the quote/subshell evasion gap.

// shellWordText returns the verbatim source text of a word, matching tree-sitter
// node.text (quotes preserved). Falls back to the literal value when offsets are
// unavailable.
func shellWordText(src string, w *syntax.Word) string {
	if w == nil {
		return ""
	}
	start := int(w.Pos().Offset())
	end := int(w.End().Offset())
	if start >= 0 && end <= len(src) && start < end {
		return src[start:end]
	}
	return w.Lit()
}

// parseShellAST parses a command into an mvdan/sh bash AST. Returns (nil, false)
// when parsing fails (e.g. genuinely malformed input) so callers can fall back to
// the conservative legacy tokenizer.
func parseShellAST(command string) (*syntax.File, bool) {
	if strings.TrimSpace(command) == "" {
		return nil, false
	}
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil || file == nil {
		return nil, false
	}
	return file, true
}

// astCommandTokens walks the AST and returns, for every CallExpr (including those
// nested inside command substitutions / subshells / pipelines / control flow),
// the verbatim token list of that command. This is the AST equivalent of the old
// splitShellCommands [][]string result, but quote/subshell/pipe-aware.
//
// To keep the existing token-based downstream helpers working unchanged, each
// command's tokens are augmented with:
//   - leading env-assignment tokens (`NAME=value`) from CallExpr.Assigns, so the
//     env-injection detectors that scan `NAME=` tokens still fire; and
//   - trailing redirect tokens (operator + target, e.g. `>` `out.txt` or
//     `2>/dev/null`) from the enclosing Stmt.Redirs, so redirect-target / path /
//     catastrophic-redirect / protected-path checks still fire.
//
// This mirrors what the legacy whitespace tokenizer produced for these forms,
// while gaining correct quoting/subshell/pipe handling from the AST.
func astCommandTokens(src string, file *syntax.File) [][]string {
	out := [][]string{}

	// Map each CallExpr to the redirects on its enclosing Stmt. A Stmt's Cmd is
	// the CallExpr; redirects (`> file`, `2>/dev/null`) live on the Stmt.
	stmtRedirs := map[*syntax.CallExpr][]*syntax.Redirect{}
	syntax.Walk(file, func(node syntax.Node) bool {
		if stmt, ok := node.(*syntax.Stmt); ok {
			if call, ok := stmt.Cmd.(*syntax.CallExpr); ok && len(stmt.Redirs) > 0 {
				stmtRedirs[call] = stmt.Redirs
			}
		}
		return true
	})

	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		// Pure assignment-only "commands" (e.g. `FOO=bar` with no args) still need
		// to surface their assignment tokens so the env-injection detector sees
		// them, but they have no command name. Emit the assignment tokens alone.
		tokens := make([]string, 0, len(call.Assigns)+len(call.Args)+2*len(stmtRedirs[call]))
		for _, a := range call.Assigns {
			tokens = append(tokens, shellAssignToken(src, a))
		}
		for _, w := range call.Args {
			tokens = append(tokens, shellWordText(src, w))
		}
		for _, r := range stmtRedirs[call] {
			tokens = append(tokens, shellRedirTokens(src, r)...)
		}
		if len(tokens) == 0 {
			return true
		}
		out = append(out, tokens)
		return true
	})
	return out
}

// shellAssignToken returns an assignment's verbatim `NAME=value` source text.
func shellAssignToken(src string, a *syntax.Assign) string {
	if a == nil {
		return ""
	}
	start := int(a.Pos().Offset())
	end := int(a.End().Offset())
	if start >= 0 && end <= len(src) && start < end {
		return src[start:end]
	}
	if a.Name != nil {
		return a.Name.Value + "="
	}
	return ""
}

// shellRedirTokens converts a redirect node into the operator/target token pair
// the legacy tokenizer produced. When the operator and target are adjacent in the
// source (e.g. `2>/dev/null`) they are emitted as a single token; when separated
// by whitespace (e.g. `> out.txt`) they are emitted as two tokens (`>`, `out.txt`)
// — both forms are what the existing shellRedirectPath helper already handles.
func shellRedirTokens(src string, r *syntax.Redirect) []string {
	if r == nil {
		return nil
	}
	start := int(r.Pos().Offset())
	end := int(r.End().Offset())
	if start >= 0 && end <= len(src) && start < end {
		return strings.Fields(src[start:end])
	}
	op := r.Op.String()
	if r.Word != nil {
		return []string{op, shellWordText(src, r.Word)}
	}
	return []string{op}
}

// astPipelineLastCommands returns the command name of the LAST stage of every
// top-level pipeline in the command, mirroring the TS detectPipeToShell, which
// inspects only the final command of each `pipeline` node. mvdan/sh models a
// pipeline `a | b | c` left-associatively as BinaryCmd{Op:Pipe, X:BinaryCmd{X:a,
// Y:b}, Y:c}; the OUTERMOST node's Y (`c`) is the true final stage. Because this
// is AST-based, a `|` inside quotes (e.g. echo "a | bash") is correctly NOT
// treated as a pipeline — the gap the legacy byte-scan had.
//
// Returns nil when parsing fails so callers can fall back to the legacy scan.
func astPipelineLastCommands(command string) ([]string, bool) {
	file, ok := parseShellAST(command)
	if !ok {
		return nil, false
	}
	// Mark every pipe BinaryCmd that appears as the X-child of another pipe; those
	// are inner nodes whose Y is an intermediate stage, not the pipeline's last.
	innerX := map[*syntax.BinaryCmd]bool{}
	syntax.Walk(file, func(node syntax.Node) bool {
		if bin, ok := node.(*syntax.BinaryCmd); ok && bin.Op == syntax.Pipe {
			if x, ok := bin.X.Cmd.(*syntax.BinaryCmd); ok && x.Op == syntax.Pipe {
				innerX[x] = true
			}
		}
		return true
	})
	names := []string{}
	syntax.Walk(file, func(node syntax.Node) bool {
		bin, ok := node.(*syntax.BinaryCmd)
		if !ok || bin.Op != syntax.Pipe || innerX[bin] {
			return true
		}
		if name := astStmtCommandName(command, bin.Y); name != "" {
			names = append(names, name)
		}
		return true
	})
	return names, true
}

// astStmtCommandName returns the command name (first arg) of a statement's
// CallExpr, or "" when the statement is not a simple command.
func astStmtCommandName(src string, stmt *syntax.Stmt) string {
	if stmt == nil {
		return ""
	}
	if call, ok := stmt.Cmd.(*syntax.CallExpr); ok && len(call.Args) > 0 {
		return shellWordText(src, call.Args[0])
	}
	return ""
}

// gitCloneFlagsWithValue mirrors TS GIT_CLONE_FLAGS_WITH_VALUE (bash-analyze.ts:1252):
// git clone/init flags that consume the following token as their value.
var gitCloneFlagsWithValue = map[string]bool{
	"-b": true, "--branch": true, "-o": true, "--origin": true,
	"--reference": true, "--reference-if-able": true, "--separate-git-dir": true,
	"--depth": true, "--shallow-since": true, "--shallow-exclude": true,
	"-j": true, "--jobs": true, "--filter": true, "--bundle-uri": true,
	"--template": true, "--config": true, "-c": true,
}

// gitWriteSubcommands mirrors TS GIT_WRITE_SUBCOMMANDS (bash-analyze.ts:1273):
// git subcommands that create/modify filesystem content.
var gitWriteSubcommands = map[string]bool{"clone": true, "init": true, "worktree": true}

// extractGitPaths ports the TS extractGitPaths (bash-analyze.ts:1287). git path
// semantics depend entirely on the subcommand, so the generic path heuristic
// cannot handle them: this extracts `git -C <dir>`, `git clone <url> [<dir>]`
// (plus a local-path repository), `git init [<dir>]`, and
// `git worktree add/move <path>`, and reports whether the subcommand writes to
// the filesystem. resolve resolves a token to an absolute path (cwd-relative),
// matching the Go resolvePermissionPath behavior.
func extractGitPaths(tokens []string, resolve func(string) string) (paths []string, isWrite bool) {
	// Find the subcommand, skipping git's own global options (-C <dir>,
	// --git-dir/--work-tree/--namespace <val>, and other -flags).
	subIdx := 1
	for subIdx < len(tokens) {
		t := tokens[subIdx]
		if t == "-C" && subIdx+1 < len(tokens) {
			paths = append(paths, resolve(tokens[subIdx+1]))
			subIdx += 2
			continue
		}
		if strings.HasPrefix(t, "-") {
			if (t == "--git-dir" || t == "--work-tree" || t == "--namespace") && subIdx+1 < len(tokens) {
				subIdx += 2
				continue
			}
			subIdx++
			continue
		}
		break
	}
	if subIdx >= len(tokens) {
		return paths, isWrite
	}
	sub := tokens[subIdx]
	if gitWriteSubcommands[sub] {
		isWrite = true
	}

	switch sub {
	case "clone":
		// git clone [options] <repository> [<directory>]
		nonFlag := []string{}
		skipNext := false
		for i := subIdx + 1; i < len(tokens); i++ {
			if skipNext {
				// A value flag's value may be absent (token-lost); if the next token
				// is itself a flag, don't consume it.
				if !strings.HasPrefix(tokens[i], "-") {
					skipNext = false
					continue
				}
				skipNext = false
			}
			arg := tokens[i]
			if strings.HasPrefix(arg, "-") {
				if gitCloneFlagsWithValue[arg] {
					skipNext = true
				}
				continue
			}
			nonFlag = append(nonFlag, arg)
		}
		if len(nonFlag) >= 2 {
			paths = append(paths, resolve(nonFlag[1]))
		}
		if len(nonFlag) >= 1 {
			repo := nonFlag[0]
			if !strings.Contains(repo, "://") && !strings.HasPrefix(repo, "git@") {
				paths = append(paths, resolve(repo))
			}
		}
	case "init":
		// git init [<directory>]
		nonFlag := []string{}
		for i := subIdx + 1; i < len(tokens); i++ {
			arg := tokens[i]
			if strings.HasPrefix(arg, "-") {
				if arg == "--template" || arg == "--separate-git-dir" {
					i++
				}
				continue
			}
			nonFlag = append(nonFlag, arg)
		}
		if len(nonFlag) >= 1 {
			paths = append(paths, resolve(nonFlag[0]))
		}
	case "worktree":
		worktreeSub := ""
		if subIdx+1 < len(tokens) {
			worktreeSub = tokens[subIdx+1]
		}
		if worktreeSub == "add" || worktreeSub == "move" {
			nonFlag := []string{}
			for i := subIdx + 2; i < len(tokens); i++ {
				arg := tokens[i]
				if strings.HasPrefix(arg, "-") {
					continue
				}
				nonFlag = append(nonFlag, arg)
			}
			if len(nonFlag) >= 1 {
				paths = append(paths, resolve(nonFlag[0]))
			}
			if worktreeSub == "move" && len(nonFlag) >= 2 {
				paths = append(paths, resolve(nonFlag[1]))
			}
		}
	}
	return paths, isWrite
}

// splitShellCommandsAST is the AST-backed replacement for the legacy
// splitShellCommands. On parse failure it falls back to the legacy splitter so
// behavior is never worse than before for inputs mvdan/sh cannot parse.
func splitShellCommandsAST(command string) [][]string {
	file, ok := parseShellAST(command)
	if !ok {
		return splitShellCommandsLegacy(command)
	}
	tokens := astCommandTokens(command, file)
	if len(tokens) == 0 {
		// Parsed but produced no CallExpr (e.g. only assignments or comments);
		// fall back so callers still see something reasonable.
		return splitShellCommandsLegacy(command)
	}
	return tokens
}

// splitShellCommandsLegacy is the original naive tokenizer, retained as a
// fallback for inputs the AST parser rejects.
func splitShellCommandsLegacy(command string) [][]string {
	command = strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n").Replace(command)
	out := [][]string{}
	for _, part := range strings.Split(command, "\n") {
		tokens := strings.Fields(strings.TrimSpace(part))
		if len(tokens) > 0 {
			out = append(out, tokens)
		}
	}
	return out
}
