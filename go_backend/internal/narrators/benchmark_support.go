package narrators

import (
	"context"
	"encoding/json"
	"strings"

	"narrafork-go/internal/db"
)

type BenchmarkUsage struct {
	TokensIn  int
	TokensOut int
	CostUSD   float64
	Model     string
}

// RecordBenchmarkExchange persists a deterministic benchmark prompt/response without
// starting a provider run. Tests and explicit fake benchmark runs use this path so the
// benchmark execution chain can be verified without spending model tokens.
func (s *Service) RecordBenchmarkExchange(ctx context.Context, narratorID, userID, prompt, response string, usage BenchmarkUsage) error {
	if strings.TrimSpace(userID) == "" {
		userID = "benchmark"
	}
	if _, err := s.addMessage(ctx, narratorID, "user", prompt, &userID); err != nil {
		return err
	}
	content := []map[string]any{{"type": "text", "text": response}}
	msg, err := s.addMessageWithBlocks(ctx, narratorID, "assistant", response, content, nil)
	if err != nil {
		return err
	}
	if err := s.updateBenchmarkUsage(ctx, msg.ID, usage); err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE narrators SET status = 'idle', substatus = '[]', total_cost_usd = COALESCE(total_cost_usd, 0) + ?, error_message = NULL, updated_at = ? WHERE id = ?`, usage.CostUSD, db.NowISO(), narratorID)
	return err
}

func (s *Service) updateBenchmarkUsage(ctx context.Context, messageID string, usage BenchmarkUsage) error {
	sets := []string{}
	args := []any{}
	if ok, err := s.columnExists(ctx, "narrator_messages", "tokens_in"); err != nil {
		return err
	} else if ok {
		sets = append(sets, "tokens_in = ?")
		args = append(args, usage.TokensIn)
	}
	if ok, err := s.columnExists(ctx, "narrator_messages", "output_tokens"); err != nil {
		return err
	} else if ok {
		sets = append(sets, "output_tokens = ?")
		args = append(args, usage.TokensOut)
	}
	if ok, err := s.columnExists(ctx, "narrator_messages", "cost_usd"); err != nil {
		return err
	} else if ok {
		sets = append(sets, "cost_usd = ?")
		args = append(args, usage.CostUSD)
	}
	if strings.TrimSpace(usage.Model) != "" {
		sets = append(sets, "model = ?")
		args = append(args, usage.Model)
	}
	if ok, err := s.columnExists(ctx, "narrator_messages", "turn_usage_json"); err != nil {
		return err
	} else if ok {
		payload, _ := json.Marshal(map[string]any{"inputTokens": usage.TokensIn, "outputTokens": usage.TokensOut, "costUsd": usage.CostUSD, "fake": true})
		sets = append(sets, "turn_usage_json = ?")
		args = append(args, string(payload))
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, messageID)
	_, err := s.DB.ExecContext(ctx, `UPDATE narrator_messages SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	return err
}
