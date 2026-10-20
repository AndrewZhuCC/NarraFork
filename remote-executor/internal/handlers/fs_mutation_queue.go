package handlers

import (
	"container/list"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Shared by all handlers/connections in this process. Controlled reads and
// mutations of the same authorized canonical path wait for each other; external
// writers do not participate. There is no persistent state or automatic replay.
var fileMutations = newFileMutationQueue(64, 1024, 30*time.Second)

type fileMutationQueue struct {
	mu                sync.Mutex
	paths             map[string]*list.List // front is running, the rest wait FIFO
	waiting           int
	maxWaitingPerPath int
	maxWaiting        int
	waitTimeout       time.Duration
}

func newFileMutationQueue(maxWaitingPerPath, maxWaiting int, waitTimeout time.Duration) *fileMutationQueue {
	return &fileMutationQueue{
		paths:             make(map[string]*list.List),
		maxWaitingPerPath: maxWaitingPerPath,
		maxWaiting:        maxWaiting,
		waitTimeout:       waitTimeout,
	}
}

// path must come from the operation's PathGuard check, including resolution of
// symlinks/junctions and Windows extended names. Match pathWithin's case policy.
func fileMutationPathKey(path string) string {
	path = filepath.Clean(path)
	if filepath.Separator == '\\' {
		path = strings.ToLower(path)
	}
	return path
}

// The caller must defer release until actual IO and handle cleanup finish.
// Cancelling a running operation never releases its slot on the caller's behalf.
func acquireFileMutation(ctx context.Context, path string) (func(), error) {
	return fileMutations.acquire(ctx, path)
}

func (q *fileMutationQueue) acquire(ctx context.Context, path string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("file mutation requires an authorized absolute path")
	}
	key := fileMutationPathKey(path)
	// This deadline bounds admission only. Once acquired, actual IO retains its
	// slot even if either this budget or the RPC's deadline expires.
	waitCtx, cancel := context.WithTimeout(ctx, q.waitTimeout)
	defer cancel()

	q.mu.Lock()
	if err := waitCtx.Err(); err != nil {
		q.mu.Unlock()
		return nil, err
	}
	queue := q.paths[key]
	if queue == nil {
		queue = list.New()
		q.paths[key] = queue
	} else if queue.Len()-1 >= q.maxWaitingPerPath || q.waiting >= q.maxWaiting {
		q.mu.Unlock()
		return nil, fmt.Errorf("file mutation queue is full")
	}
	ready := make(chan struct{})
	entry := queue.PushBack(ready)
	if queue.Len() == 1 {
		close(ready)
	} else {
		q.waiting++
	}
	q.mu.Unlock()

	select {
	case <-ready:
	case <-waitCtx.Done():
	}
	q.mu.Lock()
	if err := waitCtx.Err(); err != nil {
		q.remove(key, queue, entry)
		q.mu.Unlock()
		return nil, err
	}
	q.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			q.mu.Lock()
			q.remove(key, queue, entry)
			q.mu.Unlock()
		})
	}, nil
}

// remove requires mu. Cancelled waiters are unlinked immediately, even while
// the front operation is still running. Empty path queues retain no state.
func (q *fileMutationQueue) remove(key string, queue *list.List, entry *list.Element) {
	wasRunning := queue.Front() == entry
	queue.Remove(entry)
	if !wasRunning {
		q.waiting--
	}
	if queue.Len() == 0 {
		delete(q.paths, key)
	} else if wasRunning {
		q.waiting--
		close(queue.Front().Value.(chan struct{}))
	}
}
