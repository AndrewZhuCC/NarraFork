// Package event provides a typed, in-process event bus for decoupling
// services from WebSocket broadcast and cross-service communication.
package event

import (
	"log/slog"
	"sync"
)

// Event represents any event that can be published on the bus.
type Event interface {
	EventName() string
}

// Handler processes an event. Handlers are called synchronously in the
// goroutine that publishes — keep them fast or spawn a goroutine inside.
type Handler func(Event)

// Bus is a simple typed publish/subscribe event bus.
//
// Subscriptions are identified by a monotonic uint64 ID rather than by slice
// position, so unsubscribing one handler never disturbs the others. Publish
// snapshots the relevant handlers under the lock and invokes them after the
// lock is released (copy-on-read), which keeps the critical section short and
// avoids data races with concurrent Subscribe/Unsubscribe calls.
type Bus struct {
	mu       sync.RWMutex
	nextID   uint64
	handlers map[string]map[uint64]Handler // eventName → subID → handler
}

// NewBus creates an empty event bus.
func NewBus() *Bus {
	return &Bus{
		handlers: make(map[string]map[uint64]Handler),
	}
}

// Subscribe registers a handler for events with the given name.
// Returns an unsubscribe function that is safe to call multiple times and
// from any goroutine; only the matching subscription is removed.
func (b *Bus) Subscribe(eventName string, handler Handler) func() {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	subs := b.handlers[eventName]
	if subs == nil {
		subs = make(map[uint64]Handler)
		b.handlers[eventName] = subs
	}
	subs[id] = handler
	b.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if subs := b.handlers[eventName]; subs != nil {
				delete(subs, id)
				if len(subs) == 0 {
					delete(b.handlers, eventName)
				}
			}
		})
	}
}

// Publish sends an event to all registered handlers for that event name.
// Handlers are snapshotted under the lock and invoked after it is released,
// so a slow or re-entrant handler cannot block other publishers or mutate the
// slice being iterated. A panic in one handler is recovered and logged so it
// neither aborts the publish loop nor crashes the publishing goroutine.
func (b *Bus) Publish(event Event) {
	name := event.EventName()

	b.mu.RLock()
	subs := b.handlers[name]
	snapshot := make([]Handler, 0, len(subs))
	for _, h := range subs {
		snapshot = append(snapshot, h)
	}
	b.mu.RUnlock()

	for _, h := range snapshot {
		callHandler(h, event)
	}
}

// callHandler invokes a handler, recovering from any panic so one misbehaving
// subscriber cannot take down the publisher.
func callHandler(h Handler, event Event) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("event handler panicked",
				"event", event.EventName(),
				"panic", r,
			)
		}
	}()
	h(event)
}

// Common event types for NarraFork

// NarratorEvent is emitted when narrator state changes.
type NarratorEvent struct {
	Name       string
	NarratorID string
	Data       any
}

func (e NarratorEvent) EventName() string { return e.Name }

// ChapterEvent is emitted when chapter state changes.
type ChapterEvent struct {
	Name      string
	ChapterID string
	Data      any
}

func (e ChapterEvent) EventName() string { return e.Name }

// TerminalEvent is emitted for terminal I/O.
type TerminalEvent struct {
	Name       string
	TerminalID string
	Data       any
}

func (e TerminalEvent) EventName() string { return e.Name }
