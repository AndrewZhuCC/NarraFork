package ws

import (
	"encoding/json"
	"testing"

	"narrafork-go/internal/compat/bunfinal"
)

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

type narratorWSEventInventory struct {
	Events []narratorWSEventInventoryEvent `json:"events"`
}

type narratorWSEventInventoryEvent struct {
	Type           string   `json:"type"`
	RequiredFields []string `json:"requiredFields"`
}

func TestNarratorBroadcastPayloadsCoverBunFinalEventInventory(t *testing.T) {
	data, err := bunfinal.ReadNarratorWSEventInventory()
	if err != nil {
		t.Fatalf("read narrator WS event inventory: %v", err)
	}
	var inventory narratorWSEventInventory
	if err := json.Unmarshal(data, &inventory); err != nil {
		t.Fatalf("decode narrator WS event inventory: %v", err)
	}
	if len(inventory.Events) == 0 {
		t.Fatal("narrator WS event inventory should contain events")
	}
	for _, event := range inventory.Events {
		t.Run(event.Type, func(t *testing.T) {
			payloads := narratorBroadcastPayloads("nar-matrix", sampleNarratorWSEventPayload(event.Type))
			payload := payloadByType(t, payloads, event.Type)
			for _, field := range event.RequiredFields {
				if _, ok := payload[field]; !ok {
					t.Fatalf("event %s missing required field %s in %#v", event.Type, field, payload)
				}
			}
		})
	}
}

func sampleNarratorWSEventPayload(eventType string) map[string]any {
	switch eventType {
	case "user_message":
		return map[string]any{"type": "message", "narratorId": "nar-matrix", "message": map[string]any{"id": "msg-user", "role": "user"}}
	case "stream_event":
		return map[string]any{"type": "stream_text", "narratorId": "nar-matrix", "text": "delta", "outputIndex": 0}
	case "tool_started":
		return map[string]any{"type": "tool_call", "narratorId": "nar-matrix", "toolUseId": "tool-1", "toolName": "Read"}
	case "tool_completed":
		return map[string]any{"type": "tool_result", "narratorId": "nar-matrix", "toolUseId": "tool-1", "toolName": "Read", "output": "ok"}
	case "status_change", "substatus_change":
		return map[string]any{"type": "status", "narratorId": "nar-matrix", "status": "working", "substatus": []string{"calling_model"}}
	case "permission_request":
		return map[string]any{"type": "permission_request", "request": map[string]any{"id": "perm-1"}}
	case "permission_resolved":
		return map[string]any{"type": "permission_resolved", "toolUseId": "tool-1", "decision": "approved"}
	case "tool_use_chunk":
		return map[string]any{"type": "tool_use_chunk", "toolUseId": "tool-1", "chunk": "x"}
	case "catch_up":
		return map[string]any{"type": "catch_up", "narratorId": "nar-matrix", "messages": []any{}, "messageVersion": 1}
	case "sync_ok":
		return map[string]any{"type": "sync_ok", "narratorId": "nar-matrix", "messageVersion": 1}
	case "full_reload":
		return map[string]any{"type": "full_reload", "narratorId": "nar-matrix", "reason": "test"}
	case "streaming_snapshot":
		return map[string]any{"type": "streaming_snapshot", "narratorId": "nar-matrix", "snapshot": map[string]any{"status": "working"}}
	case "presence_update":
		return map[string]any{"type": "presence_update", "narratorId": "nar-matrix", "viewers": []any{}}
	default:
		return map[string]any{"type": eventType, "narratorId": "nar-matrix", "message": map[string]any{"id": "msg-1"}}
	}
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
