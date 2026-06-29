package event

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestUnsubscribeRemovesCorrectHandler verifies that unsubscribing one handler
// does not affect the others — the original swap-remove-by-index design removed
// the wrong handler.
func TestUnsubscribeRemovesCorrectHandler(t *testing.T) {
	b := NewBus()

	var aCount, bCount, cCount int
	_ = b.Subscribe("evt", func(Event) { aCount++ })
	unsubB := b.Subscribe("evt", func(Event) { bCount++ })
	_ = b.Subscribe("evt", func(Event) { cCount++ })

	// Remove the middle handler; A and C must still fire.
	unsubB()

	b.Publish(NarratorEvent{Name: "evt"})

	if aCount != 1 {
		t.Errorf("handler A: got %d calls, want 1", aCount)
	}
	if bCount != 0 {
		t.Errorf("handler B (unsubscribed): got %d calls, want 0", bCount)
	}
	if cCount != 1 {
		t.Errorf("handler C: got %d calls, want 1", cCount)
	}
}

// TestUnsubscribeIdempotent verifies that calling the unsubscribe function more
// than once is safe and does not corrupt other subscriptions.
func TestUnsubscribeIdempotent(t *testing.T) {
	b := NewBus()

	var aCount int
	unsubA := b.Subscribe("evt", func(Event) {})
	_ = b.Subscribe("evt", func(Event) { aCount++ })

	unsubA()
	unsubA() // second call must be a no-op
	unsubA()

	b.Publish(NarratorEvent{Name: "evt"})
	if aCount != 1 {
		t.Errorf("surviving handler: got %d calls, want 1", aCount)
	}
}

// TestPublishRecoversFromPanic verifies that a panicking handler does not abort
// the publish loop or crash the publisher.
func TestPublishRecoversFromPanic(t *testing.T) {
	b := NewBus()

	var after int
	b.Subscribe("evt", func(Event) { panic("boom") })
	b.Subscribe("evt", func(Event) { after++ })

	// Must not panic.
	b.Publish(NarratorEvent{Name: "evt"})

	if after != 1 {
		t.Errorf("handler after panicking one: got %d calls, want 1", after)
	}
}

// TestConcurrentPublishSubscribeUnsubscribe exercises the bus under concurrency
// so `go test -race` can flag any data race in the snapshot/lock design.
func TestConcurrentPublishSubscribeUnsubscribe(t *testing.T) {
	b := NewBus()
	var fired int64

	var wg sync.WaitGroup

	// Publishers.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				b.Publish(ChapterEvent{Name: "evt"})
			}
		}()
	}

	// Subscribers that churn subscriptions.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				unsub := b.Subscribe("evt", func(Event) {
					atomic.AddInt64(&fired, 1)
				})
				unsub()
			}
		}()
	}

	wg.Wait()
}
