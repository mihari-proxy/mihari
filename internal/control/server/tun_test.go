package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/state"
)

func TestTunStatusReturnsDTO(t *testing.T) {
	live := true
	fake := &fakeRuntime{
		tunStatus: protocol.TunStatus{
			Schema:        "mihari/v1",
			Revision:      5,
			DesiredEnable: true,
			LiveEnable:    &live,
			Stack:         "gVisor",
			Managed:       true,
		},
	}
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{Revision: 5}), Runtime: fake})

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodGet, "/v1/tun", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got protocol.TunStatus
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != "mihari/v1" || got.Revision != 5 || !got.DesiredEnable || !got.Managed ||
		got.Stack != "gVisor" || got.LiveEnable == nil || !*got.LiveEnable {
		t.Fatalf("status=%#v", got)
	}
}

func TestTunEnablePassesOperation(t *testing.T) {
	live := true
	fake := &fakeRuntime{
		tunStatus: protocol.TunStatus{
			Schema: "mihari/v1", Revision: 9, DesiredEnable: true, LiveEnable: &live, Stack: "gVisor", Managed: true,
		},
	}
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{Revision: 8}), Runtime: fake})

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/tun/enable",
		bytes.NewBufferString(`{"operation_id":"tun-1","if_revision":8}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.operation.ID != "tun-1" || fake.operation.Source != "control" ||
		fake.operation.IfRevision == nil || *fake.operation.IfRevision != 8 {
		t.Fatalf("operation=%#v", fake.operation)
	}
	var got protocol.TunStatus
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || !got.DesiredEnable || !got.Managed {
		t.Fatalf("status=%#v err=%v", got, err)
	}
}

func TestTunEnableRequiresOperationID(t *testing.T) {
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: &fakeRuntime{}})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/tun/enable",
		bytes.NewBufferString(`{}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != protocol.CodeInvalidArgument {
		t.Fatalf("envelope=%#v err=%v", envelope, err)
	}
}

func TestTunEnableMapsPermissionDenied(t *testing.T) {
	fake := &fakeRuntime{
		enableTunErr: protocol.APIError{
			Code:    protocol.CodePermissionDenied,
			Message: "TUN requires elevated privileges",
		},
	}
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: fake})

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/tun/enable",
		bytes.NewBufferString(`{"operation_id":"tun-perm-1"}`)))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != protocol.CodePermissionDenied {
		t.Fatalf("envelope=%#v", envelope)
	}
	if fake.operation.ID != "tun-perm-1" {
		t.Fatalf("operation=%#v", fake.operation)
	}
}

func TestTunDisablePassesOperation(t *testing.T) {
	fake := &fakeRuntime{
		tunStatus: protocol.TunStatus{
			Schema: "mihari/v1", Revision: 10, DesiredEnable: false, Managed: true, Stack: "gVisor",
		},
	}
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{Revision: 9}), Runtime: fake})

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/tun/disable",
		bytes.NewBufferString(`{"operation_id":"tun-off-1","if_revision":9}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.operation.ID != "tun-off-1" || fake.operation.Source != "control" ||
		fake.operation.IfRevision == nil || *fake.operation.IfRevision != 9 {
		t.Fatalf("operation=%#v", fake.operation)
	}
	var got protocol.TunStatus
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || got.DesiredEnable || !got.Managed {
		t.Fatalf("status=%#v err=%v", got, err)
	}
}
