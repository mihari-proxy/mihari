package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

type fakeDataRuntime struct {
	*fakeRuntime
	err error
}

func (f *fakeDataRuntime) ResetUserData(_ context.Context, operation runtimeapi.Operation) (protocol.DataResetResult, error) {
	f.operation = operation
	if f.err != nil {
		return protocol.DataResetResult{}, f.err
	}
	f.snapshot.Revision++
	return protocol.DataResetResult{
		Schema: "mihari/v1", OperationID: operation.ID, Revision: f.snapshot.Revision, SetupRequired: true,
	}, nil
}

func TestDataResetRouteRequiresOperationAndRevision(t *testing.T) {
	base := &fakeRuntime{snapshot: state.Snapshot{Revision: 4}}
	fake := &fakeDataRuntime{fakeRuntime: base}
	server := New(Options{Token: "token", Store: state.NewStore(base.snapshot), Runtime: fake})

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/data/reset", bytes.NewBufferString(`{}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, authorizedRequest(http.MethodPost, "/v1/data/reset", bytes.NewBufferString(
		`{"operation_id":"reset-1","if_revision":4}`,
	)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if fake.operation.ID != "reset-1" || fake.operation.IfRevision == nil || *fake.operation.IfRevision != 4 {
		t.Fatalf("operation=%#v", fake.operation)
	}
	var got protocol.DataResetResult
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || got.Revision != 5 || !got.SetupRequired || got.OperationID != "reset-1" {
		t.Fatalf("result=%#v err=%v", got, err)
	}
}
