package core

import (
	"errors"
	"fmt"
	"testing"
)

func TestCoreReinstall_RepeatedInterruptionRetainsOriginalIntent(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprint(running), func(t *testing.T) {
			store := newMemoryStore()
			if err := store.Save(t.Context(), InstalledBinary, "", []byte("old stable")); err != nil {
				t.Fatal(err)
			}
			intent := UpdateIntent{Previous: CoreSelection{Channel: "stable"}, Next: CoreSelection{Channel: "alpha"}, WasRunning: running}
			var originalTx string
			for attempt := 1; attempt <= 3; attempt++ {
				tx := fmt.Sprintf("%032x", attempt)
				if attempt == 1 {
					originalTx = tx
				}
				for role, raw := range map[ProvenanceRole][]byte{UpdateMarker: []byte(tx), UpdateCandidate: []byte(fmt.Sprintf("candidate %d", attempt))} {
					if err := store.Save(t.Context(), role, tx, raw); err != nil {
						t.Fatal(err)
					}
				}
				update, err := beginUpdate(t.Context(), store, tx, mustInspect(t, store, UpdateCandidate, tx), intent, attempt > 1)
				if err != nil {
					t.Fatal(err)
				}
				if err := update.Publish(t.Context()); err != nil {
					t.Fatal(err)
				}
				// Recreate the backend as a new daemon would; no transaction replay.
				store = &memoryStore{disk: store.disk}
				if blocked, err := InterruptedUpdate(t.Context(), store); !blocked || err != nil {
					t.Fatalf("blocked=%v err=%v", blocked, err)
				}
				reopened, err := OpenUpdate(t.Context(), store)
				if err != nil {
					t.Fatal(err)
				}
				selection, start := reopened.ReinstallSelection()
				if selection.Channel != "stable" || start != running {
					t.Fatalf("lost original intent: %+v start=%v", selection, start)
				}
				intent = UpdateIntent{Previous: selection, Next: CoreSelection{Channel: "stable"}}
				if backup, err := store.Load(t.Context(), UpdateBackup, originalTx); err != nil || string(backup) != "old stable" {
					t.Fatalf("lost original backup: %q %v", backup, err)
				}
			}
		})
	}
}

func TestCoreReinstall_CompletedRecordWithChangedCoreKeepsRepairAvailable(t *testing.T) {
	store := newMemoryStore()
	for role, raw := range map[ProvenanceRole][]byte{UpdateMarker: []byte(testTransaction), UpdateCandidate: []byte("accepted alpha")} {
		if err := store.Save(t.Context(), role, testTransaction, raw); err != nil {
			t.Fatal(err)
		}
	}
	u, err := BeginUpdate(t.Context(), store, testTransaction, mustInspect(t, store, UpdateCandidate, testTransaction), UpdateIntent{Previous: CoreSelection{Channel: "stable"}, Next: CoreSelection{Channel: "alpha"}, StartNew: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := u.Commit(t.Context(), func(CoreSelection) error { return nil }); err != nil {
		t.Fatal(err)
	}
	// A damaged file after commit cannot execute, but the accepted channel is
	// still known; leaving cleanup pending must not remove the repair entry.
	if err := store.Save(t.Context(), InstalledBinary, "", []byte("damaged")); err != nil {
		t.Fatal(err)
	}
	reopened := &memoryStore{disk: store.disk}
	if blocked, err := InterruptedUpdate(t.Context(), reopened); !blocked || err != nil {
		t.Fatalf("repair unavailable: %v %v", blocked, err)
	}
	if err := authorizePendingUpdate(t.Context(), reopened); err == nil {
		t.Fatal("changed core admitted")
	}
	pending, err := OpenUpdate(t.Context(), reopened)
	if err != nil {
		t.Fatal(err)
	}
	if selection, _ := pending.ReinstallSelection(); selection.Channel != "alpha" {
		t.Fatalf("accepted channel lost: %+v", selection)
	}
}

func TestCoreReinstall_UsesOriginalChannelAndRetainsInterruptedBackup(t *testing.T) {
	ctx := t.Context()
	store := newMemoryStore()
	for role, body := range map[ProvenanceRole]string{InstalledBinary: "old stable", UpdateMarker: testTransaction, UpdateCandidate: "failed alpha"} {
		tx := testTransaction
		if role == InstalledBinary {
			tx = ""
		}
		if err := store.Save(ctx, role, tx, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	old, err := BeginUpdate(ctx, store, testTransaction, mustInspect(t, store, UpdateCandidate, testTransaction), UpdateIntent{Previous: CoreSelection{Channel: "stable", Bundle: "offline stamp"}, Next: CoreSelection{Channel: "alpha"}, WasRunning: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	_ = old.RequireRecovery(ctx, errors.New("simulated interrupted update"))
	tx := "ffffffffffffffffffffffffffffffff"
	if err := store.Save(ctx, UpdateMarker, tx, []byte(tx)); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, UpdateCandidate, tx, []byte("latest stable")); err != nil {
		t.Fatal(err)
	}
	candidate := &Candidate{protected: &protectedCandidate{store: store, transaction: tx, binary: mustInspect(t, store, UpdateCandidate, tx), marker: mustInspect(t, store, UpdateMarker, tx)}}
	intent := UpdateIntent{Previous: old.Intent().Previous, Next: CoreSelection{Channel: "stable", Bundle: "offline stamp", Version: "v1.99.0"}, WasRunning: true}
	wrong := intent
	wrong.Next.Channel = "alpha"
	if _, err := candidate.BeginReinstall(ctx, wrong); err == nil {
		t.Fatal("reinstall silently retried the failed target channel")
	}
	u, err := candidate.BeginReinstall(ctx, intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	if body, err := store.Load(ctx, UpdateBackup, testTransaction); err != nil || string(body) != "old stable" {
		t.Fatalf("original backup lost: %q %v", body, err)
	}
	if err := authorizePendingUpdate(ctx, store); err == nil {
		t.Fatal("reinstall unblocked execution before acceptance")
	}
	if err := u.Rollback(ctx, func(CoreSelection) error { t.Fatal("reinstall tried to adopt uncertain settings"); return nil }); err == nil {
		t.Fatal("reinstall allowed execution recovery of the uncertain core")
	}
	if err := u.Commit(ctx, func(s CoreSelection) error {
		if s.Channel != "stable" || s.Bundle != "offline stamp" {
			t.Fatalf("selection=%+v", s)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := u.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	if err := authorizePendingUpdate(ctx, store); err != nil {
		t.Fatal(err)
	}
	if body, err := store.Load(ctx, UpdateBackup, testTransaction); err != nil || string(body) != "old stable" {
		t.Fatalf("original backup removed by unrelated cleanup: %q %v", body, err)
	}
}
