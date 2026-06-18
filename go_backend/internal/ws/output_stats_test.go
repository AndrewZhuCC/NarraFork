package ws

import (
	"strings"
	"testing"
	"time"
)

// TestOutputStatsRecordAndRateMatchesTS verifies the output-stats tracker mirrors
// the TS output-stats.ts: totalChars accumulates, charsPerSec is
// round((sum/3)*10)/10 over a 3s window, chunk lengths are UTF-16 code units, and
// non-positive counts are ignored.
func TestOutputStatsRecordAndRateMatchesTS(t *testing.T) {
	h := NewHub()

	// Non-positive counts ignored.
	h.RecordOutputChunk(0)
	h.RecordOutputChunk(-5)
	if rate, total := h.outputStatsSnapshot(); rate != 0 || total != 0 {
		t.Fatalf("expected zero rate/total after non-positive records, got rate=%v total=%d", rate, total)
	}

	// 900 chars across the window -> 900/3 = 300.0 chars/sec.
	h.RecordOutputChunk(300)
	h.RecordOutputChunk(600)
	rate, total := h.outputStatsSnapshot()
	if total != 900 {
		t.Fatalf("totalChars = %d, want 900", total)
	}
	if rate != 300.0 {
		t.Fatalf("charsPerSec = %v, want 300.0 (900/3)", rate)
	}

	// RecordOutputText measures UTF-16 code units: 10 CJK chars = 10 units.
	h.RecordOutputText(strings.Repeat("章", 10))
	_, total = h.outputStatsSnapshot()
	if total != 910 {
		t.Fatalf("totalChars after 10 CJK = %d, want 910 (UTF-16 units)", total)
	}
}

// TestOutputStatsRateRoundsToOneDecimal verifies charsPerSec rounds to one decimal
// place like TS Math.round((sum/3)*10)/10.
func TestOutputStatsRateRoundsToOneDecimal(t *testing.T) {
	h := NewHub()
	// 100 chars -> 100/3 = 33.333... -> round to 33.3.
	h.RecordOutputChunk(100)
	rate, _ := h.outputStatsSnapshot()
	if rate != 33.3 {
		t.Fatalf("charsPerSec = %v, want 33.3 (round(100/3,1))", rate)
	}
}

// TestOutputStatsWindowPrunesOldChunks verifies chunks older than the 3s window
// drop out of the rate (totalChars still accumulates).
func TestOutputStatsWindowPrunesOldChunks(t *testing.T) {
	h := NewHub()
	// Inject an old chunk directly (older than the 3s window).
	h.outputStats.mu.Lock()
	h.outputStats.totalChars = 600
	h.outputStats.chunks = []outputStatsChunk{{chars: 600, ts: time.Now().Add(-10 * time.Second)}}
	h.outputStats.mu.Unlock()
	rate, total := h.outputStatsSnapshot()
	if rate != 0 {
		t.Fatalf("stale chunk should not count toward rate, got %v", rate)
	}
	if total != 600 {
		t.Fatalf("totalChars should persist across window pruning, got %d", total)
	}
}

// TestOutputStatsSubscriberLifecycleManagesTicker verifies subscriber add/remove
// starts and stops the broadcast ticker (mirrors TS add/removeStatsSubscriber).
func TestOutputStatsSubscriberLifecycleManagesTicker(t *testing.T) {
	h := NewHub()
	if h.outputStats.ticker != nil {
		t.Fatalf("ticker should be nil before any subscriber")
	}
	h.addOutputStatsSubscriber()
	h.addOutputStatsSubscriber()
	h.outputStats.mu.Lock()
	count := h.outputStats.subscriberCount
	running := h.outputStats.ticker != nil
	h.outputStats.mu.Unlock()
	if count != 2 || !running {
		t.Fatalf("expected 2 subscribers + running ticker, got count=%d running=%v", count, running)
	}
	h.removeOutputStatsSubscriber()
	h.removeOutputStatsSubscriber()
	h.outputStats.mu.Lock()
	count = h.outputStats.subscriberCount
	running = h.outputStats.ticker != nil
	h.outputStats.mu.Unlock()
	if count != 0 || running {
		t.Fatalf("expected 0 subscribers + stopped ticker, got count=%d running=%v", count, running)
	}
}
