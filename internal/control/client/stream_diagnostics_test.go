package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/transport"
	transporttest "github.com/mihari-proxy/mihari/internal/control/transport/testutil"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
)

func streamJSONReporter(out io.Writer) diagnostics.Reporter {
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	redactor := logging.NewRedactor("stream-private-token")
	return logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(out, level, "tui", redactor)), redactor)
}

func assertStreamDiagnostic(t *testing.T, out *bytes.Buffer, event, level string) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	var record map[string]any
	if err := decoder.Decode(&record); err != nil {
		t.Fatalf("missing safe diagnostic: %v", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("extra diagnostic: %v", err)
	}
	if record["msg"] != event || record["level"] != level || record["component"] != "control.client" || record["operation_id"] != "stream-local" || record["operation"] != "stream.observe" {
		t.Fatalf("diagnostic=%v", record)
	}
	for _, key := range []string{"operation_id", "operation", "component", "cause"} {
		if bytes.Count(out.Bytes(), []byte(`"`+key+`":`)) != 1 {
			t.Fatalf("duplicate/missing key %s", key)
		}
	}
	if strings.Contains(out.String(), "stream-private-token") || strings.Contains(out.String(), "example.invalid") {
		t.Fatal("diagnostic leaked private data")
	}
}

func streamDiagnosticContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return logging.WithOperation(ctx, logging.OperationMetadata{ID: "stream-local", Name: "stream.observe"})
}

func TestStreamDiagnostics_TransportCauseAndReportedOwnership(t *testing.T) {
	cause := &urlFailure{cause: io.ErrUnexpectedEOF}
	c := NewHTTP("http://mihari", "stream-private-token", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, cause })})
	var out bytes.Buffer
	if err := c.SetDiagnosticReporter(streamJSONReporter(&out)); err != nil {
		t.Fatal(err)
	}
	err := c.Stream(streamDiagnosticContext(t), "logs", func(protocol.StreamEvent) error { return nil })
	assertControlCode(t, err, protocol.CodeDataFailure)
	if !errors.Is(err, cause) || !diagnostics.AlreadyReported(err) {
		t.Fatalf("cause or ownership lost: %T", err)
	}
	assertStreamDiagnostic(t, &out, "stream_failed", "ERROR")
	if !strings.Contains(out.String(), "unexpected end of input") {
		t.Fatal("known cause missing")
	}
}

type urlFailure struct{ cause error }

func (e *urlFailure) Error() string { return "https://example.invalid/?token=stream-private-token" }
func (e *urlFailure) Unwrap() error { return e.cause }

func TestStreamDiagnostics_RemoteEnvelopeAndInvalidHandshake(t *testing.T) {
	for _, tc := range []struct {
		name, body, event, level string
		code                     protocol.ErrorCode
	}{
		{"remote", `{"schema":"mihari.error/v1","error":{"code":"permission_denied","message":"denied"}}`, "stream_response", "DEBUG", protocol.CodePermissionDenied},
		{"invalid", `not json stream-private-token`, "stream_failed", "ERROR", protocol.CodeDataFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			c := NewHTTP(server.URL, "stream-private-token", server.Client())
			var out bytes.Buffer
			if err := c.SetDiagnosticReporter(streamJSONReporter(&out)); err != nil {
				t.Fatal(err)
			}
			err := c.Stream(streamDiagnosticContext(t), "logs", func(protocol.StreamEvent) error { return nil })
			assertControlCode(t, err, tc.code)
			if !diagnostics.AlreadyReported(err) {
				t.Fatal("response ownership missing")
			}
			assertStreamDiagnostic(t, &out, tc.event, tc.level)
		})
	}
}

func TestStreamDiagnostics_ReadDecodeAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		close     websocket.StatusCode
		code      protocol.ErrorCode
		message   string
	}{
		{name: "malformed", raw: `{"secret":"stream-private-token"`, code: protocol.CodeDataFailure, message: "invalid control stream event"},
		{name: "schema", raw: `{"schema":"other","stream":"logs","data":{}}`, code: protocol.CodeDataFailure, message: "invalid control stream event"},
		{name: "kind", raw: `{"schema":"mihari/v1","stream":"memory","data":{}}`, code: protocol.CodeDataFailure, message: "invalid control stream event"},
		{name: "oversize", raw: strings.Repeat("x", maxControlStreamSize+1), code: protocol.CodeDataFailure, message: "control stream message is too large"},
		{name: "unexpected close", close: websocket.StatusInternalError, code: protocol.CodeDaemonUnavailable, message: "control stream closed unexpectedly"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				if tc.close != 0 {
					_ = conn.Close(tc.close, "stream-private-token")
					return
				}
				// An oversize reader closes while Write is still active; that write error is expected.
				_ = conn.Write(r.Context(), websocket.MessageText, []byte(tc.raw))
				_, _, _ = conn.Read(r.Context())
			}))
			defer server.Close()
			c := NewHTTP(server.URL, "stream-private-token", server.Client())
			var out bytes.Buffer
			if err := c.SetDiagnosticReporter(streamJSONReporter(&out)); err != nil {
				t.Fatal(err)
			}
			err := c.Stream(streamDiagnosticContext(t), "logs", func(protocol.StreamEvent) error { t.Error("invalid event delivered"); return nil })
			assertControlCode(t, err, tc.code)
			var api protocol.APIError
			if !errors.As(err, &api) || api.Message != tc.message {
				t.Fatalf("public error=%v", err)
			}
			if tc.name == "malformed" {
				var syntax *json.SyntaxError
				if !errors.As(err, &syntax) {
					t.Fatal("decode cause lost")
				}
			}
			if tc.name == "oversize" && !errors.Is(err, websocket.ErrMessageTooBig) {
				t.Fatal("size cause lost")
			}
			assertStreamDiagnostic(t, &out, "stream_failed", "ERROR")
		})
	}
}

func TestStreamDiagnostics_CallbackAndNormalTerminationStayQuiet(t *testing.T) {
	for _, mode := range []string{"callback", "cancel", "normal", "nil reporter"} {
		t.Run(mode, func(t *testing.T) {
			callbackErr := errors.New("stream-private-token output failed")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				for range 2 {
					if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"schema":"mihari/v1","stream":"logs","data":{"type":"info"}}`)); err != nil {
						return
					}
				}
				_ = conn.Close(websocket.StatusNormalClosure, "done")
			}))
			defer server.Close()
			c := NewHTTP(server.URL, "stream-private-token", server.Client())
			var out bytes.Buffer
			if mode != "nil reporter" {
				if err := c.SetDiagnosticReporter(streamJSONReporter(&out)); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(streamDiagnosticContext(t))
			defer cancel()
			count := 0
			err := c.Stream(ctx, "logs", func(event protocol.StreamEvent) error {
				count++
				if event.Schema != "mihari/v1" || event.Stream != "logs" || string(event.Data) != `{"type":"info"}` {
					t.Fatal("event changed")
				}
				if mode == "callback" || mode == "nil reporter" {
					return callbackErr
				}
				if mode == "cancel" {
					cancel()
				}
				return nil
			})
			if mode == "callback" || mode == "nil reporter" {
				if err != callbackErr || count != 1 {
					t.Fatalf("callback identity/count changed: %v %d", err, count)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if mode == "normal" && count != 2 {
				t.Fatalf("event count=%d", count)
			}
			if out.Len() != 0 {
				t.Fatalf("normal/callback termination diagnosed: %s", out.String())
			}
		})
	}
}

func TestStreamDiagnostics_CancellationDeadlineAndNilReporter(t *testing.T) {
	for _, mode := range []string{"canceled dial", "upstream deadline", "nil reporter", "already reported"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(streamDiagnosticContext(t))
			defer cancel()
			cause := context.DeadlineExceeded
			if mode == "already reported" {
				cause = diagnostics.MarkReported(diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "safe"}, io.ErrUnexpectedEOF))
			}
			c := NewHTTP("http://mihari", "token", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				if mode == "canceled dial" {
					cancel()
				}
				return nil, cause
			})})
			var out bytes.Buffer
			if mode != "nil reporter" {
				if err := c.SetDiagnosticReporter(streamJSONReporter(&out)); err != nil {
					t.Fatal(err)
				}
			}
			err := c.Stream(ctx, "logs", func(protocol.StreamEvent) error { return nil })
			if mode == "canceled dial" {
				if err != nil {
					t.Fatalf("canceled dial error=%v", err)
				}
			} else {
				assertControlCode(t, err, protocol.CodeDataFailure)
				leaf := cause
				if mode == "already reported" {
					leaf = io.ErrUnexpectedEOF
					if !diagnostics.AlreadyReported(err) {
						t.Fatal("existing ownership lost")
					}
				}
				if !errors.Is(err, leaf) {
					t.Fatal("transport cause lost")
				}
			}
			if mode == "upstream deadline" {
				assertStreamDiagnostic(t, &out, "stream_failed", "ERROR")
			} else if out.Len() != 0 {
				t.Fatalf("unexpected duplicate/cancellation diagnostic: %s", out.String())
			}
			if mode == "nil reporter" && diagnostics.AlreadyReported(err) {
				t.Fatal("nil reporter claimed ownership")
			}
		})
	}
}

func TestStreamDiagnostics_NativeTransportKeepsMetadataLocal(t *testing.T) {
	endpoint := transporttest.Endpoint(t)
	listener, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	handlerDone := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		if r.URL.Path != "/v1/streams/logs" || r.URL.RawQuery != "" {
			t.Errorf("stream target=%s", r.URL.Path)
		}
		for key, values := range r.Header {
			if strings.Contains(strings.ToLower(key), "operation") || strings.Contains(strings.Join(values, ""), "stream-local") {
				t.Error("local metadata crossed IPC")
			}
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		_ = conn.Write(r.Context(), websocket.MessageText, []byte(`{"schema":"invalid","stream":"logs","data":{}}`))
		_, _, _ = conn.Read(r.Context())
	})}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	defer func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-serverDone; !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	}()
	c := New(endpoint, "stream-private-token")
	var out bytes.Buffer
	if err := c.SetDiagnosticReporter(streamJSONReporter(&out)); err != nil {
		t.Fatal(err)
	}
	err = c.Stream(streamDiagnosticContext(t), "logs", func(protocol.StreamEvent) error { return nil })
	assertControlCode(t, err, protocol.CodeDataFailure)
	assertStreamDiagnostic(t, &out, "stream_failed", "ERROR")
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("native stream connection not closed")
	}
}
