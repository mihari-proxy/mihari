package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mihari-proxy/mihari/internal/app"

	"sync"
	"testing"
)

type activationLease struct {
	held   bool
	closes int
}

func (l *activationLease) Validate(context.Context) error {
	if !l.held {
		return errors.New("lease closed")
	}
	return nil
}
func (l *activationLease) Close() error { l.held = false; l.closes++; return nil }

type activationChild struct {
	parentDeath bool
	failInit    bool
	session     *activationSession
}

func (c *activationChild) Start(ctx context.Context, r app.ValidationStart) (app.ValidationSession, error) {
	ctx, cancel := context.WithCancel(ctx)
	s := &activationSession{request: r, done: make(chan struct{}), cancel: cancel, child: c}
	c.session = s
	go func() {
		defer close(s.done)
		s.err = app.RunValidationDaemon(ctx, app.ValidationDaemonOptions{Lease: r.Child, Store: r.Store, TransactionID: r.Journal.TransactionID, ParentIdentity: r.Handshake.ParentIdentity, SelfIdentity: s.Identity(), BinaryHash: r.Journal.CandidateHash, LayoutIdentity: r.Handshake.LayoutIdentity, Run: func(ctx context.Context, _ app.InstallJournal, ready func(bool) error) error {
			s.initialized = true
			defer func() { s.joined = true }()
			if c.failInit {
				return errors.New("injected initialization failure")
			}
			if err := ready(true); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		}})
	}()
	return s, nil
}
func (c *activationChild) ReapValidation(context.Context, app.ProcessStartIdentity, app.InstallJournal) error {
	return nil
}

type activationSession struct {
	request             app.ValidationStart
	child               *activationChild
	done                chan struct{}
	cancel              context.CancelFunc
	once                sync.Once
	err                 error
	initialized, joined bool
	closes, waits       int
}

func (s *activationSession) Identity() app.ProcessStartIdentity {
	return app.ProcessStartIdentity{PID: 9, BootID: s.request.Journal.BootID, StartUnix: 200}
}
func (s *activationSession) WaitReady(ctx context.Context) error {
	var msg struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(s.request.Lease).Decode(&msg); err != nil {
		return err
	}
	if !msg.OK {
		return errors.New("missing ready")
	}
	if s.child.parentDeath {
		_ = s.request.Lease.Close()
		<-s.done
		return errors.New("parent connection lost")
	}
	return nil
}
func (s *activationSession) Stop(context.Context) error {
	select {
	case <-s.done:
		return nil
	default:
	}
	return json.NewEncoder(s.request.Lease).Encode(map[string]bool{"stop": true})
}
func (s *activationSession) Close() error {
	s.closes++
	s.once.Do(func() { s.cancel(); _ = s.request.Lease.Close(); _ = s.request.Child.Close() })
	return nil
}
func (s *activationSession) WaitLockRelease(context.Context) error { s.waits++; <-s.done; return nil }

func activationFixture(t *testing.T) (*app.InstallTransaction, *activationLease, *activationChild) {
	t.Helper()
	lease := &activationLease{}
	child := &activationChild{}
	x := &app.InstallTransaction{Store: app.NewMemoryInstallJournalForTest(), Validation: child, ParentIdentity: app.ProcessStartIdentity{PID: 7, BootID: "11111111-1111-1111-1111-111111111111", StartUnix: 100}, Artifacts: app.InstallArtifacts{CandidateHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", BootID: "11111111-1111-1111-1111-111111111111"}}
	x.Acquire = func(context.Context) (app.InstallLease, error) {
		if lease.held {
			return nil, errors.New("lock busy")
		}
		lease.held = true
		return lease, nil
	}
	return x, lease, child
}

func TestUnixBootstrap_TargetAuthorityAfterStartFailure(t *testing.T) {
	x, lease, child := activationFixture(t)
	failure := errors.New("actual target start failure")
	created := false
	ran := false
	err := (app.ForegroundBootstrap{Root: true, Transaction: x, DiscoverSource: func(context.Context) (bool, error) {
		if created {
			t.Fatal("source checked after create")
		}
		return false, nil
	}, CreateData: func(context.Context) error { created = true; return nil }, Run: func(_ context.Context, phase string) error {
		ran = true
		if lease.held {
			t.Fatal("install lock retained into normal daemon")
		}
		if phase != app.InstallPhaseActivationCommitted {
			t.Fatal("normal daemon before activation")
		}
		return failure
	}}).Start(context.Background())
	if !errors.Is(err, failure) || !ran || !created {
		t.Fatalf("failed before target startup: %v", err)
	}
	j, err := x.Store.Load(context.Background())
	if err != nil || j.RecoveryAuthority != app.InstallAuthorityTarget {
		t.Fatalf("target authority lost: %v %+v", err, j)
	}
	if !child.session.joined || child.session.closes != 1 || child.session.waits != 1 || lease.closes != 1 {
		t.Fatalf("child/lock ownership not joined: %+v lease=%+v", child.session, lease)
	}
}

func TestUnixBootstrap_ParentDeathCancelsChild(t *testing.T) {
	x, lease, child := activationFixture(t)
	child.parentDeath = true
	_, err := x.Apply(context.Background(), app.InstallRequest{Operation: app.InstallOperationInstall, Layout: app.InstallLayoutSystem})
	if err == nil {
		t.Fatal("parent EOF authorized activation")
	}
	if child.session == nil || !child.session.initialized || !child.session.joined || child.session.waits != 1 || lease.held {
		t.Fatal("EOF after readiness did not join child before releasing install lock")
	}
	j, err := x.Store.Load(context.Background())
	if err != nil || j.RecoveryAuthority == app.InstallAuthorityTarget {
		t.Fatalf("EOF authorized target: %v %+v", err, j)
	}
}

func TestUnixBootstrap_CloseOnce(t *testing.T) {
	x, lease, child := activationFixture(t)
	child.failInit = true
	_, err := x.Apply(context.Background(), app.InstallRequest{Operation: app.InstallOperationInstall, Layout: app.InstallLayoutSystem})
	if err == nil {
		t.Fatal("initialization failure ignored")
	}
	s := child.session
	if s == nil || !s.initialized || !s.joined || s.closes != 1 || s.waits != 1 || lease.closes != 1 {
		t.Fatalf("production failure cleanup: child=%+v lease=%+v", s, lease)
	}
}
