package pipeline

import (
	"context"
	"log/slog"
	"sync"
)

// FileCache ensures each file path is fetched at most once during the pipeline lifecycle.
type FileCache struct {
	mu    sync.RWMutex
	cache map[string]string // path -> content
}

// NewFileCache creates a new FileCache instance.
func NewFileCache() *FileCache {
	return &FileCache{
		cache: make(map[string]string),
	}
}

// Put stores content in the cache.
// Typically used by Stage1 when it extracts diff content.
func (c *FileCache) Put(path, content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[path] = content
}

// Has checks if the path exists in the cache.
func (c *FileCache) Has(path string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.cache[path]
	return ok
}

// GetOrFetch returns the cached content if available.
// If valid content is not found, it calls the fetcher function, caches the result, and returns it.
func (c *FileCache) GetOrFetch(ctx context.Context, path string, fetcher func() (string, error)) (string, error) {
	c.mu.RLock()
	if content, ok := c.cache[path]; ok {
		c.mu.RUnlock()
		slog.Debug("FileCache hit", "path", path)
		return content, nil
	}
	c.mu.RUnlock()

	// Cache miss, fetch
	slog.Debug("FileCache miss, fetching...", "path", path)
	content, err := fetcher()
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	c.cache[path] = content
	c.mu.Unlock()

	return content, nil
}
