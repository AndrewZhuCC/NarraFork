package narrators

import (
	"strings"
	"testing"

	"narrafork-go/internal/agentbasic"
)

// TestPlanModeFileRedirectedNoticeBilingual pins the verbatim model-facing notice
// to the TS tool.planModeFileRedirected i18n message (i18n.ts:147-150), with the
// originalPath/planFile params substituted between the localized static segments.
func TestPlanModeFileRedirectedNoticeBilingual(t *testing.T) {
	en := planModeFileRedirectedNotice("en", "IMPLEMENTATION.md", ".narrafork/plan-abc.md")
	want := `[Plan Mode] Note: Your write target was redirected from "IMPLEMENTATION.md" to the designated plan file ".narrafork/plan-abc.md". In plan mode, please use the exact plan file path directly.`
	if en != want {
		t.Fatalf("en notice mismatch:\n got %q\nwant %q", en, want)
	}
	zh := planModeFileRedirectedNotice("zh-CN", "IMPLEMENTATION.md", ".narrafork/plan-abc.md")
	wantZh := `[计划模式] 注意：你的写入目标已从 "IMPLEMENTATION.md" 重定向到指定的计划文件 ".narrafork/plan-abc.md"。在计划模式下，请直接使用正确的计划文件路径。`
	if zh != wantZh {
		t.Fatalf("zh-CN notice mismatch:\n got %q\nwant %q", zh, wantZh)
	}
	// Unknown locale falls back to English.
	if got := planModeFileRedirectedNotice("fr", "a.md", "b.md"); !strings.HasPrefix(got, "[Plan Mode] Note:") {
		t.Fatalf("unknown locale should fall back to en, got %q", got)
	}
}

// TestPlanModeToolRedirectUsesLocaleNotice verifies planModeToolRedirect redirects
// a markdown Write to the designated plan file AND returns the locale-resolved
// notice (model-facing), threading the owner locale through from the caller
// (s.toolLocale). en and zh-CN must produce the TS-aligned localized notice.
func TestPlanModeToolRedirectUsesLocaleNotice(t *testing.T) {
	planID := "plan1234"
	narrator := Narrator{PlanMode: true, PlanFileID: &planID}
	cwd := "/work"
	newCall := func() agentbasic.ToolCall {
		call := agentbasic.ToolCall{}
		call.Function.Name = "Write"
		call.Function.Arguments = `{"file_path":"IMPLEMENTATION.md","content":"# Plan"}`
		return call
	}

	// English owner locale.
	redirected, notice, ok := planModeToolRedirect(cwd, narrator, newCall(), "en")
	if !ok {
		t.Fatalf("expected markdown write to be redirected")
	}
	if !strings.Contains(redirected.Function.Arguments, ".narrafork/plan-plan1234.md") {
		t.Fatalf("expected redirect to plan file, args=%q", redirected.Function.Arguments)
	}
	if !strings.HasPrefix(notice, "[Plan Mode] Note: Your write target was redirected") {
		t.Fatalf("en notice not TS-aligned: %q", notice)
	}
	if !strings.Contains(notice, `"IMPLEMENTATION.md"`) || !strings.Contains(notice, `".narrafork/plan-plan1234.md"`) {
		t.Fatalf("en notice missing substituted paths: %q", notice)
	}

	// zh-CN owner locale.
	_, zhNotice, ok := planModeToolRedirect(cwd, narrator, newCall(), "zh-CN")
	if !ok {
		t.Fatalf("expected markdown write to be redirected (zh)")
	}
	if !strings.HasPrefix(zhNotice, "[计划模式] 注意：你的写入目标已从") {
		t.Fatalf("zh notice not TS-aligned: %q", zhNotice)
	}
	if strings.Contains(zhNotice, "Your write target was redirected") {
		t.Fatalf("zh-CN owner must not get English notice: %q", zhNotice)
	}
}
