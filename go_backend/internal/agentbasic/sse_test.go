package agentbasic

import (
	"context"
	"strings"
	"testing"
)

func TestReadSSEParsesEventsAndMultilineData(t *testing.T) {
	input := "event: message\ndata: {\"a\":1}\ndata: {\"b\":2}\n\n: keepalive\ndata: [DONE]\n\n"
	events := []sseEvent{}
	err := readSSE(context.Background(), strings.NewReader(input), 0, func(event sseEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("readSSE returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %#v", events)
	}
	if events[0].Event != "message" || events[0].Data != "{\"a\":1}\n{\"b\":2}" {
		t.Fatalf("unexpected first event: %#v", events[0])
	}
	if events[1].Data != "[DONE]" {
		t.Fatalf("unexpected done event: %#v", events[1])
	}
}
