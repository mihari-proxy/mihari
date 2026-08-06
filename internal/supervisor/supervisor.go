package supervisor

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type Status string

const (
	StatusStopped  Status = "stopped"
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusBackoff  Status = "backoff"
	StatusDegraded Status = "degraded"
)

type Observation struct {
	Status      Status
	PID         int
	Restarts    uint64
	LastError   string
	NextRetryAt time.Time
}

type Child interface {
	PID() int
	Wait() error
	Terminate() error
	Kill() error
}

type Starter interface {
	Start() (Child, error)
}

type Waiter interface {
	Wait(context.Context, time.Duration) error
}

type HealthChecker func(context.Context) error

type Options struct {
	Starter        Starter
	Health         HealthChecker
	Waiter         Waiter
	Now            func() time.Time
	Observe        func(Observation)
	MinimumBackoff time.Duration
	MaximumBackoff time.Duration
	StableAfter    time.Duration
	GracePeriod    time.Duration
	HealthInterval time.Duration
	StopTimeout    time.Duration
}

type Supervisor struct {
	options Options
	restart chan chan error
	active  atomic.Bool
}

func New(options Options) *Supervisor {
	if options.Waiter == nil {
		options.Waiter = realWaiter{}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.MinimumBackoff <= 0 {
		options.MinimumBackoff = time.Second
	}
	if options.MaximumBackoff <= 0 {
		options.MaximumBackoff = 30 * time.Second
	}
	if options.StableAfter <= 0 {
		options.StableAfter = 30 * time.Second
	}
	if options.GracePeriod <= 0 {
		options.GracePeriod = 5 * time.Second
	}
	if options.HealthInterval <= 0 {
		options.HealthInterval = 10 * time.Second
	}
	if options.StopTimeout <= 0 {
		options.StopTimeout = 5 * time.Second
	}
	return &Supervisor{options: options, restart: make(chan chan error)}
}

func (s *Supervisor) Run(ctx context.Context) error {
	if s.options.Starter == nil {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo process starter is unavailable"}
	}
	if !s.active.CompareAndSwap(false, true) {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo supervisor is already running"}
	}
	defer s.active.Store(false)

	backoff := NewBackoff(s.options.MinimumBackoff, s.options.MaximumBackoff)
	var restarts uint64
	for {
		if ctx.Err() != nil {
			s.observe(Observation{Status: StatusStopped, Restarts: restarts})
			return nil
		}
		startedAt := s.options.Now()
		child, err := s.options.Starter.Start()
		if err == nil {
			s.observe(Observation{Status: StatusStarting, PID: child.PID(), Restarts: restarts})
			var explicit bool
			err, explicit = s.runChild(ctx, child, restarts)
			if ctx.Err() != nil {
				s.observe(Observation{Status: StatusStopped, Restarts: restarts})
				return nil
			}
			if explicit {
				restarts++
				continue
			}
		}
		if s.options.Now().Sub(startedAt) >= s.options.StableAfter {
			backoff.Reset()
		}
		delay := backoff.Next()
		observation := Observation{Status: StatusBackoff, Restarts: restarts, NextRetryAt: s.options.Now().Add(delay)}
		if err != nil {
			observation.LastError = err.Error()
		}
		s.observe(observation)
		if err := s.waitBackoff(ctx, delay); err != nil {
			s.observe(Observation{Status: StatusStopped, Restarts: restarts})
			return nil
		}
		restarts++
	}
}

func (s *Supervisor) Restart(ctx context.Context) error {
	if !s.active.Load() {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo supervisor is not running"}
	}
	response := make(chan error, 1)
	select {
	case s.restart <- response:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-response:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Supervisor) runChild(parent context.Context, child Child, restarts uint64) (error, bool) {
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	monitorCtx, cancelMonitor := context.WithCancel(parent)
	healthFailure := make(chan error, 1)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		s.monitor(monitorCtx, child.PID(), restarts, healthFailure)
	}()
	finishMonitor := func() {
		cancelMonitor()
		<-monitorDone
	}

	select {
	case <-parent.Done():
		err := s.stopChild(child, done)
		finishMonitor()
		return err, false
	case response := <-s.restart:
		err := s.stopChild(child, done)
		finishMonitor()
		response <- err
		return err, true
	case err := <-done:
		finishMonitor()
		return err, false
	case err := <-healthFailure:
		stopError := s.stopChild(child, done)
		finishMonitor()
		if stopError != nil {
			return stopError, false
		}
		return err, false
	}
}

func (s *Supervisor) monitor(ctx context.Context, pid int, restarts uint64, failed chan<- error) {
	if s.options.Health == nil {
		s.observe(Observation{Status: StatusRunning, PID: pid, Restarts: restarts})
		<-ctx.Done()
		return
	}
	if err := s.options.Waiter.Wait(ctx, s.options.GracePeriod); err != nil {
		return
	}
	failures := 0
	runningPublished := false
	for {
		err := s.options.Health(ctx)
		if err == nil {
			failures = 0
			if !runningPublished {
				runningPublished = true
				s.observe(Observation{Status: StatusRunning, PID: pid, Restarts: restarts})
			}
		} else {
			failures++
			if failures >= 3 {
				select {
				case failed <- errors.New("mihomo health check failed three times"):
				case <-ctx.Done():
				}
				return
			}
		}
		if err := s.options.Waiter.Wait(ctx, s.options.HealthInterval); err != nil {
			return
		}
	}
}

func (s *Supervisor) stopChild(child Child, done <-chan error) error {
	terminateError := child.Terminate()
	timer := time.NewTimer(s.options.StopTimeout)
	defer timer.Stop()
	select {
	case <-done:
		if terminateError != nil {
			return terminateError
		}
		return nil
	case <-timer.C:
		if err := child.Kill(); err != nil {
			return err
		}
		<-done
		return nil
	}
}

func (s *Supervisor) waitBackoff(ctx context.Context, delay time.Duration) error {
	backoffCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.options.Waiter.Wait(backoffCtx, delay) }()
	select {
	case err := <-done:
		return err
	case response := <-s.restart:
		response <- nil
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Supervisor) observe(observation Observation) {
	if s.options.Observe != nil {
		s.options.Observe(observation)
	}
}

type realWaiter struct{}

func (realWaiter) Wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
