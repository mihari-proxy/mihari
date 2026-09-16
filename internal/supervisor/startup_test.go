package supervisor

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSupervisor_PreparesEveryStartAndReleasesOwnership(t *testing.T) {
	starter, waiter := newFakeStarter(), newFakeWaiter()
	var preparations, releases atomic.Int32
	s := New(Options{Starter: starter, Waiter: waiter, BeforeStart: func(context.Context) (func(), error) {
		preparations.Add(1)
		return func() { releases.Add(1) }, nil
	}})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := waitDone(t, done); err != nil {
			t.Error(err)
		}
	})
	first := starter.next(t)
	if preparations.Load() != 1 {
		t.Fatal("first child started without preparation")
	}
	first.exit(errors.New("crash"))
	waiter.next(t).release()
	starter.next(t)
	if preparations.Load() != 2 {
		t.Fatal("restart omitted preparation")
	}
	cancel()
	// Run completion is joined in Cleanup; release after Start need not precede next().
	if releases.Load() < 1 {
		t.Fatal("ownership leaked after first start")
	}
}

func TestSupervisor_PreparationFailureDoesNotStartChild(t *testing.T) {
	starter, waiter := newFakeStarter(), newFakeWaiter()
	called := make(chan struct{}, 1)
	s := New(Options{Starter: starter, Waiter: waiter, BeforeStart: func(context.Context) (func(), error) {
		called <- struct{}{}
		return nil, errors.New("validation rejected")
	}})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := waitDone(t, done); err != nil {
			t.Error(err)
		}
	})
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("preparation did not run")
	}
	waiter.next(t)
	if starter.nextPID.Load() != 0 {
		t.Fatal("started child after preparation failure")
	}
}
