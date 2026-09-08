package supervisor

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type descendantTestChild struct {
	*maintenanceChild
	waitDescendants func(context.Context) error
}

func (c *descendantTestChild) WaitDescendants(ctx context.Context) error {
	return c.waitDescendants(ctx)
}

type descendantTestStarter struct {
	child   Child
	started chan struct{}
	starts  atomic.Int32
}

func (s *descendantTestStarter) Start() (Child, error) {
	if s.starts.Add(1) != 1 {
		return nil, errors.New("unexpected second core start")
	}
	close(s.started)
	return s.child, nil
}

func newDescendantTestChild(wait func(context.Context) error) *descendantTestChild {
	return &descendantTestChild{maintenanceChild: &maintenanceChild{done: make(chan struct{}), joined: make(chan struct{})}, waitDescendants: wait}
}

func TestSupervisor_DescendantFailureBlocksMaintenanceAndRestart(t *testing.T) {
	for _, operation := range []string{"maintenance", "restart"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			failure := errors.New("descendant remains alive")
			child := newDescendantTestChild(func(context.Context) error { return failure })
			starter := &descendantTestStarter{child: child, started: make(chan struct{})}
			s := New(Options{Starter: starter})
			done := make(chan error, 1)
			go func() { done <- s.Run(ctx) }()
			t.Cleanup(func() {
				cancel()
				if err := waitDone(t, done); err != nil {
					t.Errorf("supervisor shutdown: %v", err)
				}
			})
			<-starter.started
			commits := 0
			var err error
			if operation == "maintenance" {
				err = s.Maintain(ctx, func() error { commits++; return nil })
			} else {
				err = s.Restart(ctx)
			}
			if !errors.Is(err, failure) || commits != 0 {
				t.Fatalf("descendant failure permitted %s: err=%v commits=%d", operation, err, commits)
			}
			// Both requests must be answered while blocked, without starting again.
			if err := s.Restart(ctx); err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("blocked restart did not reject promptly: %v", err)
			}
			if err := s.Maintain(ctx, func() error { commits++; return nil }); err == nil || commits != 0 {
				t.Fatalf("blocked maintenance committed: %v", err)
			}
			if starter.starts.Load() != 1 {
				t.Fatal("started a new core with unresolved descendants")
			}
		})
	}
}

func TestSupervisor_UnexpectedExitChecksDescendantsBeforeIdleMaintenance(t *testing.T) {
	failure := errors.New("unresolved process group")
	child := newDescendantTestChild(func(context.Context) error { return failure })
	close(child.done)
	s := New(Options{})
	err, explicit := s.runChild(context.Background(), child, 0)
	if !errors.Is(err, failure) || explicit {
		t.Fatalf("unexpected exit bypassed descendants: err=%v explicit=%v", err, explicit)
	}
	commits := 0
	if err := s.Maintain(context.Background(), func() error { commits++; return nil }); err == nil || commits != 0 {
		t.Fatal("idle maintenance forgot unresolved descendants")
	}
}

func TestSupervisor_MaintenanceWaitsForDescendantsAfterLeaderExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	checking, release := make(chan struct{}), make(chan struct{})
	child := newDescendantTestChild(func(ctx context.Context) error {
		close(checking)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	starter := &descendantTestStarter{child: child, started: make(chan struct{})}
	s := New(Options{Starter: starter})
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := waitDone(t, done); err != nil {
			t.Errorf("supervisor shutdown: %v", err)
		}
	})
	<-starter.started
	committed := make(chan struct{}, 1)
	result := make(chan error, 1)
	go func() { result <- s.Maintain(ctx, func() error { committed <- struct{}{}; return nil }) }()
	select {
	case <-checking:
	case err := <-result:
		t.Fatalf("maintenance completed before descendant wait: %v", err)
	case <-ctx.Done():
		t.Fatal("descendant wait was not reached")
	}
	select {
	case <-committed:
		t.Fatal("maintenance committed with live descendants")
	default:
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if len(committed) != 1 {
		t.Fatal("maintenance did not commit after descendants exited")
	}
}
