package cache

import (
	"sync"
	"time"

	"glamr/internal/provider"
)

// Cache stores MR data with timestamps
type Cache struct {
	mu          sync.RWMutex
	data        []provider.MergeRequest
	lastRefresh time.Time
	ttl         time.Duration
}

// NewCache creates a new cache with the given TTL
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		ttl: ttl,
	}
}

// Get returns cached data if still valid
func (c *Cache) Get() ([]provider.MergeRequest, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if time.Since(c.lastRefresh) > c.ttl {
		return nil, false
	}

	return c.data, true
}

// Set atomically replaces the entire cache
func (c *Cache) Set(data []provider.MergeRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data = data
	c.lastRefresh = time.Now()
}

// LastRefresh returns when the cache was last updated
func (c *Cache) LastRefresh() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.lastRefresh
}

// IsStale checks if cache needs refresh
func (c *Cache) IsStale() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return time.Since(c.lastRefresh) > c.ttl
}
