package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mihari-proxy/mihari/internal/core"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

func TestOperationStatus_SaturationCannotMisreportUntrackedOwner(t *testing.T) {
	var observation operationObservation
	finish := make([]func(), maxObservedOperations)
	for i := range finish {
		finish[i] = observation.begin(fmt.Sprint(i))
	}
	untracked := observation.begin("overflow")
	finish[0]()
	duplicate := observation.begin("overflow")
	duplicate()
	if observation.state("overflow") == "finished" {
		t.Fatal("reported finished while the untracked execution is still running")
	}
	untracked()
	for _, done := range finish[1:] {
		done()
	}
}

type settlingRuntime struct {
	*fakeRuntime
	started chan struct{}
	finish  chan struct{}
}

func (f *settlingRuntime) Install(ctx context.Context, _ runtimeapi.Operation) (core.InstallResult, error) {
	close(f.started)
	// Model a commit that must finish even after the caller cancels.
	<-f.finish
	return core.InstallResult{}, ctx.Err()
}

func readOperationState(t *testing.T, s *Server, id string) string {
	t.Helper()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authorizedRequest(http.MethodGet, "/v1/operations/"+id, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("operation query status=%d", w.Code)
	}
	var body struct {
		Schema string `json:"schema"`
		ID     string `json:"operation_id"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Schema != "mihari/v1" || body.ID != id {
		t.Fatal("invalid operation query envelope")
	}
	return body.State
}

func TestOperationStatus_UnknownDoesNotExecute(t *testing.T) {
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{})})
	if got := readOperationState(t, s, "not-started"); got != "unknown" {
		t.Fatalf("state=%s", got)
	}
}

func TestOperationStatus_CancelledRequestRemainsRunningUntilSettlement(t *testing.T) {
	f := &settlingRuntime{fakeRuntime: &fakeRuntime{}, started: make(chan struct{}), finish: make(chan struct{})}
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: f})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Handler().ServeHTTP(httptest.NewRecorder(), authorizedRequest(http.MethodPost, "/v1/core/install", bytes.NewBufferString(`{"operation_id":"settling"}`)).WithContext(ctx))
	}()
	t.Cleanup(func() { close(f.finish); <-done })
	<-f.started
	cancel()
	if got := readOperationState(t, s, "settling"); got != "running" {
		t.Fatalf("state=%s", got)
	}
}

func TestOperationStatus_Finished(t *testing.T) {
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: &fakeRuntime{}})
	s.Handler().ServeHTTP(httptest.NewRecorder(), authorizedRequest(http.MethodPost, "/v1/core/install", bytes.NewBufferString(`{"operation_id":"finished"}`)))
	if got := readOperationState(t, s, "finished"); got != "finished" {
		t.Fatalf("state=%s", got)
	}
}

func TestOperationStatus_RejectsUnauthenticatedQuery(t *testing.T) {
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: &fakeRuntime{}})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/operations/finished", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", w.Code)
	}
}
