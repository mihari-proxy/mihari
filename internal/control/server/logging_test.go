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

func TestLoggingEndpointRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"empty body", ``},
		{"empty operation ID", `{"level":"debug"}`},
		{"empty patch", `{"operation_id":"logging-1"}`},
		{"null only patch", `{"operation_id":"logging-1","level":null,"max_size_mb":null,"max_files":null}`},
		{"unknown field", `{"operation_id":"logging-1","level":"debug","unknown":true}`},
		{"string number", `{"operation_id":"logging-1","max_size_mb":"10"}`},
		{"float number", `{"operation_id":"logging-1","max_size_mb":10.5}`},
		{"exponent number", `{"operation_id":"logging-1","max_size_mb":1e1}`},
		{"overflow number", `{"operation_id":"logging-1","max_size_mb":9223372036854775808}`},
		{"invalid level", `{"operation_id":"logging-1","level":"verbose"}`},
		{"max size too small", `{"operation_id":"logging-1","max_size_mb":0}`},
		{"max size too large", `{"operation_id":"logging-1","max_size_mb":101}`},
		{"max files too small", `{"operation_id":"logging-1","max_files":0}`},
		{"max files too large", `{"operation_id":"logging-1","max_files":11}`},
		{"trailing JSON", `{"operation_id":"logging-1","level":"debug"} {"level":"warn"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newLoggingServer(&loggingTestRuntime{fakeRuntime: &fakeRuntime{}})
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, authorizedRequest(http.MethodPatch, "/v1/logging", bytes.NewBufferString(test.body)))
			assertLoggingError(t, recorder, http.StatusBadRequest, protocol.CodeInvalidArgument)
		})
	}
}

func TestLoggingEndpointMapsRuntimeErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
		code protocol.ErrorCode
	}{
		{"revision conflict", protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "logging changed"}, http.StatusConflict, protocol.CodeRevisionConflict},
		{"invalid state", protocol.APIError{Code: protocol.CodeInvalidState, Message: "logging unavailable"}, http.StatusConflict, protocol.CodeInvalidState},
		{"save failure", protocol.APIError{Code: protocol.CodeDataFailure, Message: "settings save failed"}, http.StatusUnprocessableEntity, protocol.CodeDataFailure},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Run("get", func(t *testing.T) {
				server := newLoggingServer(&loggingTestRuntime{fakeRuntime: &fakeRuntime{}, loggingErr: test.err})
				recorder := httptest.NewRecorder()
				server.Handler().ServeHTTP(recorder, authorizedRequest(http.MethodGet, "/v1/logging", nil))
				assertLoggingError(t, recorder, test.want, test.code)
			})
			t.Run("patch", func(t *testing.T) {
				server := newLoggingServer(&loggingTestRuntime{fakeRuntime: &fakeRuntime{}, updateErr: test.err})
				recorder := httptest.NewRecorder()
				server.Handler().ServeHTTP(recorder, authorizedRequest(http.MethodPatch, "/v1/logging", bytes.NewBufferString(`{"operation_id":"logging-1","level":"debug"}`)))
				assertLoggingError(t, recorder, test.want, test.code)
			})
		})
	}
}

func TestLoggingEndpointsReturnFullUnwrappedStatus(t *testing.T) {
	revision := uint64(7)
	status := protocol.LoggingStatus{Schema: "mihari/v1", Revision: 8, Level: "debug", MaxSizeMB: 20, MaxFiles: 5, Dir: "C:/logs"}
	runtime := &loggingTestRuntime{fakeRuntime: &fakeRuntime{}, loggingStatus: status, updateStatus: status}
	server := newLoggingServer(runtime)

	for _, test := range []struct {
		name   string
		method string
		body   string
	}{
		{"get", http.MethodGet, ""},
		{"patch", http.MethodPatch, `{"operation_id":"logging-1","if_revision":7,"level":"debug","max_size_mb":20,"max_files":5}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, authorizedRequest(test.method, "/v1/logging", bytes.NewBufferString(test.body)))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var got protocol.LoggingStatus
			if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got != status {
				t.Fatalf("status=%#v want=%#v", got, status)
			}
			var envelope protocol.ErrorEnvelope
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err == nil && envelope.Error.Code != "" {
				t.Fatalf("success response used error envelope: %s", recorder.Body.String())
			}
			if test.method == http.MethodPatch {
				if runtime.operation.ID != "logging-1" || runtime.operation.Source != "control" || runtime.operation.IfRevision == nil || *runtime.operation.IfRevision != revision {
					t.Fatalf("operation=%#v", runtime.operation)
				}
				if runtime.update.Level == nil || *runtime.update.Level != "debug" || runtime.update.MaxSizeMB == nil || *runtime.update.MaxSizeMB != 20 || runtime.update.MaxFiles == nil || *runtime.update.MaxFiles != 5 {
					t.Fatalf("update=%#v", runtime.update)
				}
			}
		})
	}
}

func TestLoggingEndpointRejectsRuntimeWithoutCapability(t *testing.T) {
	server := newLoggingServer(strippedRuntime{&fakeRuntime{}})
	for _, test := range []struct {
		method string
		body   string
	}{
		{http.MethodGet, ""},
		{http.MethodPatch, `{"operation_id":"logging-1","level":"debug"}`},
	} {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, authorizedRequest(test.method, "/v1/logging", bytes.NewBufferString(test.body)))
		assertLoggingError(t, recorder, http.StatusConflict, protocol.CodeInvalidState)
	}
}

func newLoggingServer(runtime RuntimeAPI) *Server {
	return New(Options{Token: "token", Store: state.NewStore(state.Snapshot{}), Runtime: runtime})
}

func assertLoggingError(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode protocol.ErrorCode) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, wantStatus, recorder.Body.String())
	}
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != wantCode {
		t.Fatalf("code=%q want=%q body=%s", envelope.Error.Code, wantCode, recorder.Body.String())
	}
}

type loggingTestRuntime struct {
	*fakeRuntime
	loggingStatus protocol.LoggingStatus
	loggingErr    error
	updateStatus  protocol.LoggingStatus
	updateErr     error
	operation     runtimeapi.Operation
	update        runtimeapi.LoggingUpdate
}

func (r *loggingTestRuntime) LoggingStatus(context.Context) (protocol.LoggingStatus, error) {
	return r.loggingStatus, r.loggingErr
}

func (r *loggingTestRuntime) UpdateLogging(_ context.Context, operation runtimeapi.Operation, update runtimeapi.LoggingUpdate) (protocol.LoggingStatus, error) {
	r.operation = operation
	r.update = update
	return r.updateStatus, r.updateErr
}
