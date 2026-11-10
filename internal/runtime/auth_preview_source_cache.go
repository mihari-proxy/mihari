package runtime

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"sync"
)

type authPreviewSourceKey struct {
	service    *subscription.Service
	id         string
	generation uint64
}
type authPreviewSourceCache struct {
	mu    sync.Mutex
	entry *authPreviewSourceEntry
}
type authPreviewSourceEntry struct {
	key      authPreviewSourceKey
	ready    chan struct{}
	document subscription.Document
	err      error
}

func (c *authPreviewSourceCache) load(ctx context.Context, key authPreviewSourceKey, read func() (subscription.Document, error)) (subscription.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if entry := c.entry; entry != nil && entry.key == key {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-entry.ready:
			return entry.document, entry.err
		}
	}
	entry := &authPreviewSourceEntry{key: key, ready: make(chan struct{})}
	c.entry = entry
	c.mu.Unlock()
	var auth subscription.Document
	var err error
	completed := false
	defer func() {
		if !completed {
			err = errors.New("authentication preview source load interrupted")
		}
		c.mu.Lock()
		entry.document, entry.err = auth, err
		if err != nil && c.entry == entry {
			c.entry = nil
		}
		close(entry.ready)
		c.mu.Unlock()
	}()
	// Only the loading request performs IO. Waiters can cancel independently;
	// neither parsing nor disk access runs while the cache mutex is held.
	document, err := read()
	if err == nil {
		auth = subscription.Document{}
		for _, field := range []string{"authentication", "skip-auth-prefixes"} {
			if value, exists := document[field]; exists {
				auth[field] = value
			}
		}
	}
	completed = true
	return auth, err
}
