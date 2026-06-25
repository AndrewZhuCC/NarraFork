package narrators

import "testing"

// TestInsertAssistantBlockOrdering locks the TS appendBlockToMessage ordering
// contract (narrator-persistence.ts:777-804) that Go must mirror so the persisted
// assistant message keeps blocks in the correct order regardless of the order the
// block_complete / tool_call events arrive in.
func TestInsertAssistantBlockOrdering(t *testing.T) {
	types := func(blocks []map[string]any) []string {
		out := make([]string, len(blocks))
		for i, b := range blocks {
			out[i] = b["type"].(string)
		}
		return out
	}

	t.Run("reasoning flushed after tool_use is reordered to the front", func(t *testing.T) {
		// Reproduces the reported bug: the tool_use block is emitted DURING the
		// stream, then reasoning is flushed at turn end. Without ordered insertion
		// the result is [tool_use, reasoning]; it must be [reasoning, tool_use].
		state := &agentRunState{}
		upsertToolUseBlock(state, map[string]any{"type": "tool_use", "id": "t1", "name": "Bash"})
		insertAssistantBlock(state, map[string]any{"type": "reasoning", "text": "thinking"})
		if got := types(state.blocks); len(got) != 2 || got[0] != "reasoning" || got[1] != "tool_use" {
			t.Fatalf("want [reasoning tool_use], got %v", got)
		}
	})

	t.Run("text flushed after tool_use is reordered before the tool", func(t *testing.T) {
		state := &agentRunState{}
		upsertToolUseBlock(state, map[string]any{"type": "tool_use", "id": "t1", "name": "Bash"})
		insertAssistantBlock(state, map[string]any{"type": "text", "text": "answer"})
		if got := types(state.blocks); len(got) != 2 || got[0] != "text" || got[1] != "tool_use" {
			t.Fatalf("want [text tool_use], got %v", got)
		}
	})

	t.Run("reasoning then text then tool_use end up reasoning,text,tool_use", func(t *testing.T) {
		state := &agentRunState{}
		// Arrival order: tool_use (during stream), then reasoning + text (flush).
		upsertToolUseBlock(state, map[string]any{"type": "tool_use", "id": "t1", "name": "Bash"})
		insertAssistantBlock(state, map[string]any{"type": "reasoning", "text": "r"})
		insertAssistantBlock(state, map[string]any{"type": "text", "text": "t"})
		got := types(state.blocks)
		want := []string{"reasoning", "text", "tool_use"}
		if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Fatalf("want %v, got %v", want, got)
		}
	})

	t.Run("outputIndex sorts blocks ascending and stable", func(t *testing.T) {
		state := &agentRunState{}
		// Provider supplies explicit outputIndex — order must follow it exactly.
		insertAssistantBlock(state, map[string]any{"type": "text", "text": "second", "outputIndex": 1})
		insertAssistantBlock(state, map[string]any{"type": "reasoning", "text": "first", "outputIndex": 0})
		insertAssistantBlock(state, map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "outputIndex": 2})
		got := types(state.blocks)
		want := []string{"reasoning", "text", "tool_use"}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("outputIndex order want %v, got %v", want, got)
			}
		}
	})

	t.Run("multiple reasoning blocks stay grouped before tool_use", func(t *testing.T) {
		state := &agentRunState{}
		upsertToolUseBlock(state, map[string]any{"type": "tool_use", "id": "t1", "name": "Bash"})
		insertAssistantBlock(state, map[string]any{"type": "reasoning", "text": "r1"})
		insertAssistantBlock(state, map[string]any{"type": "reasoning", "text": "r2"})
		got := types(state.blocks)
		if len(got) != 3 || got[0] != "reasoning" || got[1] != "reasoning" || got[2] != "tool_use" {
			t.Fatalf("want [reasoning reasoning tool_use], got %v", got)
		}
		// r1 must precede r2 (insertion order preserved among reasoning blocks).
		if state.blocks[0]["text"] != "r1" || state.blocks[1]["text"] != "r2" {
			t.Fatalf("reasoning insertion order not preserved: %v / %v", state.blocks[0]["text"], state.blocks[1]["text"])
		}
	})
}
