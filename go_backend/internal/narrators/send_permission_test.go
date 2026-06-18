package narrators

import "testing"

// TestPermissionModeRankForSendMatchesTS locks the Send-scope permission-mode
// ranking, parity-aligned with the TypeScript permissionModeRank: dontAsk=0,
// readOnly=1, default=2, acceptEdits=3, bypassPermissions=4, unknown->2. A
// narrator in plan mode without relaxedPlan is treated as readOnly (rank 1)
// regardless of its stored permissionMode. The ranking gates Send: a caller may
// only Send to targets whose rank is <= the caller's rank (every target).
func TestPermissionModeRankForSendMatchesTS(t *testing.T) {
	mk := func(mode string) Narrator { m := mode; return Narrator{PermissionMode: &m} }
	cases := []struct {
		mode string
		want int
	}{
		{"dontAsk", 0},
		{"readOnly", 1},
		{"default", 2},
		{"acceptEdits", 3},
		{"bypassPermissions", 4},
		{"", 2},      // empty -> default rank
		{"weird", 2}, // unknown -> default rank
	}
	for _, tc := range cases {
		if got := permissionModeRankForSend(mk(tc.mode)); got != tc.want {
			t.Fatalf("permissionModeRankForSend(%q)=%d want %d", tc.mode, got, tc.want)
		}
	}

	// Plan mode without relaxedPlan forces readOnly (rank 1) even if the stored
	// mode would otherwise rank higher (e.g. bypassPermissions=4).
	planStrict := mk("bypassPermissions")
	planStrict.PlanMode = true
	planStrict.RelaxedPlan = false
	if got := permissionModeRankForSend(planStrict); got != 1 {
		t.Fatalf("plan mode (non-relaxed) bypassPermissions rank=%d want 1 (readOnly)", got)
	}

	// Plan mode WITH relaxedPlan keeps the stored mode's rank.
	planRelaxed := mk("bypassPermissions")
	planRelaxed.PlanMode = true
	planRelaxed.RelaxedPlan = true
	if got := permissionModeRankForSend(planRelaxed); got != 4 {
		t.Fatalf("plan mode (relaxed) bypassPermissions rank=%d want 4", got)
	}

	// Ranking ordering sanity: a higher-privilege caller may target a
	// lower-or-equal-rank target, but not a higher-rank one.
	caller := permissionModeRankForSend(mk("default")) // 2
	if permissionModeRankForSend(mk("readOnly")) > caller {
		t.Fatal("readOnly target should be within a default caller's scope")
	}
	if permissionModeRankForSend(mk("bypassPermissions")) <= caller {
		t.Fatal("bypassPermissions target should be OUTSIDE a default caller's scope")
	}
}
