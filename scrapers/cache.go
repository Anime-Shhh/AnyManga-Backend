package scrapers

import (
	"sync"
	"time"
)

type cachedValue[T any] struct {
	value   T
	expires time.Time
}

type pendingLoad[T any] struct {
	done  chan struct{}
	value T
	err   error
}

type cache[T any] struct {
	mu      sync.Mutex
	values  map[string]cachedValue[T]
	pending map[string]*pendingLoad[T]
	limit   int
	ttl     time.Duration
}

func newCache[T any](limit int, ttl time.Duration) *cache[T] {
	return &cache[T]{
		values: make(map[string]cachedValue[T]), pending: make(map[string]*pendingLoad[T]),
		limit: limit, ttl: ttl,
	}
}

func (c *cache[T]) get(key string, load func() (T, error)) (T, error) {
	c.mu.Lock()
	if entry, ok := c.values[key]; ok && time.Now().Before(entry.expires) {
		c.mu.Unlock()
		return entry.value, nil
	}
	if pending, ok := c.pending[key]; ok {
		c.mu.Unlock()
		<-pending.done
		return pending.value, pending.err
	}
	pending := &pendingLoad[T]{done: make(chan struct{})}
	c.pending[key] = pending
	c.mu.Unlock()

	pending.value, pending.err = load()

	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, key)
	if pending.err == nil {
		if _, exists := c.values[key]; !exists && len(c.values) >= c.limit {
			var oldest string
			var expires time.Time
			for existing, entry := range c.values {
				if expires.IsZero() || entry.expires.Before(expires) {
					oldest, expires = existing, entry.expires
				}
			}
			delete(c.values, oldest)
		}
		c.values[key] = cachedValue[T]{value: pending.value, expires: time.Now().Add(c.ttl)}
	}
	close(pending.done)
	return pending.value, pending.err
}
