package ws

import "testing"

func payloadType(payload any) string {
	m, _ := payload.(map[string]any)
	typeValue, _ := m["type"].(string)
	return typeValue
}

func payloadByType(t *testing.T, payloads []any, typeValue string) map[string]any {
	t.Helper()
	for _, payload := range payloads {
		m, ok := payload.(map[string]any)
		if ok && m["type"] == typeValue {
			return m
		}
	}
	t.Fatalf("payload type %q not found in %#v", typeValue, payloads)
	return nil
}

func TestNarratorBroadcastPayloadsAddsStatusChangeAndSubstatusChange(t *testing.T) {
	payloads := narratorBroadcastPayloads("nar-1", map[string]any{
		"type":       "status",
		"narratorId": "nar-1",
		"status":     "working",
		"substatus":  []string{"calling_model"},
	})
	if len(payloads) != 3 || payloadType(payloads[0]) != "status" {
		t.Fatalf("expected legacy status plus two compatible payloads, got %#v", payloads)
	}
	statusChange := payloadByType(t, payloads, "status_change")
	if statusChange["status"] != "working" || statusChange["narratorId"] != "nar-1" {
		t.Fatalf("unexpected status_change payload: %#v", statusChange)
	}
	substatusChange := payloadByType(t, payloads, "substatus_change")
	substatus, ok := substatusChange["substatus"].([]string)
	if !ok || len(substatus) != 1 || substatus[0] != "calling_model" {
		t.Fatalf("unexpected substatus_change payload: %#v", substatusChange)
	}
}

func TestNarratorBroadcastPayloadsAddsStreamAndToolCompatibility(t *testing.T) {
	streamPayloads := narratorBroadcastPayloads("nar-1", map[string]any{
		"type":        "stream_text",
		"narratorId":  "nar-1",
		"text":        "hello",
		"outputIndex": 2,
	})
	stream := payloadByType(t, streamPayloads, "stream_event")
	event, _ := stream["event"].(map[string]any)
	delta, _ := event["delta"].(map[string]any)
	if event["type"] != "content_block_delta" || delta["type"] != "text_delta" || delta["text"] != "hello" || event["outputIndex"] != 2 {
		t.Fatalf("unexpected stream_event payload: %#v", stream)
	}

	toolPayloads := narratorBroadcastPayloads("nar-1", map[string]any{
		"type":       "tool_result",
		"narratorId": "nar-1",
		"toolUseId":  "tool-1",
		"toolName":   "Read",
		"output":     "file contents",
		"durationMs": int64(42),
	})
	completed := payloadByType(t, toolPayloads, "tool_completed")
	if completed["toolUseId"] != "tool-1" || completed["toolName"] != "Read" || completed["status"] != "success" || completed["output"] != "file contents" || completed["durationMs"] != int64(42) {
		t.Fatalf("unexpected tool_completed payload: %#v", completed)
	}
}

func TestNarratorBroadcastPayloadsAddsUserMessageCompatibility(t *testing.T) {
	message := map[string]any{"id": "msg-1", "role": "user", "contentText": "hello"}
	payloads := narratorBroadcastPayloads("nar-1", map[string]any{
		"type":       "message",
		"narratorId": "nar-1",
		"message":    message,
	})
	userMessage := payloadByType(t, payloads, "user_message")
	userPayload, _ := userMessage["message"].(map[string]any)
	if userPayload["id"] != "msg-1" || userMessage["narratorId"] != "nar-1" {
		t.Fatalf("unexpected user_message payload: %#v", userMessage)
	}
}
