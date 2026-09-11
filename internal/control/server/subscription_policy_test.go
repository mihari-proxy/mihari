package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestSubscriptionRefresh_UnknownErrorRemainsInternal(t *testing.T) {
	runtime := &fakeSubscriptionRuntime{fakeRuntime: &fakeRuntime{}, err: errors.New("private implementation failure")}
	srv := New(Options{Token: "token", Runtime: runtime})
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/v1/subscriptions/one/refresh", strings.NewReader(`{"operation_id":"unknown-failure"}`)))
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusInternalServerError || envelope.Error.Code != protocol.CodeInternal || envelope.Error.Message != "internal error" {
		t.Fatalf("unexpected error response: status=%d envelope=%+v", recorder.Code, envelope)
	}
}
