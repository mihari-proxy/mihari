package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestCoreUpdateKeepsBackupThroughPublicationAndRestoresSelection(t *testing.T) {
	store := newMemoryStore()
	for _, item := range []struct {
		role        ProvenanceRole
		tx, content string
	}{{InstalledBinary, "", "local-self-built"}, {UpdateCandidate, testTransaction, "new-official-core"}, {UpdateMarker, testTransaction, testTransaction}} {
		if err := store.Save(t.Context(), item.role, item.tx, []byte(item.content)); err != nil {
			t.Fatal(err)
		}
	}
	intent := UpdateIntent{Previous: CoreSelection{Channel: "stable", Bundle: "bundle-stamp", Version: "v1.19.30"}, Next: CoreSelection{Channel: "alpha", Bundle: "bundle-stamp", Version: "alpha-abcdef1", AlphaSHA: "abcdef1"}, WasRunning: true}
	update, err := BeginUpdate(t.Context(), store, testTransaction, mustInspect(t, store, UpdateCandidate, testTransaction), intent)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Load(t.Context(), InstalledBinary, ""); string(got) != "local-self-built" {
		t.Fatal("begin published candidate")
	}
	if err := update.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Load(t.Context(), InstalledBinary, ""); string(got) != "new-official-core" {
		t.Fatal("new binary not published")
	}
	if got, _ := store.Load(t.Context(), UpdateBackup, testTransaction); string(got) != "local-self-built" {
		t.Fatal("old bytes retired before healthy commit")
	}
	var restored CoreSelection
	if err := update.Rollback(t.Context(), func(selection CoreSelection) error { restored = selection; return nil }); err != nil {
		t.Fatal(err)
	}
	if restored != intent.Previous {
		t.Fatalf("restored selection=%+v", restored)
	}
	if got, _ := store.Load(t.Context(), InstalledBinary, ""); string(got) != "local-self-built" {
		t.Fatal("rollback did not restore old bytes")
	}
}

func TestCoreUpdateInterruptedBoundariesRetainEvidenceWithoutReplay(t *testing.T) {
	seed := func() *memoryStore {
		store := newMemoryStore()
		for _, item := range []struct {
			role        ProvenanceRole
			tx, content string
		}{{InstalledBinary, "", "old"}, {UpdateCandidate, testTransaction, "new"}, {UpdateMarker, testTransaction, testTransaction}} {
			if err := store.Save(t.Context(), item.role, item.tx, []byte(item.content)); err != nil {
				t.Fatal(err)
			}
		}
		store.step = 0
		return store
	}
	intent := UpdateIntent{Previous: CoreSelection{Channel: "stable"}, Next: CoreSelection{Channel: "alpha"}}
	run := func(store *memoryStore) error {
		update, err := BeginUpdate(t.Context(), store, testTransaction, mustInspect(t, store, UpdateCandidate, testTransaction), intent)
		if err != nil {
			return err
		}
		if err := update.Publish(t.Context()); err != nil {
			return err
		}
		return update.Commit(t.Context(), func(CoreSelection) error { return nil })
	}
	baseline := seed()
	if err := run(baseline); err != nil {
		t.Fatal(err)
	}
	for fail := 1; fail <= baseline.step; fail++ {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			store := seed()
			store.fail = fail
			_ = run(store)
			reopened := &memoryStore{disk: store.disk}
			update, err := OpenUpdate(t.Context(), reopened)
			want := "old"
			if errors.Is(err, os.ErrNotExist) {
				// No publish can happen before a durable journal exists.
			} else if err != nil {
				t.Fatal(err)
			} else if update.journal.Phase == "committed" {
				want = "new"
				if err := update.Rollback(t.Context(), func(CoreSelection) error { return nil }); err == nil {
					t.Fatal("committed update allowed reverse rollback")
				}
			} else {
				before, err := reopened.Load(t.Context(), InstalledBinary, "")
				if err != nil {
					t.Fatal(err)
				}
				want = string(before)
				if blocked, err := InterruptedUpdate(t.Context(), reopened); err != nil || !blocked {
					t.Fatalf("unfinished update was not blocked: %v %v", blocked, err)
				}
				if update.Intent().Previous != intent.Previous {
					t.Fatal("original channel lost")
				}
				if backup, err := reopened.Load(t.Context(), UpdateBackup, testTransaction); err != nil || string(backup) != "old" {
					t.Fatalf("backup lost: %q %v", backup, err)
				}
			}
			if got, err := reopened.Load(t.Context(), InstalledBinary, ""); err != nil || string(got) != want {
				t.Fatalf("core=%q want=%q err=%v", got, want, err)
			}
		})
	}
}

func TestCoreUpdateSettingsFailureCanStillRestoreOldBytes(t *testing.T) {
	store := newMemoryStore()
	for _, item := range []struct {
		role        ProvenanceRole
		tx, content string
	}{{InstalledBinary, "", "old"}, {UpdateCandidate, testTransaction, "new"}, {UpdateMarker, testTransaction, testTransaction}} {
		if err := store.Save(t.Context(), item.role, item.tx, []byte(item.content)); err != nil {
			t.Fatal(err)
		}
	}
	update, err := BeginUpdate(t.Context(), store, testTransaction, mustInspect(t, store, UpdateCandidate, testTransaction), UpdateIntent{Previous: CoreSelection{Channel: "stable"}, Next: CoreSelection{Channel: "alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := update.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("settings not saved")
	if err := update.Commit(t.Context(), func(CoreSelection) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("commit lost settings error: %v", err)
	}
	if err := update.Rollback(t.Context(), func(CoreSelection) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Load(t.Context(), InstalledBinary, ""); string(got) != "old" {
		t.Fatal("failed settings commit lost old core")
	}
}

func TestCoreUpdateAllowsOnlyTransactionBoundTrialExecution(t *testing.T) {
	store := newMemoryStore()
	for _, item := range []struct {
		role        ProvenanceRole
		tx, content string
	}{
		{InstalledBinary, "", "old"}, {UpdateCandidate, testTransaction, "new"}, {UpdateMarker, testTransaction, testTransaction},
	} {
		if err := store.Save(t.Context(), item.role, item.tx, []byte(item.content)); err != nil {
			t.Fatal(err)
		}
	}
	update, err := BeginUpdate(t.Context(), store, testTransaction, mustInspect(t, store, UpdateCandidate, testTransaction), UpdateIntent{Previous: CoreSelection{Channel: "stable"}, Next: CoreSelection{Channel: "alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := update.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if v, err := OpenInstalledCore(t.Context(), store); err == nil || v != nil {
		t.Fatal("ordinary execution bypassed pending update")
	}
	trialCtx := update.ExecutionContext(t.Context())
	verified, err := OpenInstalledCore(trialCtx, store)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := verified.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := verified.Command(trialCtx, CoreVersion, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := verified.Command(t.Context(), CoreVersion, nil); err == nil {
		t.Fatal("trial capability executed without transaction context")
	}
	if err := update.Rollback(t.Context(), func(CoreSelection) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := verified.Command(trialCtx, CoreVersion, nil); err == nil {
		t.Fatal("candidate executed after rollback")
	}
}

type uncertainCommitStore struct {
	*memoryStore
	denyJournal bool
}

func (s *uncertainCommitStore) Save(ctx context.Context, role ProvenanceRole, tx string, content []byte) error {
	if err := s.memoryStore.Save(ctx, role, tx, content); err != nil {
		return err
	}
	if role == UpdateJournal {
		var journal updateJournal
		if err := json.Unmarshal(content, &journal); err != nil {
			return err
		}
		if journal.Phase == "committed" {
			s.denyJournal = true
			return errors.New("sync failed after committed replacement")
		}
	}
	return nil
}

func (s *uncertainCommitStore) Load(ctx context.Context, role ProvenanceRole, tx string) ([]byte, error) {
	if role == UpdateJournal && s.denyJournal {
		return nil, os.ErrPermission
	}
	return s.memoryStore.Load(ctx, role, tx)
}

func TestCoreUpdateUnknownCommitOutcomeMustNotRollback(t *testing.T) {
	store := &uncertainCommitStore{memoryStore: newMemoryStore()}
	for _, item := range []struct {
		role        ProvenanceRole
		tx, content string
	}{
		{InstalledBinary, "", "old"}, {UpdateCandidate, testTransaction, "new"}, {UpdateMarker, testTransaction, testTransaction},
	} {
		if err := store.Save(t.Context(), item.role, item.tx, []byte(item.content)); err != nil {
			t.Fatal(err)
		}
	}
	update, err := BeginUpdate(t.Context(), store, testTransaction, mustInspect(t, store, UpdateCandidate, testTransaction), UpdateIntent{Previous: CoreSelection{Channel: "stable"}, Next: CoreSelection{Channel: "alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := update.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := update.Commit(t.Context(), func(CoreSelection) error { return nil }); err == nil {
		t.Fatal("uncertain commit reported success")
	}
	if err := update.Rollback(t.Context(), func(CoreSelection) error { return nil }); err == nil {
		t.Fatal("uncertain commit rolled back")
	}
	if current, err := store.Load(t.Context(), InstalledBinary, ""); err != nil || string(current) != "new" {
		t.Fatalf("possibly committed core was reversed: %q %v", current, err)
	}
}

func TestCoreUpdateRejectsCandidateChangedAfterValidation(t *testing.T) {
	store := newMemoryStore()
	if err := store.Save(t.Context(), UpdateCandidate, testTransaction, []byte("validated")); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(t.Context(), UpdateMarker, testTransaction, []byte(testTransaction)); err != nil {
		t.Fatal(err)
	}
	expected := mustInspect(t, store, UpdateCandidate, testTransaction)
	if err := store.Save(t.Context(), UpdateCandidate, testTransaction, []byte("changed")); err != nil {
		t.Fatal(err)
	}
	intent := UpdateIntent{Previous: CoreSelection{Channel: "stable"}, Next: CoreSelection{Channel: "alpha"}}
	if u, err := BeginUpdate(t.Context(), store, testTransaction, expected, intent); err == nil || u != nil {
		t.Fatal("changed candidate was rebound as a validated update")
	}
}

func preparedUpdateFixture(t *testing.T) (*memoryStore, *UpdateTransaction) {
	t.Helper()
	s := newMemoryStore()
	for _, item := range []struct {
		role        ProvenanceRole
		tx, content string
	}{
		{InstalledBinary, "", "old"}, {UpdateCandidate, testTransaction, "new"}, {UpdateMarker, testTransaction, testTransaction},
	} {
		if err := s.Save(t.Context(), item.role, item.tx, []byte(item.content)); err != nil {
			t.Fatal(err)
		}
	}
	u, err := BeginUpdate(t.Context(), s, testTransaction, mustInspect(t, s, UpdateCandidate, testTransaction), UpdateIntent{Previous: CoreSelection{Channel: "stable"}, Next: CoreSelection{Channel: "alpha"}, WasRunning: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s, u
}

func TestCoreRollbackWaitsForOwnerHealthBeforeCompletion(t *testing.T) {
	s, u := preparedUpdateFixture(t)
	if err := u.Rollback(t.Context(), func(CoreSelection) error { return nil }); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenUpdate(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.journal.Phase != "rolling_back" {
		t.Fatalf("marked recovery complete before old core health: %s", reopened.journal.Phase)
	}
}

func TestCoreUpdateCleanupOnlyAfterOwnerCompletion(t *testing.T) {
	s, u := preparedUpdateFixture(t)
	lifecycle, ok := any(u).(interface {
		CompleteRollback(context.Context) error
		Finish(context.Context) error
	})
	if !ok {
		t.Fatal("update has no owner completion and cleanup lifecycle")
	}
	if err := lifecycle.Finish(t.Context()); err == nil {
		t.Fatal("cleanup retired a nonterminal update")
	}
	if err := u.Rollback(t.Context(), func(CoreSelection) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Finish(t.Context()); err == nil {
		t.Fatal("cleanup retired recovery before health confirmation")
	}
	if err := lifecycle.CompleteRollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := lifecycle.Finish(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := OpenUpdate(t.Context(), s); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains: %v", err)
	}
	if got, err := s.Load(t.Context(), InstalledBinary, ""); err != nil || string(got) != "old" {
		t.Fatalf("cleanup changed core: %q %v", got, err)
	}
}

func TestCoreUpdateCommittedCleanupFailureDoesNotBlockVerifiedExecution(t *testing.T) {
	s, u := preparedUpdateFixture(t)
	if err := u.Commit(t.Context(), func(CoreSelection) error { return nil }); err != nil {
		t.Fatal(err)
	}
	s.fail = s.step + 1
	if err := u.Finish(t.Context()); err == nil {
		t.Fatal("missing cleanup failure")
	}
	s.fail = 0
	v, err := OpenInstalledCore(t.Context(), s)
	if err != nil {
		t.Fatalf("committed core cannot run after deferred cleanup: %v", err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if err := u.RequireRecovery(t.Context(), errors.New("later cleanup failure")); err == nil {
		t.Fatal("missing diagnostic")
	}
	current, err := OpenUpdate(t.Context(), s)
	if err != nil || current.Phase() != "committed" {
		t.Fatal("later failure erased irreversible decision")
	}
	if err := current.Rollback(t.Context(), func(CoreSelection) error { return nil }); err == nil {
		t.Fatal("committed core can now be reversed")
	}
}

func TestCoreUpdateRejectsChangedPreviouslyAdmittedLocalCore(t *testing.T) {
	s, u := preparedUpdateFixture(t)
	if err := u.Rollback(t.Context(), func(CoreSelection) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := u.CompleteRollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := u.Finish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(t.Context(), InstalledBinary, "", []byte("unknown replacement")); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(t.Context(), UpdateCandidate, testTransaction, []byte("another candidate")); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(t.Context(), UpdateMarker, testTransaction, []byte(testTransaction)); err != nil {
		t.Fatal(err)
	}
	_, err := BeginUpdate(t.Context(), s, testTransaction, mustInspect(t, s, UpdateCandidate, testTransaction), u.Intent())
	if err == nil {
		t.Fatal("unobserved local replacement became the rollback target")
	}
}

func TestCoreUpdateTerminalCleanupRecoversEachRemovalBoundary(t *testing.T) {
	seed := func() (*memoryStore, *UpdateTransaction) {
		s, u := preparedUpdateFixture(t)
		if err := u.Commit(t.Context(), func(CoreSelection) error { return nil }); err != nil {
			t.Fatal(err)
		}
		s.step = 0
		return s, u
	}
	baseline, u := seed()
	if err := u.Finish(t.Context()); err != nil {
		t.Fatal(err)
	}
	for fail := 1; fail <= baseline.step; fail++ {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			s, u := seed()
			s.fail = fail
			_ = u.Finish(t.Context())
			s.fail = 0
			loaded, err := OpenUpdate(t.Context(), s)
			if err == nil {
				u = loaded
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if err := u.Finish(t.Context()); err != nil {
				t.Fatal(err)
			}
			if b, err := s.Load(t.Context(), InstalledBinary, ""); err != nil || string(b) != "new" {
				t.Fatalf("core changed: %q %v", b, err)
			}
			if _, err := OpenUpdate(t.Context(), s); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("cleanup incomplete: %v", err)
			}
		})
	}
}
