package chapters

import (
	"strings"
	"testing"
)

// TestBuildConflictResolutionPromptBaseMatchesTS pins the base merge ai-resolve
// conflict prompt to TS prompt-i18n.ts conflictResolution (W803). No merge context
// → base variant in en/zh-CN with the TS numbered steps, conflict-marker
// explanation, and the "Do NOT run git add or git commit" instruction (which
// matches Go runtime: the model only resolves, the ai-resolve success path
// auto-commits).
func TestBuildConflictResolutionPromptBaseMatchesTS(t *testing.T) {
	en := buildConflictResolutionPromptGo([]string{"a.txt", "dir/b.go"}, "feature", "main", "en", mergeSummaryContext{})
	for _, want := range []string{
		`A git merge from branch "feature" into "main" has produced conflicts in the following files:`,
		"  - a.txt",
		"  - dir/b.go",
		"The conflict markers (<<<<<<< HEAD, =======, >>>>>>>) are already present",
		"Do NOT run git add or git commit — just resolve the conflicts in the files.",
	} {
		if !strings.Contains(en, want) {
			t.Fatalf("base en prompt missing %q\ngot:\n%s", want, en)
		}
	}
	if strings.Contains(en, "Change statistics") {
		t.Fatalf("base prompt must NOT include the enhanced 'Change statistics' section:\n%s", en)
	}

	zh := buildConflictResolutionPromptGo([]string{"a.txt"}, "feature", "main", "zh-CN", mergeSummaryContext{})
	for _, want := range []string{
		`从分支 "feature" 合并到 "main" 时，以下文件产生了冲突：`,
		"  - a.txt",
		"不要运行 git add 或 git commit",
	} {
		if !strings.Contains(zh, want) {
			t.Fatalf("base zh prompt missing %q\ngot:\n%s", want, zh)
		}
	}
}

// TestBuildConflictResolutionPromptEnhancedMatchesTS pins the enhanced variant
// (TS conflictResolutionEnhanced): when merge context carries commit subjects or a
// diff stat, the prompt includes the change summary and statistics so the model
// can understand each side's intent.
func TestBuildConflictResolutionPromptEnhancedMatchesTS(t *testing.T) {
	ctx := mergeSummaryContext{Commits: []string{"feat: add x", "fix: y"}, DiffStat: "a.txt | 3 +++", Collected: true}
	en := buildConflictResolutionPromptGo([]string{"a.txt"}, "feature", "main", "en", ctx)
	for _, want := range []string{
		`A git merge from branch "feature" into "main" has produced conflicts.`,
		"Source branch change summary (commit messages):",
		"feat: add x",
		"fix: y",
		"Change statistics:",
		"a.txt | 3 +++",
		"Conflicting files:",
		"  - a.txt",
		"Use the commit messages and change statistics above to understand the intent of each side",
		"Do NOT run git add or git commit — just resolve the conflicts in the files.",
	} {
		if !strings.Contains(en, want) {
			t.Fatalf("enhanced en prompt missing %q\ngot:\n%s", want, en)
		}
	}

	zh := buildConflictResolutionPromptGo([]string{"a.txt"}, "feature", "main", "zh-CN", ctx)
	for _, want := range []string{
		"源分支的变更摘要（commit messages）：",
		"变更统计：",
		"a.txt | 3 +++",
		"结合上方的 commit messages 和变更统计，理解双方的变更意图",
	} {
		if !strings.Contains(zh, want) {
			t.Fatalf("enhanced zh prompt missing %q\ngot:\n%s", want, zh)
		}
	}
}

// TestBuildConflictResolutionPromptEmptyContextFallsBackToBase verifies that a
// collected-but-empty merge context (no commits and no diff stat) falls back to
// the base variant, matching TS (enhanced only when commits.length > 0 || diffStat).
func TestBuildConflictResolutionPromptEmptyContextFallsBackToBase(t *testing.T) {
	en := buildConflictResolutionPromptGo([]string{"a.txt"}, "feature", "main", "en", mergeSummaryContext{Collected: true, Commits: nil, DiffStat: "   "})
	if strings.Contains(en, "Change statistics") || strings.Contains(en, "Source branch change summary") {
		t.Fatalf("empty merge context must fall back to base variant, got enhanced:\n%s", en)
	}
	if !strings.Contains(en, "has produced conflicts in the following files:") {
		t.Fatalf("empty merge context should produce the base prompt, got:\n%s", en)
	}
}

// TestBuildConflictResolutionPromptFileListFormatMatchesTS pins the file-list
// format to TS conflictFiles.map(f => `  - ${f}`).join("\n"), including the empty
// list case (empty string, no placeholder line).
func TestBuildConflictResolutionPromptFileListFormatMatchesTS(t *testing.T) {
	empty := buildConflictResolutionPromptGo(nil, "feature", "main", "en", mergeSummaryContext{})
	if strings.Contains(empty, "  - ") {
		t.Fatalf("empty conflict file list must not emit any '  - ' line:\n%s", empty)
	}
}
