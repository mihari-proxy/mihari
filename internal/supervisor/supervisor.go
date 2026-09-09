package supervisor

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
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
	Starter            Starter
	Health             HealthChecker
	Waiter             Waiter
	Now                func() time.Time
	Observe            func(Observation)
	MinimumBackoff     time.Duration
	MaximumBackoff     time.Duration
	StableAfter        time.Duration
	GracePeriod        time.Duration
	HealthInterval     time.Duration
	StopTimeout        time.Duration
	DiagnosticReporter diagnostics.Reporter
}

type maintenanceRequest struct {
	work     func() error
	response chan error
}
type Supervisor struct {
	maintain  chan maintenanceRequest
	startGate chan struct{}
	runDone   atomic.Pointer[chan struct{}]
	blocked   atomic.Bool

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
	s := &Supervisor{options: options, restart: make(chan chan error), maintain: make(chan maintenanceRequest), startGate: make(chan struct{}, 1)}
	s.startGate <- struct{}{}
	return s
}

func (s *Supervisor) Run(ctx context.Context) error {
	if s.options.Starter == nil {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo process starter is unavailable"}
	}
	if !s.active.CompareAndSwap(false, true) {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo supervisor is already running"}
	}
	runDone := make(chan struct{})
	s.runDone.Store(&runDone)
	defer func() { s.active.Store(false); close(runDone) }()
	ctx = logging.WithOperation(ctx, logging.OperationMetadata{})

	backoff := NewBackoff(s.options.MinimumBackoff, s.options.MaximumBackoff)
	var restarts uint64
	for {
		if ctx.Err() != nil {
			s.observe(Observation{Status: StatusStopped, Restarts: restarts})
			return nil
		}
		startedAt := s.options.Now()
		if s.blocked.Load() {
			select {
			case <-ctx.Done():
				return nil
			case response := <-s.restart:
				response <- coreRecoveryRequired()
				continue
			case request := <-s.maintain:
				request.response <- coreRecoveryRequired()
				continue
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.startGate:
		}
		// Idle maintenance may have degraded while Run waited for this gate.
		if ctx.Err() != nil || s.blocked.Load() {
			s.startGate <- struct{}{}
			continue
		}
		child, err := s.options.Starter.Start()
		s.startGate <- struct{}{}
		if err != nil {
			err = supervisorFailure("mihomo process start failed", err)
			s.report(ctx, "core.start.failed", slog.LevelError, err)
		}
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
		if s.blocked.Load() {
			continue
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
	if s.blocked.Load() {
		return coreRecoveryRequired()
	}
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
	joined := make(chan struct{})
	go func() { defer close(joined); done <- child.Wait() }()
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
		if err != nil {
			s.report(parent, "core.termination.failed", slog.LevelError, supervisorFailure("mihomo process termination failed", err))
		}
		return err, false
	case request := <-s.maintain:
		err := s.stopChild(child, done)
		stopFailed := err != nil
		finishMonitor()
		if err != nil {
			s.blocked.Store(true)
		} else {
			err = request.work()
			if maintenanceDegraded(err) {
				s.blocked.Store(true)
			}
		}
		request.response <- err
		// A failed OS stop does not detach the still-owned Wait goroutine.
		// No new process or transaction runs before that child actually exits.
		if stopFailed {
			<-joined
		}
		return err, true
	case response := <-s.restart:
		err := s.stopChild(child, done)
		finishMonitor()
		response <- err
		return err, true
	case err := <-done:
		finishMonitor()
		err = supervisorFailure("mihomo process exited unexpectedly", errors.Join(err, s.waitDescendants(child)))
		s.report(parent, "core.exit.unexpected", slog.LevelError, err)
		return err, false
	case err := <-healthFailure:
		stopError := s.stopChild(child, done)
		finishMonitor()
		if level, emit := diagnostics.FailureLevel(parent, err); emit {
			s.report(parent, "core.health.failed", level, err)
		}
		if stopError != nil {
			terminationError := supervisorFailure("mihomo process termination failed", stopError)
			s.report(parent, "core.termination.failed", slog.LevelError, terminationError)
			return supervisorFailure("mihomo health check failed three times", errors.Join(err, terminationError)), false
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
				failure := supervisorFailure("mihomo health check failed three times", err)
				select {
				case failed <- failure:
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

func (s *Supervisor) stopChild(child Child, done <-chan error) (resultErr error) {
	defer func() {
		if resultErr != nil {
			s.blocked.Store(true)
		}
	}()
	terminateError := child.Terminate()
	timer := time.NewTimer(s.options.StopTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return errors.Join(terminateError, s.waitDescendants(child))
	case <-timer.C:
		if err := child.Kill(); err != nil {
			return err
		}
		<-done
		return s.waitDescendants(child)
	}
}

func coreRecoveryRequired() error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: "core recovery required"}
}

func (s *Supervisor) waitDescendants(child Child) error {
	waiter, ok := child.(interface{ WaitDescendants(context.Context) error })
	if !ok {
		return nil
	}
	// Cleanup remains owned even when the daemon's parent context was canceled.
	ctx, cancel := context.WithTimeout(context.Background(), s.options.StopTimeout)
	defer cancel()
	if err := waiter.WaitDescendants(ctx); err != nil {
		s.blocked.Store(true)
		s.observe(Observation{Status: StatusDegraded, LastError: "managed core descendants did not exit"})
		return errors.Join(coreRecoveryRequired(), err)
	}
	return nil
}

func (s *Supervisor) waitBackoff(ctx context.Context, delay time.Duration) error {
	backoffCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.options.Waiter.Wait(backoffCtx, delay) }()
	select {
	case err := <-done:
		return err
	case request := <-s.maintain:
		err := request.work()
		if maintenanceDegraded(err) {
			s.blocked.Store(true)
		}
		request.response <- err
		return nil
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

func (s *Supervisor) report(ctx context.Context, event string, level slog.Level, err error) {
	if s.options.DiagnosticReporter == nil {
		return
	}
	s.options.DiagnosticReporter(ctx, diagnostics.Record{
		Component: "supervisor",
		Event:     event,
		Level:     level,
		Err:       err,
	})
}

func supervisorFailure(message string, cause error) error {
	return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: message}, cause)
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

// Maintain runs work only while the owned child and monitor are stopped. Work
// must not call supervisor methods. Once accepted, cancellation does not detach
// work: the caller waits until commit/recovery has converged. Idle maintenance
// preserves the stopped state and never starts a process.
func (s *Supervisor) Maintain(ctx context.Context, work func() error) error {
	if work == nil {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "maintenance callback missing"}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.startGate:
	}
	if s.blocked.Load() {
		s.startGate <- struct{}{}
		return coreRecoveryRequired()
	}
	if !s.active.Load() {
		defer func() { s.startGate <- struct{}{} }()
		if e := ctx.Err(); e != nil {
			return e
		}
		err := work()
		if maintenanceDegraded(err) {
			s.blocked.Store(true)
		}
		return err
	}
	done := s.runDone.Load()
	s.startGate <- struct{}{}
	if done == nil {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "supervisor is starting"}
	}
	request := maintenanceRequest{work: work, response: make(chan error, 1)}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-*done:
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "supervisor stopped"}
	case s.maintain <- request:
	}
	// The event loop owns the callback now; never return before it converges.
	return <-request.response
}
func maintenanceDegraded(err error) bool {
	var api protocol.APIError
	return errors.As(err, &api) && api.Details["degraded"] == true
}
