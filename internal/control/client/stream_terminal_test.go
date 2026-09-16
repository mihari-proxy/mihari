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
	"time"

	"github.com/coder/websocket"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestStreamTerminal_ResolvesReferenceWithoutDeliveringItAsData(t *testing.T) {
	var received, streams, queries atomic.Int32
	original := protocol.Diagnostic{ID: "daemon:1", State: protocol.DiagnosticAvailable, Code: protocol.CodeUpstreamFailure, Summary: "read upstream stream", Detail: strings.Repeat("\x00", diagnostics.MaxBytes)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/diagnostics/daemon:1" {
			queries.Add(1)
			if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer fixture" {
				t.Error("untyped or unauthenticated query")
			}
			_ = json.NewEncoder(w).Encode(protocol.DiagnosticResult{Schema: "mihari.diagnostic/v1", State: protocol.DiagnosticAvailable, Diagnostic: &original})
			return
		}
		streams.Add(1)
		if r.Header.Get(protocol.DiagnosticCapabilityHeader) != protocol.CapabilityDiagnostics {
			t.Error("stream did not opt in to diagnostic terminal events")
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		ref := original.Reference()
		raw, _ := json.Marshal(protocol.StreamEvent{Schema: "mihari/v1", Stream: "logs", Terminal: true, Diagnostic: &ref})
		if err := conn.Write(r.Context(), websocket.MessageText, raw); err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close(websocket.StatusInternalError, "stream failed")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client := NewHTTP(server.URL, "fixture", server.Client())
	err := client.Stream(ctx, "logs", func(protocol.StreamEvent) error { received.Add(1); return nil })
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeDaemonUnavailable {
		t.Fatalf("stream error category changed: %v", err)
	}
	snapshot, ok := diagnostics.Snapshot(err)
	if !ok || snapshot.ID != original.ID || snapshot.Detail != original.Detail {
		t.Fatal("terminal lost original detail")
	}
	if received.Load() != 0 || streams.Load() != 1 || queries.Load() != 1 {
		t.Fatalf("terminal replayed or reached data callback: data=%d streams=%d queries=%d", received.Load(), streams.Load(), queries.Load())
	}
}

func TestStreamTerminal_MissingReferenceKeepsActualTransportCause(t *testing.T) {
	for _, withOwner := range []bool{false, true} {
		t.Run(map[bool]string{false: "without logger", true: "local history"}[withOwner], func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				_ = conn.Close(websocket.StatusInternalError, "fixture-transport-close")
			}))
			defer server.Close()
			client := NewHTTP(server.URL, "fixture", server.Client())
			history, err := diagnostics.NewHistory(diagnostics.HistoryOptions{InstanceID: "local"})
			if err != nil {
				t.Fatal(err)
			}
			if withOwner {
				if err := client.SetDiagnosticReporter(diagnostics.NewOwner(history, nil).Report); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err = client.Stream(ctx, "logs", func(protocol.StreamEvent) error { t.Fatal("unexpected data callback"); return nil })
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != protocol.CodeDaemonUnavailable || api.Message != "control stream closed unexpectedly" {
				t.Fatal("stream classification changed")
			}
			if websocket.CloseStatus(err) != websocket.StatusInternalError {
				t.Fatal("original transport cause was lost")
			}
			snapshot, ok := diagnostics.Snapshot(err)
			if !ok || !strings.Contains(snapshot.Detail, "fixture-transport-close") || !strings.Contains(snapshot.RetrievalError, "Remote diagnostic details were not received") {
				t.Fatal("missing remote diagnostic was hidden or transport cause lost")
			}
			if requests.Load() != 1 {
				t.Fatal("missing reference triggered an ungrounded lookup or stream replay")
			}
			if withOwner {
				records := history.List("", 0, 10).Records
				if len(records) != 1 || records[0].ID != snapshot.ID || snapshot.InstanceID != "local" {
					t.Fatal("transport failure was not owned by local history")
				}
			}
		})
	}
}
