package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/core"
)

func TestUpdateAdoptsHealthyTrialWithoutStartingAnotherProcess(t *testing.T) {
	starter := newFakeStarter()
	s := New(Options{Starter: starter})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	old := starter.next(t)
	updated := make(chan error, 1)
	go func() {
		updated <- s.Update(ctx, func(session *UpdateSession) error {
			if !session.WasRunning() {
				t.Error("running intent lost")
			}
			select {
			case <-old.terminated:
			default:
				t.Error("update began before stopping old core")
			}
			return session.Start(ctx)
		})
	}()
	if err := waitDone(t, updated); err != nil {
		t.Fatal(err)
	}
	trial := starter.next(t)
	select {
	case <-trial.terminated:
		t.Fatal("healthy trial was stopped after update")
	default:
	}
	cancel()
	if err := waitDone(t, done); err != nil {
		t.Fatal(err)
	}
	if starter.nextPID.Load() != 2 {
		t.Fatalf("processes started=%d", starter.nextPID.Load())
	}
}

type trialHealthKey struct{}
type trialWaiter struct{}

func (trialWaiter) Wait(ctx context.Context, _ time.Duration) error {
	if ctx.Value(trialHealthKey{}) != nil {
		return ctx.Err()
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestUpdateHealthFailureCanRestoreAndAdoptOldCore(t *testing.T) {
	starter := newFakeStarter()
	unhealthy := errors.New("candidate health failed")
	s := New(Options{Starter: starter, Waiter: trialWaiter{}, Health: func(context.Context) error {
		if starter.nextPID.Load() == 2 {
			return unhealthy
		}
		return nil
	}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	starter.next(t)
	trialCtx := context.WithValue(ctx, trialHealthKey{}, true)
	result := make(chan error, 1)
	go func() {
		result <- s.Update(trialCtx, func(session *UpdateSession) error {
			err := session.Start(trialCtx)
			if !errors.Is(err, unhealthy) {
				return errors.New("trial unexpectedly healthy")
			}
			if stopErr := session.Stop(); stopErr != nil {
				return stopErr
			}
			if restoreErr := session.Start(trialCtx); restoreErr != nil {
				return restoreErr
			}
			return err
		})
	}()
	if err := waitDone(t, result); !errors.Is(err, unhealthy) {
		t.Fatalf("update lost initial failure: %v", err)
	}
	trial := starter.next(t)
	restored := starter.next(t)
	select {
	case <-trial.terminated:
	default:
		t.Fatal("failed trial still running")
	}
	select {
	case <-restored.terminated:
		t.Fatal("restored process stopped")
	default:
	}
	cancel()
	if err := waitDone(t, done); err != nil {
		t.Fatal(err)
	}
	if starter.nextPID.Load() != 3 {
		t.Fatalf("unexpected automatic restart: %d", starter.nextPID.Load())
	}
}

func TestCommandStarterPreservesExecutionContext(t *testing.T) {
	want := errors.New("stop before actual execution")
	ctx := context.WithValue(t.Context(), trialHealthKey{}, "transaction")
	starter := CommandStarter{CommandFactory: func(received context.Context) (core.CoreCommand, func() error, error) {
		if received.Value(trialHealthKey{}) != "transaction" {
			t.Error("execution authority lost")
		}
		return core.CoreCommand{}, nil, want
	}}
	if _, err := starter.StartContext(ctx); !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}

func TestUpdateIdlePreservesStoppedIntent(t *testing.T) {
	starter := newFakeStarter()
	s := New(Options{Starter: starter})
	called := false
	err := s.Update(t.Context(), func(session *UpdateSession) error {
		called = true
		if session.WasRunning() {
			t.Error("idle update claims running intent")
		}
		return nil
	})
	if err != nil || !called || starter.nextPID.Load() != 0 {
		t.Fatalf("called=%v starts=%d err=%v", called, starter.nextPID.Load(), err)
	}
}

func TestUpdatePendingChildShutdownKeepsCleanupFailure(t *testing.T) {
	starter := newFakeStarter()
	s := New(Options{Starter: starter})
	runCtx, cancelRun := context.WithCancel(t.Context())
	defer cancelRun()
	done := make(chan error, 1)
	go func() { done <- s.Run(runCtx) }()
	starter.next(t)
	failure := errors.New("trial termination failed during shutdown")
	if err := s.Update(t.Context(), func(session *UpdateSession) error {
		if err := session.Start(t.Context()); err != nil {
			return err
		}
		trial := starter.next(t)
		trial.mu.Lock()
		trial.terminateErr = failure
		trial.mu.Unlock()
		cancelRun()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := waitDone(t, done); !errors.Is(err, failure) {
		t.Fatalf("pending-child cleanup failure lost: %v", err)
	}
}
