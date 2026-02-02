package sync

import (
	"sync"
	"time"
)

// Tracker manages background tasks ensuring they complete before shutdown
type Tracker struct {
	wg sync.WaitGroup
}

// NewTracker creates a new Tracker
func NewTracker() *Tracker {
	return &Tracker{}
}

// Go executes a function in a goroutine and tracks it
func (t *Tracker) Go(fn func()) {
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		fn()
	}()
}

// Wait blocks until all tracked tasks are done
func (t *Tracker) Wait() {
	t.wg.Wait()
}

// WaitWithTimeout blocks until all tasks are done or timeout occurs
// Returns true if completed, false if timed out
func (t *Tracker) WaitWithTimeout(timeout time.Duration) bool {
	c := make(chan struct{})
	go func() {
		defer close(c)
		t.wg.Wait()
	}()
	select {
	case <-c:
		return true
	case <-time.After(timeout):
		return false
	}
}
