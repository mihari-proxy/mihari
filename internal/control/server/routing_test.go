package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"net/http"
	"net/http/httptest"
	"testing"
)

type routingRuntime struct {
	*fakeRuntime
	mode  string
	op    runtimeapi.Operation
	calls int
}

func (r *routingRuntime) RoutingStatus(context.Context) (protocol.RoutingStatus, error) {
	return protocol.RoutingStatus{Schema: "mihari/v1", DesiredMode: r.mode, State: "pending"}, nil
}
func (r *routingRuntime) UpdateRouting(ctx context.Context, op runtimeapi.Operation, mode string) (protocol.RoutingStatus, error) {
	r.mode, r.op = mode, op
	r.calls++
	return r.RoutingStatus(ctx)
}

func TestRoutingEndpoint_ValidatesAndForwards(t *testing.T) {
	r := &routingRuntime{fakeRuntime: &fakeRuntime{}, mode: "rule"}
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: r})
	for _, body := range []string{`{"operation_id":"one","mode":"global","if_revision":3}`, `{"operation_id":"two","mode":"direct"}`} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, authorizedRequest(http.MethodPatch, "/v1/routing", bytes.NewBufferString(body)))
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var got protocol.RoutingStatus
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.DesiredMode != r.mode || r.op.Source != "control" || r.op.ID == "" {
			t.Fatal("request not forwarded")
		}
	}
	for _, body := range []string{`{}`, `{"mode":"rule"}`, `{"operation_id":"x","mode":"proxy"}`, `{"operation_id":"x","mode":null}`, `{"operation_id":"x","mode":"rule","secret":"x"}`, `{"operation_id":"x","mode":"rule"} {}`} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, authorizedRequest(http.MethodPatch, "/v1/routing", bytes.NewBufferString(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid request status=%d", w.Code)
		}
	}
	if r.calls != 2 {
		t.Fatal("invalid request reached mutation")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authorizedRequest(http.MethodGet, "/v1/routing", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET status=%d", w.Code)
	}
}
