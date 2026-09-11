package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"sync/atomic"
	"testing"
)

// processExitChild uses the same session as exec.Cmd and the production child.
// The injected wait result models the operating system's final process exit.
type processExitChild struct {
	exitErr       error
	ready, exited bool
	closeErr      error
	lease         *observedValidationClose
}

func (p *processExitChild) Start(ctx context.Context, r ValidationStart) (ValidationSession, error) {
	id := ProcessStartIdentity{PID: 1, BootID: r.Journal.BootID, StartUnix: 1}
	p.lease = &observedValidationClose{ValidationLease: r.Child, err: p.closeErr}
	s := newProcessValidationSession(r.Lease, func() error {
		defer func() { p.exited = true }()
		err := RunValidationDaemon(ctx, ValidationDaemonOptions{Lease: p.lease, Store: r.Store, TransactionID: r.Journal.TransactionID, ParentIdentity: r.Handshake.ParentIdentity, SelfIdentity: id, BinaryHash: r.Journal.CandidateHash, LayoutIdentity: layoutIdentityOf(r.Journal), Run: func(ctx context.Context, _ InstallJournal, ready func(bool) error) error {
			if err := ready(false); err != nil {
				return err
			}
			p.ready = true
			<-ctx.Done()
			return nil
		}})
		return errors.Join(err, p.exitErr)
	}, func(context.Context, ProcessStartIdentity) error { return errors.New("unexpected forced stop") })
	s.identity = id
	return s, nil
}

func TestInstallValidation_ProcessExitControlsActivation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		exitErr error
	}{
		{name: "requested-clean-stop"},
		{name: "failed-exit-after-ready", exitErr: errors.New("child cleanup failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newInstallHarness(t, InstallDataCreate)
			child := &processExitChild{exitErr: tc.exitErr}
			h.tx.Validation = child
			_, err := h.tx.Apply(context.Background(), h.req)
			if !child.ready || !child.exited {
				t.Fatalf("did not join ready child: ready=%v exited=%v err=%v", child.ready, child.exited, err)
			}
			j := h.loadedJournal(t, h.disk)
			if tc.exitErr != nil {
				if !errors.Is(err, tc.exitErr) || j.RecoveryAuthority == InstallAuthorityTarget {
					t.Fatalf("failed child activated: err=%v authority=%s", err, j.RecoveryAuthority)
				}
			} else if err != nil || j.RecoveryAuthority != InstallAuthorityTarget {
				t.Fatalf("clean requested stop failed: err=%v authority=%s", err, j.RecoveryAuthority)
			}
		})
	}
}

type observedValidationClose struct {
	ValidationLease
	err   error
	calls atomic.Int32
}

func (p *observedValidationClose) Close() error {
	if p.calls.Add(1) != 1 {
		return errors.New("duplicate lease close")
	}
	return errors.Join(p.ValidationLease.Close(), p.err)
}

func TestInstallValidation_ChildLeaseCloseControlsActivation(t *testing.T) {
	for _, closeErr := range []error{nil, errors.New("child pipe close failed")} {
		name := "clean"
		if closeErr != nil {
			name = "close-failure"
		}
		t.Run(name, func(t *testing.T) {
			h := newInstallHarness(t, InstallDataCreate)
			child := &processExitChild{closeErr: closeErr}
			h.tx.Validation = child
			_, err := h.tx.Apply(context.Background(), h.req)
			j := h.loadedJournal(t, h.disk)
			if !child.ready || !child.exited {
				t.Fatalf("close case did not reach ready and join: ready=%v exited=%v err=%v", child.ready, child.exited, err)
			}
			if closeErr != nil {
				if !errors.Is(err, closeErr) || j.RecoveryAuthority == InstallAuthorityTarget {
					t.Fatalf("close failure activated: err=%v authority=%s", err, j.RecoveryAuthority)
				}
			} else if err != nil || j.RecoveryAuthority != InstallAuthorityTarget {
				t.Fatalf("clean stop failed: err=%v authority=%s", err, j.RecoveryAuthority)
			}
			if child.lease.calls.Load() != 1 {
				t.Fatalf("owned child lease closed %d times", child.lease.calls.Load())
			}
		})
	}
}

func TestInstallValidation_RejectedChildReturnsCloseFailure(t *testing.T) {
	p, c := newMemoryValidationPipes()
	defer func() { _ = p.Close() }() // Memory pipe cleanup cannot fail.
	closeErr := errors.New("close failed on rejection")
	lease := &observedValidationClose{ValidationLease: c, err: closeErr}
	err := RunValidationDaemon(context.Background(), ValidationDaemonOptions{Lease: lease, EUID: 1000})
	if apiCode(err) != protocol.CodePermissionDenied || !errors.Is(err, closeErr) || lease.calls.Load() != 1 {
		t.Fatalf("rejected child lost owned close: err=%v calls=%d", err, lease.calls.Load())
	}
}
