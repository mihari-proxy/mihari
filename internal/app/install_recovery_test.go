package app

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestInstallRecovery_CrashMatrixConverges(t *testing.T) {
	points := []string{"before-intent", "after-intent", "after-effect", "after-done"}
	base := newInstallHarness(t, InstallDataCreate)
	if _, err := base.tx.Apply(context.Background(), base.req); err != nil {
		t.Fatal(err)
	}
	plan := actionKinds(base.loadedJournal(t, base.disk))
	if len(plan) == 0 {
		t.Fatal("no journaled actions")
	}
	for i, kind := range plan {
		for _, point := range points {
			t.Run(fmt.Sprintf("%d-%s-%s", i+1, kind, point), func(t *testing.T) {
				h := newInstallHarness(t, InstallDataCreate)
				h.tx.crash = &installCrashSpec{index: i + 1, point: point}
				if !catchInstallCrash(func() { _, _ = h.tx.Apply(context.Background(), h.req) }) {
					t.Fatal("process did not crash")
				}
				crash := h.disk.clone()
				lease1 := &fakeInstallLease{global: true, held: true}
				disk1 := crash.clone()
				tx1 := h.reconstruct(disk1, lease1)
				if err := tx1.RecoverLocked(context.Background(), lease1); err != nil {
					t.Fatalf("first recover: %v", err)
				}
				first := disk1.snapshot()
				if err := tx1.RecoverLocked(context.Background(), lease1); err != nil {
					t.Fatalf("second recover: %v", err)
				}
				if disk1.snapshot() != first {
					t.Fatal("recover twice on one process diverged")
				}
				lease2 := &fakeInstallLease{global: true, held: true}
				disk2 := crash.clone()
				tx2 := h.reconstruct(disk2, lease2)
				if err := tx2.RecoverLocked(context.Background(), lease2); err != nil {
					t.Fatalf("independent recover: %v", err)
				}
				if disk2.snapshot() != first {
					t.Fatalf("independent recover diverged\n%s\n%s", first, disk2.snapshot())
				}
				if !disk1.SourcePresent() || !disk2.SourcePresent() {
					t.Fatal("source deleted during recovery")
				}
				if lease1.acquires != 0 || lease2.acquires != 0 {
					t.Fatal("recover acquired a new install lock")
				}
			})
		}
	}
}

func TestInstallRecovery_PreparedMaskRestoredBeforeActivation(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	h.tx.crash = &installCrashSpec{index: 2, point: "after-done"}
	if !catchInstallCrash(func() { _, _ = h.tx.Apply(context.Background(), h.req) }) {
		t.Fatal("expected crash after mask")
	}
	if !h.disk.svc.masked {
		t.Fatal("fixture did not persist mask")
	}
	lease := &fakeInstallLease{global: true, held: true}
	disk := h.disk.clone()
	if err := h.reconstruct(disk, lease).RecoverLocked(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	if disk.svc.masked {
		t.Fatal("prepared mask was not restored")
	}
	if disk.svc.enabled != true || disk.svc.running != true {
		t.Fatalf("old service not restored: %+v", disk.svc)
	}
	if disk.authority != InstallAuthoritySource && disk.authority != "" {
		t.Fatalf("rolled back past activation: %s", disk.authority)
	}
}

func TestInstallRecovery_AfterActivationRepairsTargetOnly(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	h.tx.crash = &installCrashSpec{index: 14, point: "after-intent"}
	if !catchInstallCrash(func() { _, _ = h.tx.Apply(context.Background(), h.req) }) {
		t.Fatal("expected crash after activation")
	}
	journal := h.loadedJournal(t, h.disk)
	if journal.RecoveryAuthority != InstallAuthorityTarget {
		t.Fatalf("activation did not persist target authority: %s", journal.RecoveryAuthority)
	}
	lease := &fakeInstallLease{global: true, held: true}
	disk := h.disk.clone()
	if err := h.reconstruct(disk, lease).RecoverLocked(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(disk.Object(roleManaged), h.art.ManagedNew) {
		t.Fatal("activation recovery restored old managed binary")
	}
	if !bytesEqual(disk.Object(rolePath), h.art.PathNew) {
		t.Fatal("activation recovery restored old path binary")
	}
	if disk.svc.masked {
		t.Fatal("target remained masked")
	}
	if !disk.svc.enabled || !disk.svc.running {
		t.Fatalf("target not repaired: %+v", disk.svc)
	}
	if bytesEqual(disk.Object(roleManaged), h.art.ManagedOld) {
		t.Fatal("rolled back to source after activation")
	}
}

func TestInstallRecovery_RetainCrashDoesNotRollbackData(t *testing.T) {
	h := newInstallHarness(t, InstallDataRetain)
	before := append([]byte(nil), h.disk.Object(roleData)...)
	h.tx.crash = &installCrashSpec{index: 6, point: "after-effect"}
	if !catchInstallCrash(func() { _, _ = h.tx.Apply(context.Background(), h.req) }) {
		t.Fatal("expected crash")
	}
	lease := &fakeInstallLease{global: true, held: true}
	disk := h.disk.clone()
	if err := h.reconstruct(disk, lease).RecoverLocked(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(disk.Object(roleData), before) {
		t.Fatal("retain recovery moved data")
	}
}

func TestInstallRecovery_UnknownObservedStateStops(t *testing.T) {
	action := JournalAction{Kind: JournalActionManagedBinary, OldState: shaOf([]byte("old-managed")), NewState: shaOf([]byte("new-managed")), Status: JournalActionIntent}
	if _, err := ClassifyObservedAction(action, shaOf([]byte("foreign"))); err == nil {
		t.Fatal("unknown identity allowed")
	}
	if apiCode(unknownInstallState()) != protocol.CodeInvalidState {
		t.Fatal("unknown identity is not invalid_state")
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestInstallRecovery_BorrowsDataLeaseBeforeRollbackEffects(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	h.tx.crash = &installCrashSpec{index: 4, point: "after-effect"}
	if !catchInstallCrash(func() { _, _ = h.tx.Apply(context.Background(), h.req) }) {
		t.Fatal("missing crash")
	}
	h.tx.crash = nil
	h.lease.held = true
	called := false
	h.tx.BeforeRollback = func(context.Context) error { called = true; return errors.New("data lease unavailable") }
	err := h.tx.RecoverLocked(context.Background(), h.lease)
	if !called || err == nil {
		t.Fatalf("rollback did not gate effects on borrowed data lock: called=%v err=%v", called, err)
	}
}
