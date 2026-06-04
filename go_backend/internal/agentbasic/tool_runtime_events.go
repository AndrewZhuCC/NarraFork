package agentbasic

import (
	"context"
	"strings"
	"sync"
	"time"
)

const maxStreamOutputLength = 30_000

var toolOutputThrottle = 100 * time.Millisecond

type toolRuntimeEventsContextKey struct{}

type ToolRuntimeEventSink struct {
	ToolUseID string
	Emit      func(AgentEvent)

	mu                sync.Mutex
	latestOutput      string
	lastFlushedOutput string
	lastOutputEmit    time.Time
	pendingTimer      *time.Timer
	closed            bool
}

func NewToolRuntimeEventSink(toolUseID string, emit func(AgentEvent)) *ToolRuntimeEventSink {
	return &ToolRuntimeEventSink{ToolUseID: strings.TrimSpace(toolUseID), Emit: emit}
}

func ContextWithToolRuntimeEvents(ctx context.Context, sink *ToolRuntimeEventSink) context.Context {
	if sink == nil || sink.Emit == nil || sink.ToolUseID == "" {
		return ctx
	}
	return context.WithValue(ctx, toolRuntimeEventsContextKey{}, sink)
}

func toolRuntimeEventSinkFromContext(ctx context.Context) *ToolRuntimeEventSink {
	sink, _ := ctx.Value(toolRuntimeEventsContextKey{}).(*ToolRuntimeEventSink)
	return sink
}

func EmitToolRuntimeOutput(ctx context.Context, output string) {
	if sink := toolRuntimeEventSinkFromContext(ctx); sink != nil {
		sink.EmitOutput(output)
	}
}

func EmitToolRuntimeLongRunning(ctx context.Context, elapsed time.Duration) {
	if sink := toolRuntimeEventSinkFromContext(ctx); sink != nil && sink.Emit != nil && sink.ToolUseID != "" {
		sink.Emit(AgentEvent{Type: "tool_long_running", ToolUseID: sink.ToolUseID, Elapsed: elapsed.Milliseconds()})
	}
}

func FlushToolRuntimeEvents(ctx context.Context) {
	if sink := toolRuntimeEventSinkFromContext(ctx); sink != nil {
		sink.FlushAndClose()
	}
}

func (s *ToolRuntimeEventSink) EmitOutput(output string) {
	if s == nil || s.Emit == nil || s.ToolUseID == "" {
		return
	}
	if len(output) > maxStreamOutputLength {
		output = "...\n\n" + output[len(output)-maxStreamOutputLength:]
	}
	now := time.Now()
	var event *AgentEvent
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.latestOutput = output
	elapsed := now.Sub(s.lastOutputEmit)
	if s.lastOutputEmit.IsZero() || toolOutputThrottle <= 0 || elapsed >= toolOutputThrottle {
		if s.pendingTimer != nil {
			s.pendingTimer.Stop()
			s.pendingTimer = nil
		}
		event = s.flushLocked(now)
	} else if s.pendingTimer == nil {
		delay := toolOutputThrottle - elapsed
		s.pendingTimer = time.AfterFunc(delay, func() { s.flushDue() })
	}
	s.mu.Unlock()
	if event != nil {
		s.Emit(*event)
	}
}

func (s *ToolRuntimeEventSink) flushDue() {
	var event *AgentEvent
	s.mu.Lock()
	if !s.closed {
		s.pendingTimer = nil
		event = s.flushLocked(time.Now())
	}
	s.mu.Unlock()
	if event != nil {
		s.Emit(*event)
	}
}

func (s *ToolRuntimeEventSink) FlushAndClose() {
	if s == nil || s.Emit == nil || s.ToolUseID == "" {
		return
	}
	var event *AgentEvent
	s.mu.Lock()
	if s.pendingTimer != nil {
		s.pendingTimer.Stop()
		s.pendingTimer = nil
	}
	if !s.closed {
		event = s.flushLocked(time.Now())
		s.closed = true
	}
	s.mu.Unlock()
	if event != nil {
		s.Emit(*event)
	}
}

func (s *ToolRuntimeEventSink) flushLocked(now time.Time) *AgentEvent {
	if s.latestOutput == "" || s.latestOutput == s.lastFlushedOutput {
		return nil
	}
	s.lastOutputEmit = now
	s.lastFlushedOutput = s.latestOutput
	return &AgentEvent{Type: "tool_output", ToolUseID: s.ToolUseID, Output: s.latestOutput}
}
