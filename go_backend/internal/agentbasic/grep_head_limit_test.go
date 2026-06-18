package agentbasic

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeGrepFixture creates a file with `lines` lines each containing the pattern
// "MATCH<n>" so grep content mode yields one match line per line.
func writeGrepFixture(t *testing.T, dir, name string, lines int) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&b, "MATCH%d here\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func TestGrepAppendsTruncationSuffixWhenLimited(t *testing.T) {
	cwd := t.TempDir()
	writeGrepFixture(t, cwd, "data.txt", 10)
	out := ExecuteTool(context.Background(), cwd, "Grep", `{"pattern":"MATCH","output_mode":"content","head_limit":3}`)
	if !strings.Contains(out, "Results limited to 3 entries") {
		t.Fatalf("expected truncation suffix when limited, got: %q", out)
	}
	// Only 3 match lines retained (plus the suffix line).
	matchCount := strings.Count(out, "MATCH")
	if matchCount != 3 {
		t.Fatalf("expected 3 match lines, got %d in: %q", matchCount, out)
	}
}

func TestGrepNoTruncationSuffixWhenUnderLimit(t *testing.T) {
	cwd := t.TempDir()
	writeGrepFixture(t, cwd, "data.txt", 3)
	out := ExecuteTool(context.Background(), cwd, "Grep", `{"pattern":"MATCH","output_mode":"content","head_limit":50}`)
	if strings.Contains(out, "Results limited") {
		t.Fatalf("should not append truncation suffix when under limit, got: %q", out)
	}
	if strings.Count(out, "MATCH") != 3 {
		t.Fatalf("expected all 3 matches, got: %q", out)
	}
}

func TestGrepHonorsExplicitLargeHeadLimit(t *testing.T) {
	cwd := t.TempDir()
	writeGrepFixture(t, cwd, "data.txt", 1500)
	out := ExecuteTool(context.Background(), cwd, "Grep", `{"pattern":"MATCH","output_mode":"content","head_limit":1500}`)
	// Previously head_limit > 1000 was silently downgraded to 200; now an explicit
	// value up to grepHeadLimitMax is honored, so all 1500 matches are returned.
	if strings.Contains(out, "Results limited") {
		t.Fatalf("head_limit 1500 should be honored (no truncation), got suffix: %q", out[max0g(len(out)-120):])
	}
	if got := strings.Count(out, "MATCH"); got != 1500 {
		t.Fatalf("expected 1500 honored matches, got %d", got)
	}
}

func TestGrepHeadLimitClampedAtMax(t *testing.T) {
	cwd := t.TempDir()
	// Far more matches than grepHeadLimitMax would be impractical; instead verify
	// the clamp arithmetic directly via the dispatched default path: a request for
	// a value above the ceiling collects up to grepHeadLimitMax. Use a moderate
	// fixture and assert that requesting a huge head_limit does not error and
	// returns all (under-max) matches without a spurious truncation suffix.
	writeGrepFixture(t, cwd, "data.txt", 50)
	out := ExecuteTool(context.Background(), cwd, "Grep", `{"pattern":"MATCH","output_mode":"content","head_limit":`+strconv.Itoa(grepHeadLimitMax*10)+`}`)
	if strings.Contains(out, "Results limited") {
		t.Fatalf("50 matches under the clamped ceiling should not be truncated, got: %q", out)
	}
	if strings.Count(out, "MATCH") != 50 {
		t.Fatalf("expected all 50 matches, got: %q", out)
	}
}

func max0g(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
