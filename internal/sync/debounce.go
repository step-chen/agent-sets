package sync

import (
	"sync"
	"time"
)

// Debouncer manages delayed execution of tasks for specific keys
type Debouncer struct {
	mu      sync.Mutex
	pending map[string]*time.Timer
	ttl     time.Duration
}

// NewDebouncer creates a new Debouncer with specific TTL
func NewDebouncer(ttl time.Duration) *Debouncer {
	return &Debouncer{
		pending: make(map[string]*time.Timer),
		ttl:     ttl,
	}
}

// Add schedules a function to be executed after TTL.
// If called again with the same key before TTL, the previous timer is cancelled and reset.
func (d *Debouncer) Add(key string, fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if timer, ok := d.pending[key]; ok {
		timer.Stop()
	}

	d.pending[key] = time.AfterFunc(d.ttl, func() {
		// Cleanup the map entry when firing
		d.mu.Lock()
		delete(d.pending, key)
		d.mu.Unlock()

		// Execute the function
		fn()
	})
}

// Cancel stops a pending debounce task if it exists
func (d *Debouncer) Cancel(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if timer, ok := d.pending[key]; ok {
		timer.Stop()
		delete(d.pending, key)
	}
}
