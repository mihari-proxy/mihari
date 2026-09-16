package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/mihomo"
)

type failingDiagnosticStream struct{ *fakeRuntime }

func (f failingDiagnosticStream) Stream(context.Context, mihomo.StreamKind, func(json.RawMessage) error) error {
	return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "upstream stream failed"}, errors.New("token=fixture-stream\nraw upstream body"))
}

func TestStreamDiagnostics_TerminalReferenceRequiresOptIn(t *testing.T) {
	for _, optIn := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "diagnostics"}[optIn], func(t *testing.T) {
			control, history := diagnosticServer(t, 10)
			control.runtime = failingDiagnosticStream{&fakeRuntime{}}
			server := httptest.NewServer(control.Handler())
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			header := http.Header{"Authorization": {"Bearer fixture-auth"}}
			if optIn {
				header.Set(protocol.DiagnosticCapabilityHeader, protocol.CapabilityDiagnostics)
			}
			conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/v1/streams/logs", &websocket.DialOptions{HTTPHeader: header})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			_, raw, err := conn.Read(ctx)
			if !optIn {
				if websocket.CloseStatus(err) != websocket.StatusInternalError {
					t.Fatalf("legacy close changed: %q %v", raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("missing terminal diagnostic: %v", err)
			}
			var event protocol.StreamEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Fatal(err)
			}
			if !event.Terminal || event.Stream != "logs" || event.Diagnostic == nil || event.Diagnostic.State != protocol.DiagnosticReference || event.Diagnostic.ID == "" || event.Diagnostic.Detail != "" {
				t.Fatalf("invalid terminal reference: %+v", event)
			}
			stored := history.Get(event.Diagnostic.ID)
			if stored.Diagnostic == nil || !strings.Contains(stored.Diagnostic.Detail, "token=fixture-stream") {
				t.Fatal("stream cause was not retained")
			}
			_, _, err = conn.Read(ctx)
			if websocket.CloseStatus(err) != websocket.StatusInternalError {
				t.Fatalf("terminal was not followed by close: %v", err)
			}
			if len(history.List("", 0, 100).Records) != 1 {
				t.Fatal("stream failure published more than once")
			}
		})
	}
}

func TestStreamDiagnostics_NormalCancellationDoesNotCreateOccurrence(t *testing.T) {
	for _, withCleanup := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "cleanup"}[withCleanup], func(t *testing.T) {
			server, history := diagnosticServer(t, 10)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			failure := context.Canceled
			if withCleanup {
				failure = errors.Join(failure, errors.New("cleanup token=fixture-failed"))
			}
			got := server.captureStreamFailure(ctx, failure)
			if !errors.Is(got, context.Canceled) {
				t.Fatal("cancellation cause lost")
			}
			records := history.List("", 0, 100).Records
			if !withCleanup && len(records) != 0 {
				t.Fatal("normal cancellation produced error history")
			}
			if withCleanup {
				if len(records) != 1 {
					t.Fatal("cleanup failure was discarded")
				}
				detail := history.Get(records[0].ID)
				if detail.Diagnostic == nil || !strings.Contains(detail.Diagnostic.Detail, "cleanup token=fixture-failed") {
					t.Fatal("cleanup detail lost")
				}
			}
		})
	}
}

// terminalFailListener fails the actual transport write of the final JSON frame,
// after allowing the websocket handshake to complete normally.
type terminalFailListener struct {
	net.Listener
	cause error
}

func (l terminalFailListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return terminalFailConn{Conn: conn, cause: l.cause}, nil
}

type terminalFailConn struct {
	net.Conn
	cause error
}

func (c terminalFailConn) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`"terminal":true`)) {
		return 0, c.cause
	}
	return c.Conn.Write(p)
}

func TestStreamDiagnostics_TerminalWriteFailureKeepsBothOccurrences(t *testing.T) {
	control, history := diagnosticServer(t, 10)
	control.runtime = failingDiagnosticStream{&fakeRuntime{}}
	finished := make(chan struct{})
	handler := control.Handler()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		handler.ServeHTTP(w, r)
	}))
	server.Listener = terminalFailListener{Listener: server.Listener, cause: errors.New("terminal transport token=fixture-write")}
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{"Authorization": {"Bearer fixture-auth"}}
	header.Set(protocol.DiagnosticCapabilityHeader, protocol.CapabilityDiagnostics)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/v1/streams/logs", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("failed terminal write unexpectedly delivered a frame")
	}
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("stream handler did not terminate")
	}
	records := history.List("", 0, 100).Records
	if len(records) != 2 {
		t.Fatalf("want original failure and transport failure once each, got %d", len(records))
	}
	details := ""
	for _, record := range records {
		result := history.Get(record.ID)
		if result.Diagnostic == nil {
			t.Fatal("record details missing")
		}
		details += result.Diagnostic.Detail + "\n"
	}
	if strings.Count(details, "token=fixture-stream") != 1 || strings.Count(details, "token=fixture-write") != 1 {
		t.Fatalf("original causes duplicated or missing: %q", details)
	}
}
