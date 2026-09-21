package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestApplicationUpdate_DrainsCacheOverflowAndNestedWork(t *testing.T) {
	m := New(Options{})
	const count = 257
	var started, finished sync.WaitGroup
	started.Add(count)
	finished.Add(count)
	release := make(chan struct{})
	for i := 0; i < count; i++ {
		go func() {
			defer finished.Done()
			_, err := m.doOperation(context.Background(), fmt.Sprintf("accepted:%d", i), func(ctx context.Context) (any, error) {
				started.Done()
				<-release
				return m.doOperation(ctx, "nested:", func(context.Context) (any, error) { return nil, nil })
			})
			if err != nil {
				t.Errorf("accepted operation rejected: %v", err)
			}
		}()
	}
	started.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.PrepareApplicationUpdate(ctx, "cancelled"); !errors.Is(err, context.Canceled) {
		t.Errorf("drain returned %v", err)
	}
	// Close admission while the accepted operations finish their nested work.
	m.applicationUpdate.mu.Lock()
	m.applicationUpdate.owner = "pending"
	m.applicationUpdate.mu.Unlock()
	close(release)
	finished.Wait()
	if err := m.PrepareApplicationUpdate(context.Background(), "pending"); err != nil {
		t.Fatal(err)
	}
	if err := m.ReleaseApplicationUpdate("pending"); err != nil {
		t.Fatal(err)
	}
}

// TestApplicationUpdate_DrainsAcceptedWork includes work outside the commit mutex.
func TestApplicationUpdate_DrainsAcceptedWork(t *testing.T) {
	m := New(Options{})
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = m.doOperation(context.Background(), "fixture:", func(context.Context) (any, error) { close(started); <-release; return nil, nil })
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := m.PrepareApplicationUpdate(ctx, "fixture-owner")
	close(release)
	<-done
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("prepared while accepted write is live: %v", err)
	}
	if _, err := m.doOperation(context.Background(), "after-timeout:", func(context.Context) (any, error) { return nil, nil }); err != nil {
		t.Fatalf("timeout left gate closed: %v", err)
	}
}

// TestApplicationUpdate_BlocksNewWorkAndInternalWrites tests the prepared interval and owner release.
func TestApplicationUpdate_BlocksNewWorkAndInternalWrites(t *testing.T) {
	m := New(Options{})
	if err := m.PrepareApplicationUpdate(context.Background(), "owner"); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err := m.doOperation(context.Background(), "new:", func(context.Context) (any, error) { called = true; return nil, nil })
	if err == nil || called {
		t.Fatal("accepted new mutation during preparation")
	}
	if err := m.lockMaintenance(context.Background()); err == nil {
		m.unlock()
		t.Fatal("internal write admitted")
	}
	if err := m.ReleaseApplicationUpdate("other"); err == nil {
		t.Fatal("foreign owner released gate")
	}
	if err := m.ReleaseApplicationUpdate("owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.doOperation(context.Background(), "resumed:", func(context.Context) (any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
}
