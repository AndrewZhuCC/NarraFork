package narrators

import (
	"testing"
	"time"

	"narrafork-go/internal/config"
)

// TestAgentFirstTokenTimeoutDefaultAndClamp locks the first-token-timeout
// resolution against TS parity:
//   - default (no setting / nil Settings) = 300000ms, mirroring TS
//     defaults.ts:52 firstTokenTimeoutMs:300_000 and loop.ts:2015
//     `config.firstTokenTimeoutMs ?? 300_000` (5 min).
//   - value <= 0 disables the timeout (returns 0), mirroring TS
//     `Math.max(0, ...)` + the `<= 0` disable branch.
//   - values above 600000 clamp to 600000 (the TS validator upper bound).
//
// Go previously used a 60000ms (1 min) fallback, which diverged from the
// current TS source after TS raised its default to 300000; that meant first
// tokens arriving between 60s and 300s were aborted+retried on Go but completed
// on TS for the same config/model.
func TestAgentFirstTokenTimeoutDefaultAndClamp(t *testing.T) {
	newServiceWith := func(t *testing.T, value any) *Service {
		t.Helper()
		t.Setenv("HOME", t.TempDir())
		settings, err := config.Load()
		if err != nil {
			t.Fatalf("load settings: %v", err)
		}
		if value != nil {
			if _, err := settings.Patch(map[string]any{
				"agent": map[string]any{"firstTokenTimeoutMs": value},
			}); err != nil {
				t.Fatalf("patch settings: %v", err)
			}
		}
		return &Service{Settings: settings}
	}

	// nil Settings → absolute fallback 300000ms.
	if got := (&Service{}).agentFirstTokenTimeout(); got != 300000*time.Millisecond {
		t.Fatalf("nil Settings fallback = %v, want 300000ms", got)
	}

	cases := []struct {
		name  string
		value any
		want  time.Duration
	}{
		{"default (built-in defaultRaw)", nil, 300000 * time.Millisecond},
		{"explicit 120000", float64(120000), 120000 * time.Millisecond},
		{"zero disables", float64(0), 0},
		{"negative disables", float64(-5), 0},
		{"clamp above 600000", float64(700000), 600000 * time.Millisecond},
		{"exactly 600000", float64(600000), 600000 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newServiceWith(t, tc.value)
			if got := svc.agentFirstTokenTimeout(); got != tc.want {
				t.Fatalf("agentFirstTokenTimeout(%v) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
