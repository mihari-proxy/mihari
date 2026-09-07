package app

import (
	"context"
	"errors"
	"sync"
	"time"
)

// newProcessValidationSession owns the pipe and joins the launched process.
// wait is called exactly once; forceStop must verify the recorded identity.
func newProcessValidationSession(lease ValidationLease, wait func() error, forceStop func(context.Context, ProcessStartIdentity) error) *processValidationSession {
	s := &processValidationSession{lease: lease, done: make(chan struct{}), forceStop: forceStop}
	go func() { s.waitErr = wait(); close(s.done) }()
	return s
}

type processValidationSession struct {
	forceStop func(context.Context, ProcessStartIdentity) error
	lease     ValidationLease
	identity  ProcessStartIdentity
	done      chan struct{}
	waitErr   error
	closeOnce sync.Once
	closeErr  error
}

func (s *processValidationSession) Identity() ProcessStartIdentity { return s.identity }
func (s *processValidationSession) WaitReady(ctx context.Context) error {
	result := make(chan error, 1)
	go func() {
		msg, err := readValidationJSON(s.lease)
		if err == nil && !msg.OK {
			err = errValidationReady
		}
		result <- err
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		_ = s.Close()
		<-result
		return ctx.Err()
	case <-s.done:
		_ = s.Close()
		<-result
		return errValidationFailed
	}
}
func (s *processValidationSession) Stop(ctx context.Context) error {
	result := make(chan error, 1)
	go func() { result <- writeValidationJSON(s.lease, validationPipeMessage{Stop: true}) }()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		_ = s.Close()
		<-result
		return ctx.Err()
	case <-timer.C:
		_ = s.Close()
		<-result
		return installBusy("validation stop timed out")
	}
}
func (s *processValidationSession) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.lease.Close() })
	return s.closeErr
}
func (s *processValidationSession) WaitLockRelease(ctx context.Context) error {
	// EOF makes the authenticated child cancel and close all resources. If it
	// cannot exit, retain ownership while reaping this exact started process.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-s.done:
		return s.waitErr
	case <-timer.C:
	}
	signalErr := s.forceStop(context.WithoutCancel(ctx), s.identity)
	<-s.done // kernel wait, never abandon a child still owning data locks
	return errors.Join(s.waitErr, signalErr, installBusy("validation child required forced shutdown"))
}
