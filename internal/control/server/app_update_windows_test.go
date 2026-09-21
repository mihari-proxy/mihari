//go:build windows

package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/transport"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

type fixtureUpdateOwner struct {
	key    string
	exited *atomic.Bool
	closed *atomic.Int32
}

func (o *fixtureUpdateOwner) Key() string                          { return o.key }
func (o *fixtureUpdateOwner) Exited(context.Context) (bool, error) { return o.exited.Load(), nil }
func (o *fixtureUpdateOwner) Close() error                         { o.closed.Add(1); return nil }

// TestApplicationUpdate_HTTPLeaseSurvivesDisconnect exercises preparation, peer ownership and explicit release.
func TestApplicationUpdate_HTTPLeaseSurvivesDisconnect(t *testing.T) {
	m := runtimeapi.New(runtimeapi.Options{})
	s := New(Options{UpdateRuntimeJob: "fixture-job", Token: "fixture", Store: state.NewStore(state.Snapshot{}), Runtime: m})
	var exited atomic.Bool
	var closed atomic.Int32
	key := "owner"
	s.openUpdateOwner = func(context.Context) (transport.UpdateOwner, error) {
		return &fixtureUpdateOwner{key: key, exited: &exited, closed: &closed}, nil
	}
	t.Cleanup(func() { s.cancelSnapshots(); s.updateWG.Wait() })
	call := func(method, path, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer fixture")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		return w.Code
	}
	if code := call("POST", "/v1/app-update/prepare", `{"operation_id":"one"}`); code != 200 {
		t.Fatal(code)
	}
	if err := m.PrepareApplicationUpdate(context.Background(), "other"); err == nil {
		t.Fatal("completed request released gate")
	}
	if code := call("POST", "/v1/app-update/prepare", `{"operation_id":"one"}`); code != 200 {
		t.Fatalf("idempotent retry: %d", code)
	}
	key = "foreign"
	if code := call("DELETE", "/v1/app-update/prepare/one", ""); code != 403 {
		t.Fatalf("foreign process release: %d", code)
	}
	key = "owner"
	if code := call("DELETE", "/v1/app-update/prepare/one", ""); code != 200 {
		t.Fatal(code)
	}
	if err := m.PrepareApplicationUpdate(context.Background(), "other"); err != nil {
		t.Fatal(err)
	}
	if err := m.ReleaseApplicationUpdate("other"); err != nil {
		t.Fatal(err)
	}
}

// TestApplicationUpdate_OwnerExitReleasesGate retains a process lease, rather than a request TTL.
func TestApplicationUpdate_OwnerExitReleasesGate(t *testing.T) {
	m := runtimeapi.New(runtimeapi.Options{})
	s := New(Options{UpdateRuntimeJob: "fixture-job", Token: "fixture", Store: state.NewStore(state.Snapshot{}), Runtime: m})
	var exited atomic.Bool
	var closed atomic.Int32
	s.openUpdateOwner = func(context.Context) (transport.UpdateOwner, error) {
		return &fixtureUpdateOwner{key: "owner", exited: &exited, closed: &closed}, nil
	}
	t.Cleanup(func() { s.cancelSnapshots(); s.updateWG.Wait() })
	req := httptest.NewRequest("POST", "/v1/app-update/prepare", strings.NewReader(`{"operation_id":"one"}`))
	req.Header.Set("Authorization", "Bearer fixture")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	exited.Store(true)
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := m.PrepareApplicationUpdate(context.Background(), "next"); err == nil {
			if err = m.ReleaseApplicationUpdate("next"); err != nil {
				t.Fatal(err)
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("owner exit left gate closed")
		case <-ticker.C:
		}
	}
}
