package sync

import (
	"sync"
)

// KeyLock manages named mutexes for granular locking
type KeyLock struct {
	locks sync.Map
}

// NewKeyLock creates a new KeyLock instance
func NewKeyLock() *KeyLock {
	return &KeyLock{}
}

// Lock acquires a lock for the specific key
func (l *KeyLock) Lock(key string) {
	// LoadOrStore returns the existing value if present, or stores and returns the new value
	// We use a pointer to sync.Mutex so we can share the actual mutex instance
	val, _ := l.locks.LoadOrStore(key, &sync.Mutex{})
	mu := val.(*sync.Mutex)
	mu.Lock()
}

// Unlock releases the lock for the specific key
func (l *KeyLock) Unlock(key string) {
	val, ok := l.locks.Load(key)
	if !ok {
		return
	}
	mu := val.(*sync.Mutex)
	mu.Unlock()

	// Optional: We could try to cleanup unused locks, but that's complex to do safely without race conditions.
	// For PR IDs which are finite but numerous, a better approach for long term might be needed,
	// but for now relying on Go's GC for deleted entries or just keeping them is fine for typical uptime.
	// To strictly prevent memory growth, one would need ref counting.
	// Given "Simplicity", keeping the Map entry is acceptable acceptable unless millions of unique PRs.
}

// TryLock attempts to acquire the lock, returning true if successful
func (l *KeyLock) TryLock(key string) bool {
	val, _ := l.locks.LoadOrStore(key, &sync.Mutex{})
	mu := val.(*sync.Mutex)
	return mu.TryLock()
}
