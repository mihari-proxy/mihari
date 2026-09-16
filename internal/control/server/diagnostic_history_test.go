package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/state"
)

func diagnosticServer(t *testing.T, count int) (*Server, *diagnostics.History) {
	t.Helper()
	history, err := diagnostics.NewHistory(diagnostics.HistoryOptions{InstanceID: "server-instance", MaxRecords: count})
	if err != nil {
		t.Fatal(err)
	}
	owner := diagnostics.NewOwner(history, nil)
	return New(Options{Token: "fixture-auth", Store: state.NewStore(state.Snapshot{}), DiagnosticReporter: owner.Report, DiagnosticHistory: history}), history
}

func diagnosticRequest(server *Server, path string, authorized bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if authorized {
		request.Header.Set("Authorization", "Bearer fixture-auth")
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func TestControlError_ReturnsOriginalDiagnosticWithHistoryIdentity(t *testing.T) {
	server, history := diagnosticServer(t, 10)
	err := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "save settings"}, errors.New("password=fixture-original\n/private/settings.yaml"))
	response := httptest.NewRecorder()
	server.writeControlError(context.Background(), response, err)
	var envelope protocol.ErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusUnprocessableEntity || envelope.Error.Code != protocol.CodeDataFailure || envelope.Error.Message != "save settings" {
		t.Fatalf("classification changed: %d %+v", response.Code, envelope)
	}
	detail := envelope.Error.Diagnostic
	if detail == nil || !strings.Contains(detail.Detail, "password=fixture-original") || !strings.Contains(detail.Detail, "/private/settings.yaml") {
		t.Fatalf("original diagnostic missing: %+v", detail)
	}
	got := history.Get(detail.ID)
	if got.Diagnostic == nil || !got.Diagnostic.Time.Equal(detail.Time) {
		t.Fatalf("response and owner history differ: %+v", got)
	}
	// JSON has no monotonic clock or location identity; compare the instant above.
	stored := *got.Diagnostic
	stored.Time = detail.Time
	if stored != *detail {
		t.Fatalf("response changed the occurrence: stored=%+v response=%+v", stored, *detail)
	}
}

func TestDiagnosticHistory_AuthenticatedMetadataAndDetails(t *testing.T) {
	server, history := diagnosticServer(t, 10)
	owner := diagnostics.NewOwner(history, nil)
	owner.Report(context.Background(), diagnostics.Record{Component: "runtime", Event: "refresh.failed", Level: slog.LevelError, Err: errors.New("token=fixture-private")})
	response := diagnosticRequest(server, "/v1/diagnostics?limit=1", true)
	var page protocol.DiagnosticList
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Records) != 1 {
		t.Fatalf("history response = %d %s", response.Code, response.Body.String())
	}
	if page.Records[0].Detail != "" || page.Records[0].State != protocol.DiagnosticReference {
		t.Fatal("metadata response included the full detail")
	}
	response = diagnosticRequest(server, "/v1/diagnostics/"+page.Records[0].ID, true)
	var detail protocol.DiagnosticResult
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &detail) != nil || detail.Diagnostic == nil || detail.Diagnostic.Detail != "token=fixture-private" {
		t.Fatalf("detail response = %d %s", response.Code, response.Body.String())
	}
	response = diagnosticRequest(server, "/v1/diagnostics/"+page.Records[0].ID, false)
	if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), "fixture-private") {
		t.Fatal("diagnostic endpoint bypassed authentication")
	}
}

func TestDiagnosticHistory_CapabilityAndExpiry(t *testing.T) {
	server, history := diagnosticServer(t, 1)
	first := history.Add(protocol.Diagnostic{Detail: "first"})
	history.Add(protocol.Diagnostic{Detail: "second"})
	response := diagnosticRequest(server, "/v1/diagnostics/"+first.ID, true)
	var detail protocol.DiagnosticResult
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &detail) != nil || detail.State != protocol.DiagnosticExpired {
		t.Fatalf("expired detail = %d %s", response.Code, response.Body.String())
	}
	response = diagnosticRequest(server, "/v1/status", true)
	var status protocol.Status
	if json.Unmarshal(response.Body.Bytes(), &status) != nil || !slices.Contains(status.Capabilities, protocol.CapabilityDiagnostics) {
		t.Fatalf("diagnostic capability missing: %s", response.Body.String())
	}
}

func TestDecodeControlJSON_ReturnsOriginalCauseAndOwnerIdentity(t *testing.T) {
	for _, body := range []string{`{"token=fixture-unknown":1}`, `{"operation_id":"op"} {"token=fixture-trailing": }`} {
		t.Run(body, func(t *testing.T) {
			server, history := diagnosticServer(t, 10)
			request := httptest.NewRequest(http.MethodPost, "/v1/core/restart", strings.NewReader(body))
			response := httptest.NewRecorder()
			var input protocol.MutationRequest
			if server.decodeControlJSON(response, request, &input) {
				t.Fatal("invalid request accepted")
			}
			var envelope protocol.ErrorEnvelope
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusBadRequest || envelope.Error.Code != protocol.CodeInvalidArgument {
				t.Fatal("validation contract changed")
			}
			snapshot := envelope.Error.Diagnostic
			if snapshot == nil || snapshot.ID == "" {
				t.Fatal("response lost original decoder diagnostic")
			}
			records := history.List("", 0, 100).Records
			if len(records) != 1 || records[0].ID != snapshot.ID {
				t.Fatal("response did not reuse rejection owner identity")
			}
			stored := history.Get(snapshot.ID)
			if stored.Diagnostic == nil || stored.Diagnostic.Detail != snapshot.Detail {
				t.Fatal("response differs from captured cause")
			}
			want := `invalid character '}'`
			if strings.Contains(body, "unknown") {
				want = "token=fixture-unknown"
			}
			if !strings.Contains(snapshot.Detail, want) {
				t.Fatalf("original decoder cause missing: %q", snapshot.Detail)
			}
		})
	}
}
