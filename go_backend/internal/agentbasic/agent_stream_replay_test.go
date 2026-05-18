package agentbasic

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type streamReplayFixture struct {
	Name               string                `json:"name"`
	InputMessages      []Message             `json:"inputMessages"`
	ProviderTurns      [][]ParsedStreamEvent `json:"providerTurns"`
	ToolOutputs        map[string]string     `json:"toolOutputs"`
	ExpectedEventTypes []string              `json:"expectedEventTypes"`
}

func TestRunAgentLoopStreamReplayFixture(t *testing.T) {
	fixture := loadStreamReplayFixture(t, "testdata/stream_replay_basic.json")
	turns := make([][]StreamEvent, 0, len(fixture.ProviderTurns))
	for _, turn := range fixture.ProviderTurns {
		streamTurn := make([]StreamEvent, 0, len(turn))
		for _, parsed := range turn {
			streamTurn = append(streamTurn, StreamEvent{Parsed: parsed})
		}
		turns = append(turns, streamTurn)
	}
	adapter := &scriptedAdapter{turns: turns}
	stream := runAgentLoop(
		context.Background(),
		ProviderResolution{Provider: "fixture", Model: "fixture-model", Adapter: adapter},
		fixture.InputMessages,
		t.TempDir(),
		normalizeGenerateOptions(
			WithMaxTurns(4),
			WithRetryBaseDelay(0),
			WithToolExecutor(func(ctx context.Context, cwd string, call ToolCall) (ToolRun, error) {
				output, ok := fixture.ToolOutputs[call.ID]
				if !ok {
					t.Fatalf("unexpected replay tool call: %#v", call)
				}
				return ToolRun{Output: output}, nil
			}),
		),
	)
	events := collectEvents(stream)
	if got := eventTypes(events); !reflect.DeepEqual(got, fixture.ExpectedEventTypes) {
		t.Fatalf("%s event types = %#v, want %#v", fixture.Name, got, fixture.ExpectedEventTypes)
	}
}

func loadStreamReplayFixture(t *testing.T, path string) streamReplayFixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stream replay fixture: %v", err)
	}
	var fixture streamReplayFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode stream replay fixture: %v", err)
	}
	return fixture
}
