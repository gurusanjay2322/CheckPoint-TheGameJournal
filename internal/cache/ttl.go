// Package cache provides a small in-process TTL cache for slow or
// rate-limited upstream calls (e.g. the Steam store).
package cache

import (
	"sync"
	"time"
)

type entry[V any] struct {
	value   V
	expires time.Time
}

type TTL[K comparable, V any] struct {
	mu    sync.Mutex
	ttl   time.Duration
	items map[K]entry[V]
}

func NewTTL[K comparable, V any](ttl time.Duration) *TTL[K, V] {
	return &TTL[K, V]{ttl: ttl, items: make(map[K]entry[V])}
}

func (c *TTL[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok || time.Now().After(e.expires) {
		delete(c.items, key)
		var zero V
		return zero, false
	}
	return e.value, true
}

func (c *TTL[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = entry[V]{value: value, expires: time.Now().Add(c.ttl)}
}
