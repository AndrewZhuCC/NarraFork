package ws

import (
	"encoding/json"
	"math"
	"sync"
	"time"
	"unicode/utf16"
)

// Output stats: a global AI-output character-rate tracker mirroring the
// TypeScript server/services/output-stats.ts. It records every streamed output
// chunk size, keeps a 3-second sliding window, and broadcasts
// {type:"output_stats", charsPerSec, totalChars} to subscribed narrator WS
// clients once per second while at least one client is subscribed.
//
// charsPerSec mirrors TS computeRate(): Math.round((sum / (WINDOW_MS/1000)) * 10) / 10
// (one decimal place over the 3-second window). totalChars accumulates since
// process start. Chunk char counts are measured in UTF-16 code units to match the
// TS recordOutputChunk(event.text.length) call sites.

const (
	outputStatsWindow            = 3 * time.Second
	outputStatsBroadcastInterval = time.Second
	outputStatsMaxChunks         = 10000
)

type outputStatsChunk struct {
	chars int
	ts    time.Time
}

type outputStatsTracker struct {
	mu              sync.Mutex
	totalChars      int64
	chunks          []outputStatsChunk
	subscriberCount int
	ticker          *time.Ticker
	stop            chan struct{}
}

// JSStringLength returns the number of UTF-16 code units in s, matching the JS
// String.prototype.length used by the TS recordOutputChunk call sites.
func outputStatsJSLength(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// RecordOutputChunk records a streamed output chunk of the given UTF-16 code-unit
// length. Mirrors the TS recordOutputChunk(charCount): non-positive counts are
// ignored, the running total accumulates, and the sliding window is only pruned
// when it grows clearly oversized (cheap on the streaming hot path).
func (h *Hub) RecordOutputChunk(charCount int) {
	if charCount <= 0 {
		return
	}
	t := &h.outputStats
	t.mu.Lock()
	t.totalChars += int64(charCount)
	t.chunks = append(t.chunks, outputStatsChunk{chars: charCount, ts: time.Now()})
	if len(t.chunks) > outputStatsMaxChunks {
		cutoff := time.Now().Add(-outputStatsWindow)
		idx := 0
		for idx < len(t.chunks) && t.chunks[idx].ts.Before(cutoff) {
			idx++
		}
		if idx > 0 {
			t.chunks = append(t.chunks[:0], t.chunks[idx:]...)
		}
	}
	t.mu.Unlock()
}

// RecordOutputText records a streamed output text chunk, measuring its length in
// UTF-16 code units to match TS recordOutputChunk(event.text.length).
func (h *Hub) RecordOutputText(text string) {
	if text == "" {
		return
	}
	h.RecordOutputChunk(outputStatsJSLength(text))
}

// computeOutputRate mirrors the TS computeRate(): prune entries older than the
// 3-second window, then return Math.round((sum / 3) * 10) / 10 chars/sec.
func (t *outputStatsTracker) computeRate() float64 {
	cutoff := time.Now().Add(-outputStatsWindow)
	idx := 0
	for idx < len(t.chunks) && t.chunks[idx].ts.Before(cutoff) {
		idx++
	}
	if idx > 0 {
		t.chunks = append(t.chunks[:0], t.chunks[idx:]...)
	}
	if len(t.chunks) == 0 {
		return 0
	}
	sum := 0
	for _, c := range t.chunks {
		sum += c.chars
	}
	return math.Round((float64(sum)/(float64(outputStatsWindow)/float64(time.Second)))*10) / 10
}

// outputStatsSnapshot returns the current charsPerSec and totalChars under lock.
func (h *Hub) outputStatsSnapshot() (float64, int64) {
	t := &h.outputStats
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.computeRate(), t.totalChars
}

// OutputStatsSnapshot is an exported accessor for the current output-stats
// charsPerSec/totalChars, used by tests and any external observability.
func (h *Hub) OutputStatsSnapshot() (float64, int64) {
	return h.outputStatsSnapshot()
}

// addOutputStatsSubscriber increments the subscriber count and starts the 1s
// broadcast ticker if it is not already running.
func (h *Hub) addOutputStatsSubscriber() {
	t := &h.outputStats
	t.mu.Lock()
	t.subscriberCount++
	start := t.ticker == nil
	if start {
		t.ticker = time.NewTicker(outputStatsBroadcastInterval)
		t.stop = make(chan struct{})
	}
	ticker := t.ticker
	stop := t.stop
	t.mu.Unlock()
	if start {
		go h.outputStatsBroadcastLoop(ticker, stop)
	}
}

// removeOutputStatsSubscriber decrements the subscriber count and stops the
// broadcast ticker when no subscribers remain.
func (h *Hub) removeOutputStatsSubscriber() {
	t := &h.outputStats
	t.mu.Lock()
	if t.subscriberCount > 0 {
		t.subscriberCount--
	}
	if t.subscriberCount == 0 && t.ticker != nil {
		t.ticker.Stop()
		close(t.stop)
		t.ticker = nil
		t.stop = nil
	}
	t.mu.Unlock()
}

func (h *Hub) outputStatsBroadcastLoop(ticker *time.Ticker, stop chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			h.broadcastOutputStats()
		}
	}
}

func (h *Hub) broadcastOutputStats() {
	rate, total := h.outputStatsSnapshot()
	payload, err := json.Marshal(map[string]any{
		"type":        "output_stats",
		"charsPerSec": rate,
		"totalChars":  total,
	})
	if err != nil {
		return
	}
	h.mu.RLock()
	clients := make([]*client, 0, len(h.narrators))
	for c := range h.narrators {
		if c.subscribedStats {
			clients = append(clients, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range clients {
		c.queueSend(payload)
	}
}
