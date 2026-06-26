package narrators

import (
	"encoding/json"
	"strings"
	"testing"

	"narrafork-go/internal/agentbasic"
)

func brokenToolCall(name, args string) agentbasic.ToolCall {
	call := agentbasic.ToolCall{ID: "tc-" + name, Type: "function"}
	call.Function.Name = name
	call.Function.Arguments = args
	return call
}

// TestGuardedMalformedToolInputDetection pins which inputs the production broken
// detection treats as broken, mirroring TS tool-executor.ts:212-249: malformed /
// truncated JSON and a file-writing tool with empty input are broken; well-formed
// input and empty input for non-file tools are not.
func TestGuardedMalformedToolInputDetection(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
		want bool
	}{
		{"truncated json edit", "Edit", `{"file_path":"a.txt","new_string":"abc`, true},
		{"truncated json write", "Write", `{"file_path":"a.txt","content":"`, true},
		{"raw field surfaced", "Write", `{"_raw":"garbage"}`, true},
		{"valid edit", "Edit", `{"file_path":"a.txt","old_string":"x","new_string":"y"}`, false},
		{"valid read", "Read", `{"file_path":"a.txt"}`, false},
		{"empty write is complete truncation", "Write", `{}`, true},
		{"empty edit is complete truncation", "Edit", ``, true},
		{"empty read is fine", "Read", `{}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, blocked := guardedMalformedToolInputRun(brokenToolCall(tc.tool, tc.args), "en")
			if blocked != tc.want {
				t.Fatalf("guardedMalformedToolInputRun(%q, %q) blocked = %v, want %v", tc.tool, tc.args, blocked, tc.want)
			}
		})
	}
}

// TestGuardedMalformedToolInputBilingualResult verifies the model-facing output of
// a broken tool call is the bilingual brokenToolCallResult message (parity with TS
// loop.ts:3714 getToolMessage("brokenToolCallResult", locale)) rather than the old
// single-language English "refusing to execute" string.
func TestGuardedMalformedToolInputBilingualResult(t *testing.T) {
	call := brokenToolCall("Write", `{"file_path":"big.txt","content":"aaaa`)

	en, blocked := guardedMalformedToolInputRun(call, "en")
	if !blocked {
		t.Fatal("expected broken call to be blocked")
	}
	if en.Output != agentbasic.BrokenToolCallResultMessage("en") {
		t.Fatalf("en output = %q, want bilingual brokenToolCallResult", en.Output)
	}
	if !strings.Contains(en.Output, "truncated by token limit") {
		t.Fatalf("en output missing expected text: %q", en.Output)
	}
	if en.Error != en.Output {
		t.Fatalf("en error should mirror output, got %q vs %q", en.Error, en.Output)
	}
	if !en.Broken || en.Status != "error" {
		t.Fatalf("expected Broken=true status=error, got Broken=%v status=%q", en.Broken, en.Status)
	}

	zh, _ := guardedMalformedToolInputRun(call, "zh-CN")
	if zh.Output != agentbasic.BrokenToolCallResultMessage("zh-CN") {
		t.Fatalf("zh output = %q, want bilingual brokenToolCallResult", zh.Output)
	}
	if !strings.Contains(zh.Output, "token 限制截断") {
		t.Fatalf("zh output missing expected text: %q", zh.Output)
	}
}

// TestGuardedMalformedToolInputSanitizesArguments verifies a broken call's
// run.Arguments is replaced with the sanitized input (parity with TS
// brokenInputOverride): structural fields like file_path are kept while large
// content fields collapse to the placeholder. This is what every downstream
// input_json persistence point writes, so the DB never shows truncated garbage.
func TestGuardedMalformedToolInputSanitizesArguments(t *testing.T) {
	placeholder := agentbasic.BrokenToolInputPlaceholder("en")

	// Truncated JSON (unparseable) → surfaced as _raw → file_path extracted.
	rawTruncated := brokenToolCall("Write", `{"file_path":"big.txt","content":"aaaa`)
	run, blocked := guardedMalformedToolInputRun(rawTruncated, "en")
	if !blocked {
		t.Fatal("expected truncated write to be blocked")
	}
	var sanitized map[string]any
	if err := json.Unmarshal([]byte(run.Arguments), &sanitized); err != nil {
		t.Fatalf("run.Arguments must be valid sanitized JSON, got %q: %v", run.Arguments, err)
	}
	if sanitized["file_path"] != "big.txt" {
		t.Fatalf("expected file_path extracted from raw, got %#v", sanitized)
	}
	if sanitized["content"] != placeholder {
		t.Fatalf("expected content placeholder, got %#v", sanitized)
	}

	// Empty write (complete truncation) → sanitized with empty file_path stub.
	emptyWrite := brokenToolCall("Write", `{}`)
	emptyRun, _ := guardedMalformedToolInputRun(emptyWrite, "en")
	if err := json.Unmarshal([]byte(emptyRun.Arguments), &sanitized); err != nil {
		t.Fatalf("empty-write Arguments must be valid JSON, got %q: %v", emptyRun.Arguments, err)
	}
	if _, ok := sanitized["file_path"]; !ok {
		t.Fatalf("expected file_path stub on empty write, got %#v", sanitized)
	}
	if sanitized["content"] != placeholder {
		t.Fatalf("expected content placeholder on empty write, got %#v", sanitized)
	}
}

// TestGuardedMalformedToolInputLeavesValidCallUntouched verifies a well-formed
// tool call is not flagged and its arguments are preserved verbatim (no sanitize).
func TestGuardedMalformedToolInputLeavesValidCallUntouched(t *testing.T) {
	args := `{"file_path":"a.txt","old_string":"x","new_string":"y"}`
	run, blocked := guardedMalformedToolInputRun(brokenToolCall("Edit", args), "en")
	if blocked {
		t.Fatalf("valid edit must not be blocked, got run=%#v", run)
	}
	if run.Arguments != args {
		t.Fatalf("valid edit arguments must be preserved, got %q want %q", run.Arguments, args)
	}
	if run.Broken {
		t.Fatal("valid edit must not be marked Broken")
	}
}
