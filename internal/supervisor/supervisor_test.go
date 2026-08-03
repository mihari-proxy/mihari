package supervisor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSupervisorRestartsCrashAfterExponentialBackoff(t *testing.T) {
	starter := newFakeStarter()
	waiter := newFakeWaiter()
	observations := &observationLog{}
	supervisor := New(Options{
		Starter: starter,
		Waiter:  waiter,
		Now:     time.Now,
		Observe: observations.add,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()

	first := starter.next(t)
	first.exit(errors.New("crashed"))
	firstDelay := waiter.next(t)
	if firstDelay.duration != time.Second {
		t.Fatalf("first delay=%s", firstDelay.duration)
	}
	firstDelay.release()
	second := starter.next(t)
	second.exit(errors.New("crashed again"))
	secondDelay := waiter.next(t)
	if secondDelay.duration != 2*time.Second {
		t.Fatalf("second delay=%s", secondDelay.duration)
	}

	cancel()
	if err := waitDone(t, done); err != nil {
		t.Fatal(err)
	}
	if !observations.containsStatus(StatusBackoff) {
		t.Fatalf("observations=%#v", observations.snapshot())
	}
}

func TestSupervisorRestartsAfterThreeHealthFailures(t *testing.T) {
	starter := newFakeStarter()
	waiter := newFakeWaiter()
	healthCalls := make(chan struct{}, 3)
	supervisor := New(Options{
		Starter: starter,
		Waiter:  waiter,
		Now:     time.Now,
		Health: func(context.Context) error {
			healthCalls <- struct{}{}
			return errors.New("unhealthy")
		},
		GracePeriod:    5 * time.Second,
		HealthInterval: 10 * time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()

	child := starter.next(t)
	waiter.next(t).release()
	for failure := 0; failure < 3; failure++ {
		select {
		case <-healthCalls:
		case <-time.After(3 * time.Second):
			t.Fatalf("health check %d did not run", failure+1)
		}
		if failure < 2 {
			waiter.next(t).release()
		}
	}
	select {
	case <-child.terminated:
	case <-time.After(3 * time.Second):
		t.Fatal("unhealthy child was not terminated")
	}
	cancel()
	if err := waitDone(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorExplicitRestartDoesNotWaitForBackoff(t *testing.T) {
	starter := newFakeStarter()
	supervisor := New(Options{Starter: starter, Waiter: realWaiter{}, Now: time.Now})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()

	first := starter.next(t)
	restarted := make(chan error, 1)
	go func() { restarted <- supervisor.Restart(context.Background()) }()
	select {
	case <-first.terminated:
	case <-time.After(3 * time.Second):
		t.Fatal("restart did not terminate the first child")
	}
	if err := <-restarted; err != nil {
		t.Fatal(err)
	}
	starter.next(t)
	cancel()
	if err := waitDone(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorCancellationDuringBackoffStopsPromptly(t *testing.T) {
	starter := newFakeStarter()
	waiter := newFakeWaiter()
	supervisor := New(Options{Starter: starter, Waiter: waiter, Now: time.Now})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	starter.next(t).exit(errors.New("crashed"))
	waiter.next(t)
	cancel()
	if err := waitDone(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorStableRuntimeResetsBackoff(t *testing.T) {
	starter := newFakeStarter()
	waiter := newFakeWaiter()
	currentTime := time.Unix(100, 0)
	var timeMu sync.Mutex
	now := func() time.Time {
		timeMu.Lock()
		defer timeMu.Unlock()
		return currentTime
	}
	setTime := func(value time.Time) {
		timeMu.Lock()
		defer timeMu.Unlock()
		currentTime = value
	}
	supervisor := New(Options{Starter: starter, Waiter: waiter, Now: now, StableAfter: 30 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()

	starter.next(t).exit(errors.New("first crash"))
	firstDelay := waiter.next(t)
	if firstDelay.duration != time.Second {
		t.Fatalf("first delay=%s", firstDelay.duration)
	}
	firstDelay.release()
	second := starter.next(t)
	setTime(time.Unix(131, 0))
	second.exit(errors.New("crash after stable runtime"))
	if delay := waiter.next(t).duration; delay != time.Second {
		t.Fatalf("delay after stable runtime=%s", delay)
	}
	cancel()
	if err := waitDone(t, done); err != nil {
		t.Fatal(err)
	}
}

type fakeStarter struct {
	started chan *fakeChild
	nextPID atomic.Int64
}

func newFakeStarter() *fakeStarter {
	return &fakeStarter{started: make(chan *fakeChild, 16)}
}

func (s *fakeStarter) Start() (Child, error) {
	child := &fakeChild{
		pid:        int(s.nextPID.Add(1)),
		done:       make(chan error, 1),
		terminated: make(chan struct{}),
	}
	s.started <- child
	return child, nil
}

func (s *fakeStarter) next(t *testing.T) *fakeChild {
	t.Helper()
	select {
	case child := <-s.started:
		return child
	case <-time.After(3 * time.Second):
		t.Fatal("child did not start")
		return nil
	}
}

type fakeChild struct {
	pid        int
	done       chan error
	terminated chan struct{}
	exitOnce   sync.Once
}

func (c *fakeChild) PID() int { return c.pid }

func (c *fakeChild) Wait() error { return <-c.done }

func (c *fakeChild) Terminate() error {
	c.exitOnce.Do(func() {
		close(c.terminated)
		c.done <- errors.New("terminated by supervisor")
	})
	return nil
}

func (c *fakeChild) Kill() error { return c.Terminate() }

func (c *fakeChild) exit(err error) {
	c.exitOnce.Do(func() { c.done <- err })
}

type waitRequest struct {
	duration time.Duration
	done     chan struct{}
}

func (r waitRequest) release() { close(r.done) }

type fakeWaiter struct{ requests chan waitRequest }

func newFakeWaiter() *fakeWaiter {
	return &fakeWaiter{requests: make(chan waitRequest, 32)}
}

func (w *fakeWaiter) Wait(ctx context.Context, duration time.Duration) error {
	request := waitRequest{duration: duration, done: make(chan struct{})}
	select {
	case w.requests <- request:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-request.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *fakeWaiter) next(t *testing.T) waitRequest {
	t.Helper()
	select {
	case request := <-w.requests:
		return request
	case <-time.After(3 * time.Second):
		t.Fatal("wait was not requested")
		return waitRequest{}
	}
}

type observationLog struct {
	mu     sync.Mutex
	values []Observation
}

func (l *observationLog) add(observation Observation) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.values = append(l.values, observation)
}

func (l *observationLog) snapshot() []Observation {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Observation(nil), l.values...)
}

func (l *observationLog) containsStatus(status Status) bool {
	for _, observation := range l.snapshot() {
		if observation.Status == status {
			return true
		}
	}
	return false
}

func waitDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor did not stop")
		return nil
	}
}
