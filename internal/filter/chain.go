package filter

import (
	"fmt"
	"sync"
)

// FilterChain applies multiple filters in sequence
type FilterChain struct {
	filters []ResponseFilter
}

// NewFilterChain creates a new FilterChain
func NewFilterChain(filters ...ResponseFilter) *FilterChain {
	return &FilterChain{filters: filters}
}

// Filter applies all filters in sequence
func (c *FilterChain) Filter(toolName string, response any) any {
	result := response
	for _, f := range c.filters {
		result = f.Filter(toolName, result)
	}
	return result
}

// Add adds a filter to the chain
func (c *FilterChain) Add(f ResponseFilter) {
	c.filters = append(c.filters, f)
}

// Registry for filter factories
type Factory func(config map[string]interface{}) (ResponseFilter, error)

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Factory)
)

// Register registers a filter factory
func Register(name string, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = f
}

// Create creates a filter by name with config
func Create(name string, config map[string]interface{}) (ResponseFilter, error) {
	registryMu.RLock()
	factory, ok := registry[name]
	registryMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("filter not found: %s", name)
	}
	return factory(config)
}
