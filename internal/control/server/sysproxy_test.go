package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/LeeShunEE/mihari/internal/state"
)

func TestSystemProxyStatusReturnsDTO(t *testing.T) {
	fake := &fakeRuntime{
		systemProxyStatus: protocol.SystemProxyStatus{
			Schema:   "mihari/v1",
			Revision: 5,
			Desired:  true,
			Target:   "127.0.0.1:9190",
			Observed: protocol.SystemProxyObserved{
				Enabled: true, Server: "127.0.0.1:9190", Owned: true,
			},
		},
	}
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{Revision: 5}), Runtime: fake})

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodGet, "/v1/system-proxy", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var got protocol.SystemProxyStatus
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Schema != "mihari/v1" || got.Revision != 5 || !got.Desired || got.Target != "127.0.0.1:9190" ||
		!got.Observed.Enabled || !got.Observed.Owned || got.Observed.Server != "127.0.0.1:9190" {
		t.Fatalf("status=%#v", got)
	}
}

func TestSystemProxyEnablePassesForceAndOperation(t *testing.T) {
	fake := &fakeRuntime{
		systemProxyStatus: protocol.SystemProxyStatus{
			Schema: "mihari/v1", Revision: 9, Desired: true, Target: "127.0.0.1:9190",
			Observed: protocol.SystemProxyObserved{Enabled: true, Server: "127.0.0.1:9190", Owned: true},
		},
	}
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{Revision: 8}), Runtime: fake})

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/system-proxy/enable",
		bytes.NewBufferString(`{"operation_id":"sysproxy-1","if_revision":8,"force":true}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.operation.ID != "sysproxy-1" || fake.operation.Source != "control" ||
		fake.operation.IfRevision == nil || *fake.operation.IfRevision != 8 || !fake.systemProxyForce {
		t.Fatalf("operation=%#v force=%v", fake.operation, fake.systemProxyForce)
	}
	var got protocol.SystemProxyStatus
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || !got.Desired {
		t.Fatalf("status=%#v err=%v", got, err)
	}
}

func TestSystemProxyEnableRequiresOperationID(t *testing.T) {
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: &fakeRuntime{}})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/system-proxy/enable",
		bytes.NewBufferString(`{"force":true}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Error.Code != protocol.CodeInvalidArgument {
		t.Fatalf("envelope=%#v err=%v", envelope, err)
	}
}

func TestSystemProxyDisableMapsNotOwnedError(t *testing.T) {
	fake := &fakeRuntime{
		disableSystemProxyErr: protocol.APIError{
			Code:    protocol.CodeSystemProxyNotOwned,
			Message: "system proxy is managed by another application; Mihari will not clear it",
		},
	}
	server := New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: fake})

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/system-proxy/disable",
		bytes.NewBufferString(`{"operation_id":"sysproxy-disable-1"}`)))
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != protocol.CodeSystemProxyNotOwned {
		t.Fatalf("envelope=%#v", envelope)
	}
	if fake.operation.ID != "sysproxy-disable-1" || fake.systemProxyForce {
		t.Fatalf("operation=%#v force=%v (force must be ignored on disable)", fake.operation, fake.systemProxyForce)
	}
}
