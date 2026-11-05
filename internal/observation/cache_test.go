package observation

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestCache_ReuseAndInvalidate(t *testing.T) {
	var c Cache[int, int]
	var calls int
	probe := func(context.Context) (int, error) { calls++; return calls, nil }
	for range 2 {
		got, err := c.Get(t.Context(), 1, time.Minute, probe)
		if err != nil || got != 1 {
			t.Fatalf("got=%d err=%v calls=%d", got, err, calls)
		}
	}
	c.Invalidate()
	if got, _ := c.Get(t.Context(), 1, time.Minute, probe); got != 2 {
		t.Fatalf("after invalidate=%d", got)
	}
	if got, _ := c.Get(t.Context(), 2, time.Minute, probe); got != 3 {
		t.Fatalf("new identity=%d", got)
	}
}

func TestCache_FailedResultsAreNotReused(t *testing.T) {
	var c Cache[int, int]
	var calls int
	failure := errors.New("probe failed")
	probe := func(context.Context) (int, error) {
		calls++
		if calls == 1 {
			return 0, failure
		}
		return 2, nil
	}
	if _, err := c.Get(t.Context(), 1, time.Minute, probe); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if got, err := c.Get(t.Context(), 1, time.Minute, probe); err != nil || got != 2 {
		t.Fatalf("got=%d err=%v", got, err)
	}
	if got, err := c.Get(t.Context(), 1, time.Minute, probe); err != nil || got != 2 || calls != 2 {
		t.Fatalf("calls=%d got=%d err=%v", calls, got, err)
	}
}

func TestCache_CoalescesConcurrentZeroTTL(t *testing.T) {
	var c Cache[int, int]
	var calls atomic.Int32
	entered, release := make(chan struct{}, 2), make(chan struct{})
	probe := func(ctx context.Context) (int, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return 1, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	done := make(chan error, 2)
	for range 2 {
		go func() { _, err := c.Get(t.Context(), 1, 0, probe); done <- err }()
	}
	<-entered

	awaitWaiters(t, &c, 2)

	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
}

func awaitWaiters[K comparable, V any](t *testing.T, c *Cache[K, V], n int) {
	t.Helper()
	limit := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		got := 0
		if c.running != nil {
			got = c.running.waiters
		}
		c.mu.Unlock()
		if got == n {
			return
		}
		if time.Now().After(limit) {
			t.Fatalf("waiters=%d want %d", got, n)
		}
		runtime.Gosched()
	}
}
func TestCache_ExpiryFreshAndCompletionTime(t *testing.T) {
	var seconds atomic.Int64
	var c Cache[int, int]
	c.now = func() time.Time { return time.Unix(seconds.Load(), 0) }
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	probe := func(context.Context) (int, error) {
		n := calls.Add(1)
		if n == 1 {
			close(entered)
			<-release
		}
		return int(n), nil
	}
	done := make(chan error, 1)
	go func() { _, err := c.Get(t.Context(), 1, 3*time.Second, probe); done <- err }()
	<-entered
	seconds.Store(10)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	seconds.Store(12)
	if got, _ := c.Get(t.Context(), 1, 3*time.Second, probe); got != 1 {
		t.Fatal("expiry counted probe execution time")
	}
	seconds.Store(13)
	if got, _ := c.Get(t.Context(), 1, 3*time.Second, probe); got != 2 {
		t.Fatal("expired result reused")
	}
	if got, _ := c.Get(WithFresh(t.Context()), 1, 3*time.Second, probe); got != 3 {
		t.Fatal("explicit refresh used cache")
	}
	if got, _ := c.Get(t.Context(), 1, 0, probe); got != 4 {
		t.Fatal("zero TTL used completed result")
	}
}
func TestCache_OneWaiterCancelKeepsOtherProbe(t *testing.T) {
	var c Cache[int, int]
	entered, release := make(chan struct{}), make(chan struct{})
	probe := func(ctx context.Context) (int, error) {
		close(entered)
		select {
		case <-release:
			return 7, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	a, b := make(chan error, 1), make(chan error, 1)
	go func() { _, err := c.Get(ctx, 1, time.Minute, probe); a <- err }()
	<-entered
	go func() {
		v, err := c.Get(t.Context(), 1, time.Minute, probe)
		if err == nil && v != 7 {
			err = errors.New("wrong result")
		}
		b <- err
	}()
	awaitWaiters(t, &c, 2)
	cancel()
	if err := <-a; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err := <-b; err != nil {
		t.Fatal(err)
	}
}
func TestCache_CanceledNativeProbeDoesNotPileUp(t *testing.T) {
	var c Cache[int, int]
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	probe := func(context.Context) (int, error) {
		n := calls.Add(1)
		if n == 1 {
			close(entered)
			<-release
		}
		return int(n), nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := c.Get(ctx, 1, 0, probe); done <- err }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	limited, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer stop()
	if _, err := c.Get(limited, 2, 0, probe); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("canceled native calls piled up")
	}
	close(release)
	if got, err := c.Get(t.Context(), 2, time.Minute, probe); err != nil || got != 2 {
		t.Fatalf("got=%d err=%v", got, err)
	}
	c.Close()
	if _, err := c.Get(t.Context(), 2, time.Minute, probe); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestCache_InvalidationRejectsLateResult(t *testing.T) {
	var c Cache[int, int]
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := c.Get(t.Context(), 1, time.Minute, func(context.Context) (int, error) { close(entered); <-release; return 9, nil })
		done <- err
	}()
	<-entered
	c.Invalidate()
	if err := <-done; !errors.Is(err, ErrInvalidated) {
		t.Fatal(err)
	}
	close(release)
	if got, err := c.Get(t.Context(), 2, time.Minute, func(context.Context) (int, error) { return 10, nil }); err != nil || got != 10 {
		t.Fatalf("got=%d err=%v", got, err)
	}
}
func TestCache_ProbeDeadline(t *testing.T) {
	var c Cache[int, int]
	c.timeout = 20 * time.Millisecond
	if _, err := c.Get(t.Context(), 1, time.Minute, func(ctx context.Context) (int, error) { <-ctx.Done(); return 0, ctx.Err() }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestCache_FreshFailureRetiresPreviousSuccess(t *testing.T) {
	var c Cache[int, int]
	calls := 0
	failure := errors.New("fresh probe failed")
	probe := func(context.Context) (int, error) {
		calls++
		if calls == 2 {
			return 0, failure
		}
		return calls, nil
	}
	if _, err := c.Get(t.Context(), 1, time.Minute, probe); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(WithFresh(t.Context()), 1, time.Minute, probe); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if got, err := c.Get(t.Context(), 1, time.Minute, probe); err != nil || got != 3 {
		t.Fatalf("known failure resurrected old success: got=%d err=%v", got, err)
	}
}

func TestCache_DisplacedFlightRequestsNewIdentity(t *testing.T) {
	var c Cache[int, int]
	entered, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		_, err := c.Get(t.Context(), 1, 0, func(context.Context) (int, error) {
			close(entered)
			<-release
			return 1, nil
		})
		first <- err
	}()
	<-entered
	second := make(chan error, 1)
	go func() {
		_, err := c.Get(t.Context(), 2, 0, func(context.Context) (int, error) { return 2, nil })
		second <- err
	}()
	err := <-first
	close(release)
	if !errors.Is(err, ErrInvalidated) {
		t.Errorf("displacement must request identity refresh, got %v", err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}
