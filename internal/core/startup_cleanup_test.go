package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const nextCleanupTransaction = "1123456789abcdef0123456789abcdef"

func TestDeferredCoreCleanup_AllowsNextUpdateWithoutDeletingOldFiles(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed", true: "rolled_back"}[rollback], func(t *testing.T) {
			s, first := preparedUpdateFixture(t)
			if rollback {
				if err := first.Rollback(t.Context(), func(CoreSelection) error { return nil }); err != nil {
					t.Fatal(err)
				}
				if err := first.CompleteRollback(t.Context()); err != nil {
					t.Fatal(err)
				}
			} else if err := first.Commit(t.Context(), func(CoreSelection) error { return nil }); err != nil {
				t.Fatal(err)
			}
			backup := mustInspect(t, s, UpdateBackup, testTransaction)
			for role, content := range map[ProvenanceRole]string{UpdateCandidate: "next", UpdateMarker: nextCleanupTransaction} {
				if err := s.Save(t.Context(), role, nextCleanupTransaction, []byte(content)); err != nil {
					t.Fatal(err)
				}
			}
			next, err := BeginUpdate(t.Context(), s, nextCleanupTransaction, mustInspect(t, s, UpdateCandidate, nextCleanupTransaction), first.Intent())
			if err != nil {
				t.Fatalf("completed update blocked next update: %v", err)
			}
			if got := mustInspect(t, s, UpdateBackup, testTransaction); !sameObject(got, backup) {
				t.Fatal("next update removed previous backup")
			}
			if !mustInspect(t, s, UpdateMarker, testTransaction).Present {
				t.Fatal("previous cleanup identity lost")
			}
			if err := next.Publish(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := next.Commit(t.Context(), func(CoreSelection) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if got, err := s.Load(t.Context(), InstalledBinary, ""); err != nil || string(got) != "next" {
				t.Fatalf("core=%q err=%v", got, err)
			}
		})
	}
}

func (s *memoryStore) cleanupTransactions(ctx context.Context) ([]string, error) {
	seen := map[string]bool{}
	for key := range s.disk.files {
		parts := strings.Split(key, "/")
		if len(parts) == 5 && parts[0] == "staging" && parts[2] == "update" && validTransaction(parts[3]) {
			seen[parts[3]] = true
		}
	}
	var transactions []string
	for tx := range seen {
		transactions = append(transactions, tx)
	}
	sort.Strings(transactions)
	return transactions, ctx.Err()
}

func (s *memoryStore) syncCleanupRecord(ctx context.Context, _ string) error { return s.Sync(ctx) }

func (s *memoryStore) removeEmptyCleanupDirectory(context.Context, string) error { return nil }

func TestFileStartupCleanup_RemovesOnlyEmptyTransactionDirectories(t *testing.T) {
	root := t.TempDir()
	s, err := NewUpdateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	for index, tx := range []string{testTransaction, nextCleanupTransaction} {
		body := "first"
		if index == 1 {
			body = "latest"
		}
		for role, content := range map[ProvenanceRole]string{UpdateCandidate: body, UpdateMarker: tx} {
			if err := s.Save(t.Context(), role, tx, []byte(content)); err != nil {
				t.Fatal(err)
			}
		}
		u, err := BeginUpdate(t.Context(), s, tx, mustInspect(t, s, UpdateCandidate, tx), UpdateIntent{Previous: CoreSelection{Channel: "stable"}, Next: CoreSelection{Channel: "stable"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := u.Publish(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := u.Commit(t.Context(), func(CoreSelection) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if err := u.DeferCleanup(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	unknown := filepath.Join(root, "staging", "core", "update", "2223456789abcdef0123456789abcdef")
	if err := os.Mkdir(unknown, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unknown, "user-backup"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CleanupCompletedUpdates(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	assertCleanupFinished(t, s)
	for _, tx := range []string{testTransaction, nextCleanupTransaction} {
		if _, err := os.Stat(filepath.Join(root, "staging", "core", "update", tx)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("empty transaction directory remains: %s: %v", tx, err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(unknown, "user-backup")); err != nil || string(raw) != "preserve" {
		t.Fatalf("unknown material changed: %q %v", raw, err)
	}
}

type cleanupSyncFailureStore struct {
	*memoryStore
	failure error
}

func (s *cleanupSyncFailureStore) coreStore() storeBackend                         { return s }
func (s *cleanupSyncFailureStore) syncCleanupRecord(context.Context, string) error { return s.failure }

func TestDeferredCoreCleanup_RetainsActiveJournalUntilCleanupDirectoryDurable(t *testing.T) {
	s, u := preparedUpdateFixture(t)
	if err := u.Commit(t.Context(), func(CoreSelection) error { return nil }); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("cleanup directory sync failed")
	u.store = &cleanupSyncFailureStore{memoryStore: s, failure: cause}
	if err := u.DeferCleanup(t.Context()); !errors.Is(err, cause) {
		t.Fatalf("missing directory sync failure: %v", err)
	}
	if _, err := OpenUpdate(t.Context(), s); err != nil {
		t.Fatalf("lost active journal before durable cleanup record: %v", err)
	}
	u.store = s
	if err := u.DeferCleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !mustInspect(t, s, UpdateCleanup, testTransaction).Present {
		t.Fatal("retry lost cleanup authority")
	}
}

func completedCleanupFixture(t *testing.T) *memoryStore {
	t.Helper()
	s, u := preparedUpdateFixture(t)
	if err := u.Commit(t.Context(), func(CoreSelection) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for role, content := range map[ProvenanceRole]string{UpdateCandidate: "latest", UpdateMarker: nextCleanupTransaction} {
		if err := s.Save(t.Context(), role, nextCleanupTransaction, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	next, err := BeginUpdate(t.Context(), s, nextCleanupTransaction, mustInspect(t, s, UpdateCandidate, nextCleanupTransaction), u.Intent())
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := next.Commit(t.Context(), func(CoreSelection) error { return nil }); err != nil {
		t.Fatal(err)
	}
	s.step = 0
	return s
}

func assertCleanupFinished(t *testing.T, s ProvenanceStore) {
	t.Helper()
	for _, tx := range []string{testTransaction, nextCleanupTransaction} {
		for _, role := range []ProvenanceRole{UpdateBackup, UpdateRestore, UpdateCandidate, UpdateMarker, UpdateCleanup} {
			if got := mustInspect(t, s, role, tx); got.Present {
				t.Fatalf("retained %s %s", tx, role)
			}
		}
	}
	if _, err := s.Load(t.Context(), UpdateJournal, ""); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains: %v", err)
	}
	if got, err := s.Load(t.Context(), InstalledBinary, ""); err != nil || string(got) != "latest" {
		t.Fatalf("installed core changed: %q %v", got, err)
	}
}

func TestStartupCoreCleanup_CleansHistoricalAndCurrentCompletedTransactions(t *testing.T) {
	s := completedCleanupFixture(t)
	for range 2 {
		if err := CleanupCompletedUpdates(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		assertCleanupFinished(t, s)
	}
}

func TestStartupCoreCleanup_RetriesEveryMutationBoundary(t *testing.T) {
	baseline := completedCleanupFixture(t)
	if err := CleanupCompletedUpdates(t.Context(), baseline); err != nil {
		t.Fatal(err)
	}
	for fail := 1; fail <= baseline.step; fail++ {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			s := completedCleanupFixture(t)
			s.fail = fail
			_ = CleanupCompletedUpdates(t.Context(), s)
			reopened := &memoryStore{disk: s.disk}
			if err := CleanupCompletedUpdates(t.Context(), reopened); err != nil {
				t.Fatal(err)
			}
			assertCleanupFinished(t, reopened)
		})
	}
}

func TestStartupCoreCleanup_PreservesIncompleteAndChangedObjects(t *testing.T) {
	for _, scenario := range []string{"incomplete", "changed backup", "changed marker", "invalid record"} {
		t.Run(scenario, func(t *testing.T) {
			s, u := preparedUpdateFixture(t)
			if scenario != "incomplete" {
				if err := u.Commit(t.Context(), func(CoreSelection) error { return nil }); err != nil {
					t.Fatal(err)
				}
				if err := u.deferCleanupLocked(t.Context()); err != nil {
					t.Fatal(err)
				}
				role := map[string]ProvenanceRole{"changed backup": UpdateBackup, "changed marker": UpdateMarker, "invalid record": UpdateCleanup}[scenario]
				if err := s.Save(t.Context(), role, testTransaction, []byte("changed")); err != nil {
					t.Fatal(err)
				}
			}
			backup := mustInspect(t, s, UpdateBackup, testTransaction)
			err := CleanupCompletedUpdates(t.Context(), s)
			if scenario != "incomplete" && err == nil {
				t.Fatal("missing identity/record warning")
			}
			if got := mustInspect(t, s, UpdateBackup, testTransaction); !sameObject(got, backup) {
				t.Fatal("recovery backup removed")
			}
		})
	}
}
