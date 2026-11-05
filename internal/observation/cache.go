// Package observation shares bounded, process-local read-only probes.
package observation

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrInvalidated asks an owner to retry with its current observation identity.
var ErrInvalidated = errors.New("observation invalidated")

type freshKey struct{}

// WithFresh bypasses completed results, but permits joining a valid live probe.
func WithFresh(ctx context.Context) context.Context { return context.WithValue(ctx, freshKey{}, true) }

// Fresh reports whether this request explicitly requires a fresh observation.
func Fresh(ctx context.Context) bool { v, _ := ctx.Value(freshKey{}).(bool); return v }

type result[K comparable, V any] struct {
	key   K
	value V
	at    time.Time
}
type flight[K comparable, V any] struct {
	key        K
	generation uint64
	canceled   chan struct{}
	cancelErr  error
	cancel     context.CancelFunc
	done       chan struct{}
	waiters    int
	complete   bool
	value      V
	err        error
}

// Cache holds at most one running probe, including a canceled native call that
// has not returned yet. Values must be immutable; callers clone mutable data.
type Cache[K comparable, V any] struct {
	mu         sync.Mutex
	entry      *result[K, V]
	running    *flight[K, V]
	generation uint64
	closed     bool
	now        func() time.Time
	timeout    time.Duration
}

func (c *Cache[K, V]) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
func (c *Cache[K, V]) limit() time.Duration {
	if c.timeout > 0 {
		return c.timeout
	}
	return 5 * time.Second
}

// Get reuses successful, unexpired results. A canceled waiter never owns the
// other waiters' execution context. Probe lifetime belongs to Cache.
func (c *Cache[K, V]) Get(ctx context.Context, key K, ttl time.Duration, probe func(context.Context) (V, error)) (V, error) {
	var zero V
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	var waitCtx context.Context

	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return zero, context.Canceled
		}
		if c.entry != nil && c.entry.key == key && ttl > 0 && !Fresh(ctx) && c.clock().Sub(c.entry.at) < ttl {
			value := c.entry.value
			c.mu.Unlock()
			return value, nil
		}
		if waitCtx == nil {
			var cancel context.CancelFunc
			waitCtx, cancel = context.WithTimeout(ctx, c.limit())
			defer cancel()
		}
		if err := waitCtx.Err(); err != nil {
			c.mu.Unlock()
			return zero, err
		}
		f := c.running
		if f != nil && (f.key != key || f.generation != c.generation || f.cancelErr != nil) {
			c.cancelFlight(f)
			done := f.done
			c.mu.Unlock()
			select {
			case <-done:
				continue
			case <-waitCtx.Done():
				return zero, waitCtx.Err()
			}
		}
		if f == nil {
			// A failed explicit refresh must not resurrect a prior success.
			c.entry = nil
			worker, cancelWorker := context.WithTimeout(context.Background(), c.limit())
			f = &flight[K, V]{key: key, generation: c.generation, cancel: cancelWorker, done: make(chan struct{}), canceled: make(chan struct{})}
			c.running = f
			stopCancellation := context.AfterFunc(worker, func() {
				c.mu.Lock()
				f.cancelErr = worker.Err()
				close(f.canceled)
				c.mu.Unlock()
			})
			go c.execute(worker, f, probe, stopCancellation)
		}
		f.waiters++
		c.mu.Unlock()
		select {
		case <-f.done:
		case <-f.canceled:
		case <-waitCtx.Done():
		}
		c.mu.Lock()
		f.waiters--
		complete, value, err := f.complete, f.value, f.err
		cancelErr := f.cancelErr
		invalid := f.generation != c.generation
		if !complete && f.waiters == 0 {
			c.cancelFlight(f)
		}
		c.mu.Unlock()
		if invalid {
			return zero, ErrInvalidated
		}
		if complete {
			return value, err
		}
		if err := waitCtx.Err(); err != nil {
			return zero, err
		}
		return zero, cancelErr
	}
}

func (c *Cache[K, V]) execute(ctx context.Context, f *flight[K, V], probe func(context.Context) (V, error), stopCancellation func() bool) {
	value, err := probe(ctx)
	stopCancellation()
	c.mu.Lock()
	if f.generation != c.generation || c.closed {
		err = ErrInvalidated
	} else if ctx.Err() != nil {
		err = ctx.Err()
	}
	f.value, f.err, f.complete = value, err, true
	if err == nil {
		c.entry = &result[K, V]{key: f.key, value: value, at: c.clock()}
	}
	c.running = nil
	close(f.done)
	c.mu.Unlock()
	f.cancel()
}

// Invalidate retires cached and in-flight observations.
func (c *Cache[K, V]) Invalidate() {
	c.mu.Lock()
	c.generation++
	c.entry = nil
	if c.running != nil {
		c.cancelFlight(c.running)
	}
	c.mu.Unlock()
}

// Close cancels probes owned by this cache and rejects future requests.
func (c *Cache[K, V]) Close() {
	c.mu.Lock()
	c.closed = true
	c.generation++
	c.entry = nil
	if c.running != nil {
		c.cancelFlight(c.running)
	}
	c.mu.Unlock()
}

// cancelFlight is called with mu held. It marks retirement synchronously even
// when context cancellation's callback has not run yet.
func (c *Cache[K, V]) cancelFlight(f *flight[K, V]) {
	if f.cancelErr == nil {
		f.cancelErr = context.Canceled
	}
	f.cancel()
}
