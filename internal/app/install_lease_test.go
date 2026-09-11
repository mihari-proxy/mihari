package app

import (
	"context"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestInstallLease_BorrowedForRecoverAndStart(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	ctx := context.Background()
	lease, err := h.lease.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.tx.RecoverLocked(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if _, err := h.tx.ApplyLocked(ctx, lease, h.req); err != nil {
		t.Fatal(err)
	}
	if err := h.tx.StartLocked(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if h.lease.acquires != 1 {
		t.Fatalf("inner methods reacquired lock: %d", h.lease.acquires)
	}
	if h.lease.closes != 0 {
		t.Fatal("borrowed lease released itself")
	}
	if h.lease.validates == 0 {
		t.Fatal("borrowed lease was not validated")
	}
}

func TestInstallLease_DaemonDoesNotTakeLockOrBusyLoop(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	h.tx.crash = &installCrashSpec{index: 3, point: "after-done"}
	if !catchInstallCrash(func() { _, _ = h.tx.Apply(context.Background(), h.req) }) {
		t.Fatal("expected pending journal")
	}
	journal := h.loadedJournal(t, h.disk)
	if !InstallPending(journal) {
		t.Fatalf("pending not detected for phase %s", journal.Phase)
	}
	lease := &fakeInstallLease{global: true}
	start := time.Now()
	err := CheckDaemonInstallJournal(journal)
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("daemon busy-looped on pending journal")
	}
	if apiCode(err) != protocol.CodeInvalidState {
		t.Fatalf("daemon want invalid_state, got %v", err)
	}
	if lease.acquires != 0 {
		t.Fatal("ordinary daemon took the install lock")
	}
	if InstallPending(InstallJournal{Phase: InstallPhaseActivationCommitted}) || InstallPending(InstallJournal{Phase: InstallPhaseComplete}) {
		t.Fatal("activation/complete treated as pending")
	}
}

func TestInstallLease_PrivateServiceTakesGlobalThenPrivate(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	h.lease.private = true
	h.tx.Private = true
	if _, err := h.tx.Apply(context.Background(), h.req); err != nil {
		t.Fatal(err)
	}
	if len(h.lease.order) < 2 || h.lease.order[0] != "B" || h.lease.order[1] != "P" {
		t.Fatalf("private service lock order %v", h.lease.order)
	}
}

func TestInstallLease_HeldLockReportsBusyWithoutLooping(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	if _, err := h.lease.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := h.tx.Apply(context.Background(), h.req)
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("apply looped waiting for install lock")
	}
	if apiCode(err) != protocol.CodeInvalidState {
		t.Fatalf("want busy invalid_state, got %v", err)
	}
	if h.lease.loops != 1 {
		t.Fatalf("busy reported %d loops", h.lease.loops)
	}
}
