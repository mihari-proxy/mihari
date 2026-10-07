package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

type egressRuntime struct {
	*fakeRuntime
	calls     int
	selection protocol.EgressSelection
	op        runtimeapi.Operation
}

func (r *egressRuntime) EgressStatus(context.Context) (protocol.EgressStatus, error) {
	return protocol.EgressStatus{Schema: "mihari/v1", Selection: r.selection, State: "saved", Interfaces: []protocol.EgressInterface{}}, nil
}
func (r *egressRuntime) UpdateEgress(ctx context.Context, op runtimeapi.Operation, s protocol.EgressSelection) (protocol.EgressStatus, error) {
	r.calls++
	r.selection = s
	r.op = op
	return r.EgressStatus(ctx)
}

func TestEgressEndpoint_ValidatesAndAuthenticates(t *testing.T) {
	r := &egressRuntime{fakeRuntime: &fakeRuntime{}}
	s := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: r})
	for _, body := range []string{`{}`, `{"operation_id":"x","mode":"manual"}`, `{"operation_id":"x","mode":"automatic","interface_name":"Ethernet"}`, `{"operation_id":"x","mode":"other"}`, `{"operation_id":"x","mode":"automatic","force":true}`} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, authorizedRequest(http.MethodPatch, "/v1/egress", bytes.NewBufferString(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%s code=%d", body, w.Code)
		}
	}
	if r.calls != 0 {
		t.Fatal("invalid payload reached runtime")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, authorizedRequest(http.MethodPatch, "/v1/egress", bytes.NewBufferString(`{"operation_id":"set","if_revision":7,"mode":"manual","interface_name":"VPN 日本"}`)))
	if w.Code != http.StatusOK || r.calls != 1 || r.op.IfRevision == nil || *r.op.IfRevision != 7 || r.selection.InterfaceName != "VPN 日本" {
		t.Fatalf("code=%d runtime=%+v body=%s", w.Code, r, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/egress", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized code=%d", w.Code)
	}
}
