package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// FakeDataLease is the in-process data/endpoint lock used by tests.
type FakeDataLease struct {
	mu   sync.Mutex
	held bool
}

func (l *FakeDataLease) Held() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held
}

func (l *FakeDataLease) Release(context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.held = false
	return nil
}

func (l *FakeDataLease) Acquire() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.held = true
	l.mu.Unlock()
}

func (l *FakeDataLease) WaitReleased(ctx context.Context) error {
	if l == nil {
		return nil
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if !l.Held() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// FakeValidationChild is the injected Windows/host test child. It never forks.
type FakeValidationChild struct {
	Result           string
	SetupRequired    bool
	MissingPipe      bool
	ForgeParent      bool
	ClosePipe        bool
	WriteStatusReady bool
	SkipReady        bool
	Recover          bool
	TakeInstallLock  bool
	WrongReadyChild  bool
}

type fakeValidationSession struct {
	child  *FakeValidationChild
	req    ValidationStart
	done   chan struct{}
	cancel context.CancelFunc
	err    error
	once   sync.Once
}

func (s *fakeValidationSession) Identity() ProcessStartIdentity {
	return ProcessStartIdentity{PID: 1, BootID: s.req.Journal.BootID, StartUnix: 1}
}
func (c *FakeValidationChild) ReapValidation(context.Context, ProcessStartIdentity, InstallJournal) error {
	return nil
}
func (c *FakeValidationChild) Start(ctx context.Context, req ValidationStart) (ValidationSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &fakeValidationSession{child: c, req: req, done: make(chan struct{}), cancel: cancel}
	go func() { defer close(s.done); s.err = s.run(ctx) }()
	return s, nil
}
func (s *fakeValidationSession) run(ctx context.Context) (resultErr error) {
	transferred := false
	defer func() {
		if !transferred {
			resultErr = errors.Join(resultErr, s.req.Child.Close())
		}
	}()
	if s.child.MissingPipe || s.child.ClosePipe {
		return errMissingValidationPipe
	}
	if s.child.TakeInstallLock || s.child.Recover {
		return errValidationHandshake
	}
	parent := s.req.Handshake.ParentIdentity
	if s.child.ForgeParent {
		parent.PID++
	}
	transferred = true
	return RunValidationDaemon(ctx, ValidationDaemonOptions{Lease: s.req.Child, Store: s.req.Store, TransactionID: s.req.Journal.TransactionID, EUID: s.req.Handshake.EUID, ParentIdentity: parent, SelfIdentity: s.Identity(), BinaryHash: s.req.Journal.CandidateHash, LayoutIdentity: layoutIdentityOf(s.req.Journal), Run: func(ctx context.Context, _ InstallJournal, ready func(bool) error) error {
		if lease, ok := s.req.DataLease.(*FakeDataLease); ok {
			lease.Acquire()
			defer func() { _ = lease.Release(context.Background()) }() // In-memory lock release cannot fail.
		}
		if s.child.SkipReady {
			return errValidationReady
		}
		if s.child.Result == ValidationFailed {
			return errValidationFailed
		}
		if s.child.WriteStatusReady || s.child.WrongReadyChild {
			var raw []byte
			if s.child.WriteStatusReady {
				raw = []byte(`{"schema":"mihari/v1","health":"ok"}`)
			} else {
				id := s.Identity()
				id.StartUnix++
				raw, _ = json.Marshal(ValidationReady{TransactionID: s.req.Journal.TransactionID, DaemonIdentity: id, BinaryHash: s.req.Journal.CandidateHash, LayoutIdentity: layoutIdentityOf(s.req.Journal), Validation: ValidationOK})
			}
			if _, err := s.req.Store.files.write(ctx, validationReadyPath(s.req.Journal.TransactionID), raw, JournalObject{}); err != nil {
				return err
			}
			if err := writeValidationJSON(s.req.Child, validationPipeMessage{OK: true}); err != nil {
				return err
			}
		} else if err := ready(s.child.SetupRequired); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}})
}
func (s *fakeValidationSession) WaitReady(ctx context.Context) error {
	result := make(chan error, 1)
	go func() {
		msg, err := readValidationJSON(s.req.Lease)
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
		_ = s.req.Lease.Close()
		<-result
		return s.err
	}
}
func (s *fakeValidationSession) Stop(context.Context) error {
	select {
	case <-s.done:
		return nil
	default:
	}
	return writeValidationJSON(s.req.Lease, validationPipeMessage{Stop: true})
}
func (s *fakeValidationSession) WaitLockRelease(ctx context.Context) error {
	<-s.done
	if s.req.DataLease != nil {
		return s.req.DataLease.WaitReleased(ctx)
	}
	return nil
}
func (s *fakeValidationSession) Close() error {
	s.once.Do(func() { s.cancel(); _ = s.req.Lease.Close() })
	return nil
}
