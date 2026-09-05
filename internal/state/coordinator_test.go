package state

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
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

func TestCoordinatorCommittedErrorStoresSnapshotAndPreservesAPIError(t *testing.T) {
	store := NewStore(Snapshot{Revision: 7, Health: "ok"})
	coordinator := NewCoordinator(store)
	want := protocol.APIError{Code: protocol.CodeDataFailure, Message: "mutation compensation failed"}

	returned, err := coordinator.Do(context.Background(), CommandMeta{Source: "test"}, func(snapshot Snapshot) (Snapshot, error) {
		snapshot.Health = "degraded"
		return snapshot, CommittedError{Err: want}
	})
	if err == nil {
		t.Fatal("expected committed error")
	}
	if returned.Revision != 8 || returned.Health != "degraded" {
		t.Fatalf("returned=%#v", returned)
	}
	if got := store.Load(); got.Revision != 8 || got.Health != "degraded" {
		t.Fatalf("stored=%#v", got)
	}
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != want.Code || apiError.Message != want.Message {
		t.Fatalf("err=%v api=%#v", err, apiError)
	}
}

func TestCoordinatorOrdinaryErrorDoesNotStoreSnapshot(t *testing.T) {
	store := NewStore(Snapshot{Revision: 4, Health: "ok"})
	coordinator := NewCoordinator(store)
	_, err := coordinator.Do(context.Background(), CommandMeta{Source: "test"}, func(snapshot Snapshot) (Snapshot, error) {
		snapshot.Health = "degraded"
		return snapshot, errors.New("not committed")
	})
	if err == nil {
		t.Fatal("expected mutation error")
	}
	if got := store.Load(); got.Revision != 4 || got.Health != "ok" {
		t.Fatalf("ordinary error stored snapshot=%#v", got)
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
