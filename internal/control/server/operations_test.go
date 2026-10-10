package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
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

type progressRuntime struct {
	*fakeRuntime
	started chan struct{}
	hold    chan struct{}
}

func (f *progressRuntime) Install(ctx context.Context, _ runtimeapi.Operation) (core.InstallResult, error) {
	core.ReportProgress(ctx, core.Progress{Phase: protocol.ProgressPhaseDownloading, Received: 1536, Total: 2048})
	close(f.started)
	<-f.hold
	return core.InstallResult{Version: "v1.19.0"}, nil
}

func TestOperationStatus_RunningInstallReportsProgress(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	f := &progressRuntime{fakeRuntime: &fakeRuntime{}, started: make(chan struct{}), hold: make(chan struct{})}
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: f})
	s.operations.now = func() time.Time { return now }
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Handler().ServeHTTP(httptest.NewRecorder(), authorizedRequest(http.MethodPost, "/v1/core/install", bytes.NewBufferString(`{"operation_id":"download"}`)))
	}()
	var release sync.Once
	finish := func() { release.Do(func() { close(f.hold) }) }
	t.Cleanup(func() { finish(); <-done })
	<-f.started
	now = now.Add(1500 * time.Millisecond)
	body := readOperationProgress(t, s, "download")
	if body.State != "running" || body.Progress == nil || body.Progress.Phase != protocol.ProgressPhaseDownloading {
		t.Fatalf("status=%+v", body)
	}
	if body.Progress.ReceivedBytes == nil || *body.Progress.ReceivedBytes != 1536 || body.Progress.TotalBytes == nil || *body.Progress.TotalBytes != 2048 {
		t.Fatalf("progress=%+v", body.Progress)
	}
	if body.Progress.ElapsedMilliseconds != 1500 {
		t.Fatalf("elapsed=%d", body.Progress.ElapsedMilliseconds)
	}
	finish()
	<-done
	finished := readOperationProgress(t, s, "download")
	if finished.State != "finished" || finished.Progress != nil {
		t.Fatalf("finished=%+v", finished)
	}
}

func readOperationProgress(t *testing.T, s *Server, id string) protocol.OperationStatus {
	t.Helper()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authorizedRequest(http.MethodGet, "/v1/operations/"+id, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("operation query status=%d", w.Code)
	}
	var body protocol.OperationStatus
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestOperationProgress_ReusedIDStartsWithoutOldProgress(t *testing.T) {
	var observation operationObservation
	finish := observation.begin("reused")
	observation.noteProgress("reused", core.Progress{Phase: protocol.ProgressPhaseChecking})
	finish()
	finish = observation.begin("reused")
	defer finish()
	if got := observation.snapshot("reused"); got.State != "running" || got.Progress != nil {
		t.Fatalf("new operation inherited old progress: %+v", got)
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
