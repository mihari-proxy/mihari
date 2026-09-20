package supervisor

import (
	"context"
	"errors"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

// UpdateSession owns trial processes while ordinary restart and monitoring pause.
type UpdateSession struct {
	supervisor  *Supervisor
	process     *ownedChild
	wasRunning  bool
	healthy     bool
	keepStopped bool
}

type updateRequest struct {
	work      func(*UpdateSession) error
	response  chan error
	reinstall bool
}

type ownedChild struct {
	startedAt time.Time
	child     Child
	done      chan error
	joined    chan struct{}
	exitErr   error // read only after joined closes
}

func ownChild(child Child) *ownedChild {
	owned := &ownedChild{child: child, done: make(chan error, 1), joined: make(chan struct{})}
	go func() { defer close(owned.joined); owned.exitErr = child.Wait(); owned.done <- owned.exitErr }()
	return owned
}

// WasRunning reports whether the maintenance session replaced running intent.
func (s *UpdateSession) WasRunning() bool { return s.wasRunning }

// PID reports the owned trial or restored process, or zero when stopped.
func (s *UpdateSession) PID() int {
	if s.process == nil {
		return 0
	}
	return s.process.child.PID()
}

// StartedAt is the actual start of the currently owned process.
func (s *UpdateSession) StartedAt() time.Time {
	if s.process == nil {
		return time.Time{}
	}
	return s.process.startedAt
}

// KeepStopped preserves a stopped or missing original core after recovery.
func (s *UpdateSession) KeepStopped() { s.keepStopped = true }

// WaitForCore starts the event loop without trying to execute a missing binary.
// Runtime assembly invokes it before Run for an initial installation.
func (s *Supervisor) WaitForCore() {
	<-s.startGate
	defer func() { s.startGate <- struct{}{} }()
	// A first install may have completed while Run observed the missing file.
	// Its healthy child is already owned and must be adopted, not paused.
	s.waiting.Store(s.pending == nil)
}

// Start starts one process and waits for its initial health confirmation.
func (s *UpdateSession) Start(ctx context.Context) error {
	if s.process != nil || s.supervisor.blocked.Load() {
		return coreRecoveryRequired()
	}
	child, err := s.supervisor.start(ctx)
	if err != nil {
		return supervisorFailure("mihomo trial start failed", err)
	}
	s.process = ownChild(child)
	s.process.startedAt = s.supervisor.options.Now().UTC()
	if err := s.confirmHealth(ctx); err != nil {
		return supervisorFailure("mihomo trial health check failed", err)
	}
	s.healthy = true
	return nil
}

// Stop joins the current trial process before files may be replaced again.
func (s *UpdateSession) Stop() error {
	if s.process == nil {
		return nil
	}
	var err error
	select {
	case <-s.process.joined:
		err = s.supervisor.waitDescendants(s.process.child)
	default:
		err = s.supervisor.stopChild(s.process.child, s.process.done)
	}
	if err != nil {
		return err
	}
	<-s.process.joined
	s.process = nil
	s.healthy = false
	return nil
}

func (s *UpdateSession) confirmHealth(ctx context.Context) error {
	owner := s.supervisor
	if owner.options.Health == nil {
		select {
		case <-s.process.joined:
			return errors.Join(errors.New("trial process exited"), s.process.exitErr)
		default:
			return nil
		}
	}
	// Child exit interrupts a slow health request; this watcher is always joined.
	healthCtx, cancel := context.WithTimeout(ctx, owner.options.GracePeriod+3*owner.options.HealthInterval+owner.options.StopTimeout)
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-s.process.joined:
			cancel()
		case <-healthCtx.Done():
		}
	}()
	defer func() { cancel(); <-watchDone }()
	if err := owner.options.Waiter.Wait(healthCtx, owner.options.GracePeriod); err != nil {
		return err
	}
	var failure error
	for attempt := 0; attempt < 3; attempt++ {
		if err := healthCtx.Err(); err != nil {
			return errors.Join(failure, err)
		}
		failure = owner.options.Health(healthCtx)
		if failure == nil {
			select {
			case <-s.process.joined:
				return errors.Join(errors.New("trial process exited"), s.process.exitErr)
			default:
				return healthCtx.Err()
			}
		}
		if attempt < 2 {
			if err := owner.options.Waiter.Wait(healthCtx, owner.options.HealthInterval); err != nil {
				return errors.Join(failure, err)
			}
		}
	}
	return failure
}

func (s *Supervisor) start(ctx context.Context) (Child, error) {
	var release func()
	var err error
	if s.options.BeforeStart != nil {
		release, err = s.options.BeforeStart(ctx)
	}
	if release != nil {
		defer release()
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if starter, ok := s.options.Starter.(interface {
		StartContext(context.Context) (Child, error)
	}); ok {
		return starter.StartContext(ctx)
	}
	return s.options.Starter.Start()
}

// Update serializes one complete core update, including trial and recovery.
func (s *Supervisor) Update(ctx context.Context, work func(*UpdateSession) error) error {
	return s.updateOwned(ctx, work, false)
}

// Reinstall admits one explicit candidate installation while ordinary starts are blocked.
func (s *Supervisor) Reinstall(ctx context.Context, work func(*UpdateSession) error) error {
	return s.updateOwned(ctx, work, true)
}

func (s *Supervisor) updateOwned(ctx context.Context, work func(*UpdateSession) error, reinstall bool) error {
	if work == nil {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "update callback missing"}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.startGate:
	}
	if s.blocked.Load() && !reinstall {
		s.startGate <- struct{}{}
		return coreRecoveryRequired()
	}
	if !s.active.Load() {
		defer func() { s.startGate <- struct{}{} }()
		if reinstall {
			s.blocked.Store(false)
		}
		return s.performUpdate(work, false)
	}
	done := s.runDone.Load()
	s.startGate <- struct{}{}
	if done == nil {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "supervisor is starting"}
	}
	request := updateRequest{work: work, response: make(chan error, 1), reinstall: reinstall}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-*done:
		return coreRecoveryRequired()
	case s.update <- request:
	}
	return <-request.response
}

func (s *Supervisor) performUpdate(work func(*UpdateSession) error, wasRunning bool) error {
	session := &UpdateSession{supervisor: s, wasRunning: wasRunning}
	err := work(session)
	if maintenanceDegraded(err) || (session.process != nil && !session.healthy) || (wasRunning && session.process == nil && !session.keepStopped) {
		stopErr := session.Stop()
		s.blocked.Store(true)
		s.observe(Observation{Status: StatusDegraded, LastError: "Core update recovery required"})
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "core update recovery required", Details: map[string]any{"degraded": true}}, errors.Join(err, stopErr))
	}
	if session.keepStopped && session.process != nil {
		if stopErr := session.Stop(); stopErr != nil {
			s.blocked.Store(true)
			return errors.Join(err, stopErr)
		}
	}
	if session.keepStopped {
		s.waiting.Store(true)
	} else if session.process != nil {
		s.waiting.Store(false)
	}
	s.pending = session.process
	select {
	case s.updateWake <- struct{}{}:
	default:
	}
	return err
}
