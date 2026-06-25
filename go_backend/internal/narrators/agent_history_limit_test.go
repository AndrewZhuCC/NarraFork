package narrators

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestAgentHistoryLoadsAllMessagesSinceCompact verifies that agentHistory now
// loads EVERY message after the last compact boundary (matching the TS
// buildHistory/getMessagesSinceLastCompact in server/services/narrator-messages.ts),
// rather than the former Go-only 80/200 cap that silently truncated. It still
// reads in bounded cursor pages (agentHistoryBatchSize) so no single query is
// unbounded, honoring the backend performance rule.
func TestAgentHistoryLoadsAllMessagesSinceCompact(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqlDB.Close()
	if err := createNarratorTestSchema(sqlDB); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	service := &Service{DB: sqlDB}
	narratorID := "nar_all_since_compact"
	if _, err := sqlDB.Exec(`INSERT INTO narrators (id, type, inherit_mode, model, status, created_at, updated_at) VALUES (?, 'primary', 'full', 'openai:gpt-test', 'idle', ?, ?)`, narratorID, nowForHistoryTest(0), nowForHistoryTest(0)); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}

	// Compact boundary at seq 0 with a summary, then 250 user messages after it.
	// 250 exceeds the old 80 default and the old 200 hard ceiling, so the old
	// capped implementation would have dropped the oldest 50-170 of them.
	insertHistoryMessage(t, sqlDB, narratorID, 0, "compact", "system",
		[]map[string]any{{"type": "compact", "status": "compacted", "summary": "Earlier summary"}},
		"[Compact] Earlier summary", true)
	const total = 250
	for i := 1; i <= total; i++ {
		insertHistoryMessage(t, sqlDB, narratorID, i, "m"+itoa(i), "user",
			[]map[string]any{{"type": "text", "text": "msg " + itoa(i)}}, "msg "+itoa(i), false)
	}

	history, err := service.agentHistory(context.Background(), narratorID)
	if err != nil {
		t.Fatalf("agentHistory returned error: %v", err)
	}
	// 250 user messages. The compact summary reaches the model ONLY through the
	// system prompt's "## Conversation Context" section, never as a history
	// message, so it is not present here.
	if len(history) != total {
		t.Fatalf("expected %d history entries (%d messages), got %d", total, total, len(history))
	}
	for _, msg := range history {
		if strings.Contains(msg.Content, "Prior compacted conversation summary") {
			t.Fatalf("compact summary must NOT be injected into history; got %#v", msg)
		}
	}
	// Order must be ascending by seq: first real message is "msg 1", last is "msg 250".
	if history[0].Content != "msg 1" {
		t.Fatalf("first real message should be \"msg 1\", got %q", history[0].Content)
	}
	if history[total-1].Content != "msg "+itoa(total) {
		t.Fatalf("last message should be %q, got %q", "msg "+itoa(total), history[total-1].Content)
	}
}

// TestAgentHistoryBatchBoundaries exercises the cursor paging at and around the
// agentHistoryBatchSize so no rows are dropped or duplicated across page reads.
func TestAgentHistoryBatchBoundaries(t *testing.T) {
	for _, count := range []int{agentHistoryBatchSize - 1, agentHistoryBatchSize, agentHistoryBatchSize + 1, agentHistoryBatchSize*2 + 3} {
		t.Run(itoa(count), func(t *testing.T) {
			sqlDB, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			defer sqlDB.Close()
			if err := createNarratorTestSchema(sqlDB); err != nil {
				t.Fatalf("create schema: %v", err)
			}
			service := &Service{DB: sqlDB}
			narratorID := "nar_batch"
			if _, err := sqlDB.Exec(`INSERT INTO narrators (id, type, inherit_mode, model, status, created_at, updated_at) VALUES (?, 'primary', 'full', 'openai:gpt-test', 'idle', ?, ?)`, narratorID, nowForHistoryTest(0), nowForHistoryTest(0)); err != nil {
				t.Fatalf("insert narrator: %v", err)
			}
			// No compact boundary: all messages should be returned.
			for i := 1; i <= count; i++ {
				insertHistoryMessage(t, sqlDB, narratorID, i, "m"+itoa(i), "user",
					[]map[string]any{{"type": "text", "text": "msg " + itoa(i)}}, "msg "+itoa(i), false)
			}
			history, err := service.agentHistory(context.Background(), narratorID)
			if err != nil {
				t.Fatalf("agentHistory returned error: %v", err)
			}
			if len(history) != count {
				t.Fatalf("count=%d: expected %d history entries, got %d", count, count, len(history))
			}
			// Verify exact ascending order with no gaps/dupes.
			for i := 0; i < count; i++ {
				want := "msg " + itoa(i+1)
				if history[i].Content != want {
					t.Fatalf("count=%d: history[%d] = %q, want %q", count, i, history[i].Content, want)
				}
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
