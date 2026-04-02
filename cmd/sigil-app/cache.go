package main

import (
	"sync"
	"time"

	"github.com/wambozi/sigil/internal/socket"
)

// responseCache is a simple TTL cache for socket responses.
// It reduces redundant daemon calls during rapid view switching.
type responseCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	resp   socket.Response
	expiry time.Time
}

func newResponseCache(ttl time.Duration) *responseCache {
	return &responseCache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
	}
}

func (c *responseCache) get(key string) (socket.Response, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiry) {
		return socket.Response{}, false
	}
	return e.resp, true
}

func (c *responseCache) set(key string, resp socket.Response) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{
		resp:   resp,
		expiry: time.Now().Add(c.ttl),
	}
}

func (c *responseCache) invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}
