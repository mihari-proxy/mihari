package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestDiagnosticReference_FetchesOriginalWithoutRepeatingMutation(t *testing.T) {
	for _, availability := range []protocol.DiagnosticState{protocol.DiagnosticAvailable, protocol.DiagnosticExpired, protocol.DiagnosticRestarted, protocol.DiagnosticUnavailable} {
		t.Run(string(availability), func(t *testing.T) {
			var mutations, lookups atomic.Int32
			snapshot := protocol.Diagnostic{ID: "daemon:1", State: protocol.DiagnosticAvailable, Detail: "token=fixture-original\n/private/settings.yaml", Summary: "save settings", Code: protocol.CodeDataFailure}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-auth" {
					t.Error("missing authentication")
				}
				if r.Method == http.MethodPatch && r.URL.Path == "/v1/logging" {
					mutations.Add(1)
					ref := snapshot.Reference()
					w.WriteHeader(http.StatusUnprocessableEntity)
					_ = json.NewEncoder(w).Encode(protocol.ErrorEnvelope{Schema: "mihari.error/v1", Error: protocol.APIError{Code: protocol.CodeDataFailure, Message: snapshot.Summary, Diagnostic: &ref}})
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != "/v1/diagnostics/daemon:1" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				lookups.Add(1)
				if availability == protocol.DiagnosticUnavailable {
					http.Error(w, "lookup failed", http.StatusServiceUnavailable)
					return
				}
				result := protocol.DiagnosticResult{Schema: "mihari.diagnostic/v1", State: availability}
				if availability == protocol.DiagnosticAvailable {
					result.Diagnostic = &snapshot
				}
				_ = json.NewEncoder(w).Encode(result)
			}))
			defer server.Close()
			client := NewHTTP(server.URL, "fixture-auth", server.Client())
			_, err := client.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{OperationID: "fixture"})
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure || api.Message != "save settings" {
				t.Fatalf("lookup replaced original failure: %v", err)
			}
			got, ok := diagnostics.Snapshot(err)
			if !ok || got.State != availability || got.ID != snapshot.ID {
				t.Fatalf("detail availability=%+v", got)
			}
			if availability == protocol.DiagnosticAvailable && got.Detail != snapshot.Detail {
				t.Fatalf("detail changed: %q", got.Detail)
			}
			if mutations.Load() != 1 || lookups.Load() != 1 {
				t.Fatalf("requests: mutation=%d lookup=%d", mutations.Load(), lookups.Load())
			}
		})
	}
}

func TestDiagnosticReference_OldDaemonIsExplicitWithoutExtraRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"schema":"mihari.error/v1","error":{"code":"invalid_state","message":"legacy failure"}}`))
	}))
	defer server.Close()
	client := NewHTTP(server.URL, "fixture-auth", server.Client())
	_, err := client.Core(context.Background())
	got, ok := diagnostics.Snapshot(err)
	if !ok || got.State != protocol.DiagnosticUnsupported || got.Detail != "" || !strings.Contains(err.Error(), "legacy failure") {
		t.Fatalf("legacy failure lost or fabricated: %+v %v", got, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("unexpected legacy probe: %d", requests.Load())
	}
}

func TestDiagnosticWarnings_FailureRetainsEarlierWarning(t *testing.T) {
	warning := protocol.Diagnostic{ID: "daemon:2", State: protocol.DiagnosticAvailable, Detail: "cleanup token=fixture-warning"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		envelope := protocol.NewError(protocol.CodeInternal, "operation failed", nil)
		envelope.Warnings = []protocol.Warning{{Message: "cleanup warning", Diagnostic: &warning}}
		_ = json.NewEncoder(w).Encode(envelope)
	}))
	defer server.Close()
	client := NewHTTP(server.URL, "fixture-auth", server.Client())
	_, err := client.Core(context.Background())
	var api protocol.APIError
	if !errors.As(err, &api) || api.Message != "operation failed" || len(api.Warnings) != 1 || api.Warnings[0].Diagnostic == nil || api.Warnings[0].Diagnostic.Detail != warning.Detail {
		t.Fatalf("failure discarded the accompanying warning: %+v", api)
	}
}

func TestDiagnosticHistory_RejectsInvalidMetadataWithoutReportingRecursively(t *testing.T) {
	cases := map[string]func(*protocol.DiagnosticList){
		"unknown state":       func(p *protocol.DiagnosticList) { p.State = "invented" },
		"foreign instance":    func(p *protocol.DiagnosticList) { p.Records[0].InstanceID = "other" },
		"mismatched identity": func(p *protocol.DiagnosticList) { p.Records[0].ID = "daemon:3" },
		"body in list":        func(p *protocol.DiagnosticList) { p.Records[0].Detail = "unexpected token=fixture" },
		"unbounded summary":   func(p *protocol.DiagnosticList) { p.Records[0].Summary = strings.Repeat("x", 4097) },
		"no progress":         func(p *protocol.DiagnosticList) { p.NextSequence = 1 },
		"future record":       func(p *protocol.DiagnosticList) { p.LatestSequence = 1 },
		"duplicate record":    func(p *protocol.DiagnosticList) { p.Records = append(p.Records, p.Records[0]) },
		"empty continuation":  func(p *protocol.DiagnosticList) { p.Records = nil },
	}
	for name, invalidate := range cases {
		t.Run(name, func(t *testing.T) {
			page := protocol.DiagnosticList{Schema: "mihari.diagnostics/v1", State: protocol.DiagnosticAvailable, InstanceID: "daemon", OldestSequence: 1, LatestSequence: 3, NextSequence: 2, HasMore: true, Records: []protocol.Diagnostic{{ID: "daemon:2", InstanceID: "daemon", Sequence: 2, State: protocol.DiagnosticReference, Summary: "fixture"}}}
			invalidate(&page)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(page) }))
			defer server.Close()
			client := NewHTTP(server.URL, "fixture", server.Client())
			reports := 0
			if err := client.SetDiagnosticReporter(func(context.Context, diagnostics.Record) { reports++ }); err != nil {
				t.Fatal(err)
			}
			got, err := client.Diagnostics(context.Background(), "daemon", 1, 50)
			if err == nil {
				t.Fatal("invalid diagnostic history was accepted")
			}
			if len(got.Records) != 0 {
				t.Fatal("invalid records escaped into caller history")
			}
			if reports != 0 {
				t.Fatal("diagnostic query failure recursively reported")
			}
		})
	}
}

func TestDiagnosticHistory_ValidPagesMatchServerCursorSemantics(t *testing.T) {
	history, err := diagnostics.NewHistory(diagnostics.HistoryOptions{InstanceID: "daemon", MaxRecords: 2})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		history.Add(protocol.Diagnostic{Summary: "fixture", Detail: "original token=fixture"})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Exercise actual history pagination, including eviction and instance reset.
		after := uint64(0)
		if r.URL.Query().Get("after") == "2" {
			after = 2
		}
		if r.URL.Query().Get("after") == "3" {
			after = 3
		}
		_ = json.NewEncoder(w).Encode(history.List(r.URL.Query().Get("instance_id"), after, 1))
	}))
	defer server.Close()
	client := NewHTTP(server.URL, "fixture", server.Client())
	for _, request := range []struct {
		instance string
		after    uint64
	}{{"", 0}, {"daemon", 2}, {"daemon", 3}, {"previous", 2}} {
		got, err := client.Diagnostics(context.Background(), request.instance, request.after, 1)
		if err != nil {
			t.Fatalf("valid history page rejected for %+v: %v", request, err)
		}
		if got.InstanceID != "daemon" {
			t.Fatal("history instance changed")
		}
	}
}

func TestDiagnosticReferences_ErrorHeaderPreservesCauseAndDoesNotReplay(t *testing.T) {
	for _, state := range []protocol.DiagnosticState{protocol.DiagnosticAvailable, protocol.DiagnosticExpired, protocol.DiagnosticUnavailable} {
		t.Run(string(state), func(t *testing.T) {
			var mutations, reads atomic.Int32
			detail := protocol.Diagnostic{ID: "fixture:1", State: protocol.DiagnosticAvailable, Summary: "original failure", Detail: "token=fixture-full-cause"}
			reference := detail.Reference()
			metadata, err := protocol.EncodeDiagnosticReferences(protocol.DiagnosticReferences{Schema: "mihari.diagnostic-references/v1", Diagnostic: &reference})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPatch {
					mutations.Add(1)
					w.Header().Set(protocol.DiagnosticReferencesHeader, metadata)
					w.WriteHeader(http.StatusUnprocessableEntity)
					_ = json.NewEncoder(w).Encode(protocol.NewError(protocol.CodeDataFailure, "original failure", map[string]any{"object": "fixture"}))
					return
				}
				reads.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/v1/diagnostics/fixture:1" {
					t.Error("unexpected diagnostic request")
				}
				if state == protocol.DiagnosticUnavailable {
					http.Error(w, "fixture lookup failure", http.StatusServiceUnavailable)
					return
				}
				result := protocol.DiagnosticResult{Schema: "mihari.diagnostic/v1", State: state}
				if state == protocol.DiagnosticAvailable {
					result.Diagnostic = &detail
				}
				_ = json.NewEncoder(w).Encode(result)
			}))
			defer server.Close()
			client := NewHTTP(server.URL, "fixture", server.Client())
			_, err = client.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{OperationID: "fixture-operation"})
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure || api.Message != "original failure" || api.Details["object"] != "fixture" {
				t.Fatal("header transfer changed original error")
			}
			got, ok := diagnostics.Snapshot(err)
			if !ok || got.ID != detail.ID || got.State != state {
				t.Fatal("diagnostic reference availability lost")
			}
			if state == protocol.DiagnosticAvailable && got.Detail != detail.Detail {
				t.Fatal("original cause lost")
			}
			if mutations.Load() != 1 || reads.Load() != 1 {
				t.Fatal("diagnostic transfer replayed a mutation or extra query")
			}
		})
	}
}

func TestDiagnosticReferences_MalformedHeaderDoesNotInvalidateCommit(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set(protocol.DiagnosticReferencesHeader, "invalid:base64")
		_ = json.NewEncoder(w).Encode(protocol.LoggingStatus{Schema: "mihari/v1", Revision: 42, Level: "debug"})
	}))
	defer server.Close()
	client := NewHTTP(server.URL, "fixture", server.Client())
	result, err := client.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{OperationID: "fixture-operation"})
	if err != nil || result.Revision != 42 || result.Level != "debug" || requests.Load() != 1 {
		t.Fatal("diagnostic metadata failure changed committed result")
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Diagnostic == nil || result.Warnings[0].Diagnostic.State != protocol.DiagnosticUnavailable || result.Warnings[0].Diagnostic.Detail == "" {
		t.Fatal("metadata failure was silently discarded")
	}
}

func TestDiagnosticDetail_RejectsMalformedMetadataWithoutPublishing(t *testing.T) {
	for name, change := range map[string]func(*protocol.DiagnosticResult){
		"summary":          func(r *protocol.DiagnosticResult) { r.Diagnostic.Summary = strings.Repeat("x", 4097) },
		"object":           func(r *protocol.DiagnosticResult) { r.Diagnostic.Object = strings.Repeat("x", 1025) },
		"instance":         func(r *protocol.DiagnosticResult) { r.Diagnostic.InstanceID = strings.Repeat("x", 129) },
		"severity":         func(r *protocol.DiagnosticResult) { r.Diagnostic.Severity = "invented" },
		"unavailable body": func(r *protocol.DiagnosticResult) { r.State = protocol.DiagnosticExpired },
	} {
		t.Run(name, func(t *testing.T) {
			value := protocol.DiagnosticResult{Schema: "mihari.diagnostic/v1", State: protocol.DiagnosticAvailable, Diagnostic: &protocol.Diagnostic{ID: "daemon:1", State: protocol.DiagnosticAvailable, Detail: "token=fixture-original"}}
			change(&value)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(value) }))
			defer server.Close()
			client := NewHTTP(server.URL, "fixture", server.Client())
			reports := 0
			if err := client.SetDiagnosticReporter(func(context.Context, diagnostics.Record) { reports++ }); err != nil {
				t.Fatal(err)
			}
			got, err := client.Diagnostic(context.Background(), "daemon:1")
			if err == nil || got.Diagnostic != nil || reports != 0 {
				t.Fatal("invalid detail accepted or recursively published")
			}
		})
	}
}
