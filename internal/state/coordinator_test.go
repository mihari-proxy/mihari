package state

import (
	"context"
	"sync"
	"testing"
)

func TestCoordinatorSerializesAndRejectsStaleRevision(t *testing.T) {
	store := NewStore(Snapshot{Revision: 1, Health: "starting"})
	coordinator := NewCoordinator(store)
	ctx := context.Background()

	first, err := coordinator.Do(ctx, CommandMeta{ID: "one", Source: "test", IfRevision: revision(1)}, func(snapshot Snapshot) (Snapshot, error) {
		snapshot.Health = "ok"
		return snapshot, nil
	})
	if err != nil || first.Revision != 2 || first.Health != "ok" {
		t.Fatalf("first=%#v err=%v", first, err)
	}

	_, err = coordinator.Do(ctx, CommandMeta{ID: "two", Source: "test", IfRevision: revision(1)}, func(snapshot Snapshot) (Snapshot, error) {
		snapshot.Health = "wrong"
		return snapshot, nil
	})
	if err == nil {
		t.Fatal("expected revision conflict")
	}
	if got := store.Load(); got.Revision != 2 || got.Health != "ok" {
		t.Fatalf("stale mutation changed state: %#v", got)
	}
}

func TestCoordinatorSerializesConcurrentMutations(t *testing.T) {
	store := NewStore(Snapshot{Revision: 0})
	coordinator := NewCoordinator(store)
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := coordinator.Do(context.Background(), CommandMeta{Source: "test"}, func(snapshot Snapshot) (Snapshot, error) {
				return snapshot, nil
			})
			if err != nil {
				t.Errorf("mutation: %v", err)
			}
		}()
	}
	wait.Wait()
	if got := store.Load().Revision; got != 32 {
		t.Fatalf("revision=%d want 32", got)
	}
}

func revision(value uint64) *uint64 { return &value }
