package mihomo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestStreamReadsAllSupportedKindsWithAuthentication(t *testing.T) {
	for _, kind := range []StreamKind{StreamTraffic, StreamMemory, StreamLogs, StreamConnections} {
		t.Run(string(kind), func(t *testing.T) {
			serverErrors := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/"+string(kind) {
					serverErrors <- errors.New("unexpected stream path: " + request.URL.Path)
					return
				}
				if request.Header.Get("Authorization") != "Bearer stream-secret" {
					serverErrors <- errors.New("missing stream authorization")
					return
				}
				connection, err := websocket.Accept(response, request, nil)
				if err != nil {
					serverErrors <- err
					return
				}
				if err := connection.Write(request.Context(), websocket.MessageText, []byte(`{"kind":"`+kind+`"}`)); err != nil {
					serverErrors <- err
					return
				}
				serverErrors <- connection.Close(websocket.StatusNormalClosure, "done")
			}))
			defer server.Close()

			var messages []json.RawMessage
			err := NewClient(server.URL, "stream-secret", server.Client()).Stream(context.Background(), kind, func(message json.RawMessage) error {
				messages = append(messages, append(json.RawMessage(nil), message...))
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := <-serverErrors; err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 || string(messages[0]) != `{"kind":"`+string(kind)+`"}` {
				t.Fatalf("messages=%q", messages)
			}
		})
	}
}

func TestStreamStopsCleanlyWhenContextIsCancelled(t *testing.T) {
	accepted := make(chan struct{})
	release := make(chan struct{})
	var closeRelease sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		close(accepted)
		<-release
	}))
	defer func() {
		closeRelease.Do(func() { close(release) })
		server.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	reports := make(chan diagnostics.Record, 2)
	client := NewClient(server.URL, "secret", server.Client())
	client.SetDiagnosticReporter(func(_ context.Context, record diagnostics.Record) { reports <- record })
	go func() {
		done <- client.Stream(ctx, StreamTraffic, func(json.RawMessage) error {
			return nil
		})
	}()
	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("stream was not accepted")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled stream returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled stream did not stop")
	}
	closeRelease.Do(func() { close(release) })
	select {
	case record := <-reports:
		if record.Level != slog.LevelInfo || !errors.Is(record.Err, context.Canceled) {
			t.Fatal("stream cancellation lost original cause or INFO level")
		}
	default:
		t.Fatal("stream cancellation was not recorded by the available owner")
	}
	if len(reports) != 0 {
		t.Fatal("stream cancellation logged more than once")
	}
}

func TestReportStreamClose_ReportsOnlyGenuineCloseFailure(t *testing.T) {
	for _, expected := range []error{nil, net.ErrClosed, io.EOF, context.Canceled, websocket.CloseError{Code: websocket.StatusNormalClosure}} {
		var records []diagnostics.Record
		client := &Client{reporter: func(_ context.Context, record diagnostics.Record) { records = append(records, record) }}
		client.reportStreamClose(context.Background(), expected)
		if len(records) != 0 {
			t.Fatalf("expected close %v produced diagnostics: %+v", expected, records)
		}
	}

	cause := errors.New("close websocket transport fixture")
	var records []diagnostics.Record
	client := &Client{reporter: func(_ context.Context, record diagnostics.Record) { records = append(records, record) }}
	client.reportStreamClose(context.Background(), cause)
	if len(records) != 1 || records[0].Event != "stream.close.failed" || records[0].Level != slog.LevelWarn || !errors.Is(records[0].Err, cause) {
		t.Fatalf("genuine close cause missing: %+v", records)
	}
}

func TestStreamRejectsInvalidAndOversizedMessages(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{"invalid JSON", `not-json token=stream-fixture-secret`},
		{"oversized", `"` + strings.Repeat("x", maxStreamMessageSize) + `"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				connection, err := websocket.Accept(response, request, nil)
				if err != nil {
					return
				}
				defer connection.CloseNow()
				_ = connection.Write(request.Context(), websocket.MessageText, []byte(test.message))
			}))
			defer server.Close()

			err := NewClient(server.URL, "secret", server.Client()).Stream(context.Background(), StreamTraffic, func(json.RawMessage) error {
				return nil
			})
			var apiError protocol.APIError
			if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
				t.Fatalf("err=%v", err)
			}
			if test.name == "oversized" && !errors.Is(err, websocket.ErrMessageTooBig) {
				t.Fatal("oversize stream lost original websocket cause")
			}
			if test.name == "invalid JSON" {
				var syntax *json.SyntaxError
				var detail *diagnostics.HTTPError
				if !errors.As(err, &syntax) || !errors.As(err, &detail) || detail.Body != test.message {
					t.Fatal("invalid stream JSON lost syntax cause or original failed message")
				}
				if strings.Contains(err.Error(), "stream-fixture-secret") {
					t.Fatal("stream cause leaked into public error")
				}
			}
		})
	}
}

func TestStreamRejectsUnknownKindBeforeDial(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", "secret", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("should not dial")
	})})
	err := client.Stream(context.Background(), StreamKind("unknown"), func(json.RawMessage) error { return nil })
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeInvalidArgument {
		t.Fatalf("err=%v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
