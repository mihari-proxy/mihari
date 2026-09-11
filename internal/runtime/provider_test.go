package runtime

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeProviderRefresh_MissingControllerDoesNotAdvanceRevision(t *testing.T) {
	m := New(Options{})
	before := m.store.Load().Revision
	err := m.RefreshProvider(context.Background(), Operation{ID: "native-missing", Source: "test"}, "rules")
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatal(err)
	}
	if m.store.Load().Revision != before {
		t.Fatal("failed provider operation advanced revision")
	}
}

func TestNativeProviderRefresh_AllowsCoordinatorDuringIOAndDeduplicates(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var calls atomic.Int32
	controller := &fakeController{updateRuleProvider: func(context.Context, string) error {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return nil
	}}
	joinOnCleanup := func(joined <-chan struct{}) {
		t.Cleanup(func() {
			unblock()
			select {
			case <-joined:
			case <-time.After(3 * time.Second):
				t.Error("provider regression goroutine did not terminate after release")
			}
		})
	}
	waitSignal := func(signal <-chan struct{}, message string) {
		t.Helper()
		select {
		case <-signal:
		case <-time.After(3 * time.Second):
			t.Fatal(message)
		}
	}
	waitResult := func(result <-chan error) {
		t.Helper()
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("provider regression operation did not complete")
		}
	}
	manager := newTestManager(Options{Controller: controller})
	revision := uint64(0)
	op := Operation{ID: "provider-blocked", Source: "test", IfRevision: &revision}
	firstDone := make(chan error, 1)
	firstJoined := make(chan struct{})
	joinOnCleanup(firstJoined)
	go func() {
		defer close(firstJoined)
		firstDone <- manager.RefreshProvider(context.Background(), op, "rules")
	}()
	waitSignal(entered, "provider request did not begin")
	duplicateContext := &doneObservedContext{Context: context.Background(), observed: make(chan struct{})}
	duplicateDone := make(chan error, 1)
	duplicateJoined := make(chan struct{})
	joinOnCleanup(duplicateJoined)
	go func() {
		defer close(duplicateJoined)
		duplicateDone <- manager.RefreshProvider(duplicateContext, op, "rules")
	}()
	waitSignal(duplicateContext.observed, "duplicate request did not join operation")
	stateDone := make(chan error, 1)
	stateJoined := make(chan struct{})
	joinOnCleanup(stateJoined)
	go func() {
		defer close(stateJoined)
		_, err := manager.coordinator.Do(context.Background(), state.CommandMeta{Source: "concurrent-state"}, func(s state.Snapshot) (state.Snapshot, error) { s.Health = "concurrent-observation"; return s, nil })
		stateDone <- err
	}()
	select {
	case err := <-stateDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("native provider IO blocked unrelated coordinator commit")
	}
	unblock()
	waitResult(firstDone)
	waitResult(duplicateDone)
	if calls.Load() != 1 {
		t.Fatal("duplicate provider request reached controller")
	}
	snapshot := manager.Snapshot()
	if snapshot.Revision != 2 || snapshot.Health != "concurrent-observation" {
		t.Fatalf("provider lost concurrent state or duplicate advanced revision: %#v", snapshot)
	}
}
