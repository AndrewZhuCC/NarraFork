package handlers

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// RPC cancellation can be acknowledged before the Git process has exited. The
// executor therefore owns execution slots independently of the server's flights.
// A caller must hold its release function until every cmd.Wait has completed.
// Admission uses opaque per-request identities, BEFORE any Git command (including
// root discovery). The second, separate pool is only the canonical root lock;
// at most four admitted requests can reach it, so its queue cannot grow unbounded.
var gitStatusAdmissions = newGitStatusScheduler(4, 128)
var gitStatusQueries = newGitStatusScheduler(4, 4)

type gitStatusWaiter struct {
	root    string
	ctx     context.Context
	ready   chan struct{}
	granted bool
}

type gitStatusScheduler struct {
	nextAdmission atomic.Uint64
	mu            sync.Mutex
	maxRunning    int
	maxQueued     int
	active        map[string]bool
	queue         []*gitStatusWaiter
}

func newGitStatusScheduler(maxRunning, maxQueued int) *gitStatusScheduler {
	return &gitStatusScheduler{maxRunning: maxRunning, maxQueued: maxQueued, active: make(map[string]bool)}
}

// admit deliberately takes no cwd: an unverified path is not a root identity.
// It is used only on the admission pool, never recursively on the root-lock pool.
func (s *gitStatusScheduler) admit(ctx context.Context) (func(), error) {
	return s.acquire(ctx, fmt.Sprintf("admission:%d", s.nextAdmission.Add(1)))
}

func (s *gitStatusScheduler) acquire(ctx context.Context, root string) (func(), error) {
	if filepath.Separator == '\\' {
		root = strings.ToLower(root)
	}
	s.mu.Lock()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	waiter := &gitStatusWaiter{root: root, ctx: ctx, ready: make(chan struct{})}
	if len(s.active) < s.maxRunning && !s.active[root] {
		s.grant(waiter)
	} else {
		if len(s.queue) >= s.maxQueued {
			s.mu.Unlock()
			return nil, fmt.Errorf("Git status queue is full; retry shortly")
		}
		s.queue = append(s.queue, waiter)
	}
	s.mu.Unlock()

	select {
	case <-waiter.ready:
	case <-ctx.Done():
	}
	s.mu.Lock()
	if err := ctx.Err(); err != nil {
		if waiter.granted {
			delete(s.active, root)
		} else {
			for index, queued := range s.queue {
				if queued == waiter {
					s.queue = append(s.queue[:index], s.queue[index+1:]...)
					break
				}
			}
		}
		s.pump()
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.active, root)
			s.pump()
			s.mu.Unlock()
		})
	}, nil
}

// grant and pump require mu. Roots blocked by running work do not starve other
// roots, while the oldest eligible request for a root always runs first.
func (s *gitStatusScheduler) grant(waiter *gitStatusWaiter) {
	s.active[waiter.root] = true
	waiter.granted = true
	close(waiter.ready)
}

func (s *gitStatusScheduler) pump() {
	for index := 0; index < len(s.queue); {
		waiter := s.queue[index]
		if waiter.ctx.Err() != nil {
			s.queue = append(s.queue[:index], s.queue[index+1:]...)
			close(waiter.ready)
			continue
		}
		if len(s.active) >= s.maxRunning || s.active[waiter.root] {
			index++
			continue
		}
		s.queue = append(s.queue[:index], s.queue[index+1:]...)
		s.grant(waiter)
	}
}
