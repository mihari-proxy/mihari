package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
)

type webDiagnosticBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *webDiagnosticBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}
func (b *webDiagnosticBuffer) text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.String()
}
func newWebDiagnostics() (diagnostics.Reporter, *webDiagnosticBuffer) {
	out := new(webDiagnosticBuffer)
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	redactor := logging.NewRedactor()
	return logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(out, level, "daemon", redactor)), redactor), out
}
func assertWebDiagnostics(t *testing.T, out *webDiagnosticBuffer, event, level string, count int) {
	t.Helper()
	text := out.text()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if len(records) != count {
		t.Fatalf("diagnostic count=%d, want %d: %s", len(records), count, text)
	}
	for _, r := range records {
		if r["msg"] != event || r["level"] != level || r["component"] != "web" {
			t.Fatalf("unexpected diagnostic: %#v", r)
		}
		if _, ok := r["operation_id"]; ok {
			t.Fatal("server invented operation ID")
		}
	}
	for _, secret := range []string{task5ControllerSecret, task5WebCredential, "private-body", "private-query", "private-address", "upstream-private-diagnostic-task5"} {
		if strings.Contains(text, secret) {
			t.Fatal("diagnostic leaked sensitive value")
		}
	}
}

func TestGatewayMutationDiagnostics_RejectionsAndFallback(t *testing.T) {
	for _, tt := range []struct {
		name, method, path, body string
		err                      error
		nilMutator               bool
		status                   int
		event, level             string
	}{
		{"parse", "PUT", "/proxies/group", `private-body`, nil, false, 400, "mutation.rejected", "DEBUG"},
		{"empty name", "PUT", "/proxies/group", `{"name":""}`, nil, false, 400, "mutation.rejected", "DEBUG"},
		{"config parse", "PATCH", "/configs", `private-body`, nil, false, 400, "mutation.rejected", "DEBUG"},
		{"empty patch", "PATCH", "/configs", `{}`, nil, false, 403, "mutation.rejected", "DEBUG"},
		{"managed", "PATCH", "/configs", `{"secret":"private-body"}`, nil, false, 403, "mutation.rejected", "DEBUG"},
		{"unknown key", "PATCH", "/configs", `{"private-body":true}`, nil, false, 403, "mutation.rejected", "DEBUG"},
		{"tun shape", "PATCH", "/configs", `{"tun":"private-body"}`, nil, false, 400, "mutation.rejected", "DEBUG"},
		{"tun enable", "PATCH", "/configs", `{"tun":{"enable":"private-body"}}`, nil, false, 400, "mutation.rejected", "DEBUG"},
		{"tun stack", "PATCH", "/configs", `{"tun":{"enable":true,"stack":42}}`, nil, false, 400, "mutation.rejected", "DEBUG"},
		{"unknown route", "POST", "/configs/private-body", `{}`, nil, false, 403, "mutation.rejected", "DEBUG"},
		{"unsupported", "PATCH", "/configs", `{"tun":{"enable":true}}`, nil, true, 403, "mutation.rejected", "DEBUG"},
		{"unwired action", "POST", "/restart", `{}`, nil, false, 403, "mutation.rejected", "DEBUG"},
		{"api", "PATCH", "/configs", `{"tun":{"enable":true}}`, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "safe public message"}, false, 400, "mutation.failed", "DEBUG"},
		{"select failure", "PUT", "/proxies/group", `{"name":"private-body"}`, os.ErrPermission, false, 502, "mutation.failed", "ERROR"},
		{"close failure", "DELETE", "/connections/private-body", ``, os.ErrPermission, false, 502, "mutation.failed", "ERROR"},
		{"close all failure", "DELETE", "/connections", ``, os.ErrPermission, false, 502, "mutation.failed", "ERROR"},
		{"ordinary", "PATCH", "/configs", `{"tun":{"enable":true}}`, &os.PathError{Op: "read", Path: "/private-body", Err: os.ErrPermission}, false, 502, "mutation.failed", "ERROR"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reporter, out := newWebDiagnostics()
			m := &recordingMutator{err: tt.err}
			var mutator Mutator = m
			if tt.nilMutator {
				mutator = nil
			}
			gateway, err := New(Options{Addr: "127.0.0.1:0", Auth: Authenticator{WebCredential: task5WebCredential}, ControllerURL: "http://private-address", Mutator: mutator, Reporter: reporter})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(tt.method, tt.path+"?token=private-query", strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer "+task5WebCredential)
			rec := httptest.NewRecorder()
			gateway.handler().ServeHTTP(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status=%d want %d", rec.Code, tt.status)
			}
			if tt.err != nil {
				var api protocol.APIError
				if errors.As(tt.err, &api) {
					if !strings.Contains(rec.Body.String(), api.Message) {
						t.Fatal("API response changed")
					}
				} else if rec.Body.String() != "mutation failed\n" {
					t.Fatal("ordinary response changed")
				}
			}
			assertWebDiagnostics(t, out, tt.event, tt.level, 1)
		})
	}
}

func TestControllerProxyDiagnostics_SafeFailureAndOriginalContext(t *testing.T) {
	reporter, out := newWebDiagnostics()
	gateway, err := New(Options{Addr: "127.0.0.1:0", ControllerURL: "http://private-address", ControllerSecret: task5ControllerSecret, Reporter: reporter, Transport: task5RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer "+task5ControllerSecret {
			t.Fatal("controller auth changed")
		}
		return nil, &url.Error{Op: "Get", URL: "http://private-address/path?token=private-query", Err: &os.PathError{Op: "read", Path: "/private-body", Err: os.ErrPermission}}
	})})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/version?token=private-query", nil)
	req.Header.Set("Authorization", "Bearer "+task5WebCredential)
	rec := httptest.NewRecorder()
	gateway.Proxy.ServeHTTP(rec, req)
	if rec.Code != 502 || rec.Body.String() != "upstream controller unavailable\n" {
		t.Fatal("proxy failure response changed")
	}
	assertWebDiagnostics(t, out, "proxy.failed", "ERROR", 1)
	if !strings.Contains(out.text(), "permission denied") {
		t.Fatal("typed cause lost")
	}
}

func TestGatewayDiagnostics_NormalCancellationIsQuiet(t *testing.T) {
	reporter, out := newWebDiagnostics()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	proxy, err := NewControllerProxy(ProxyOptions{ControllerURL: "http://private-address", Reporter: reporter, Transport: task5RoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, ctx.Err() })})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest("GET", "/version", nil).WithContext(ctx))
	if rec.Code != 502 {
		t.Fatal("cancellation response changed")
	}
	assertWebDiagnostics(t, out, "", "", 0)
}

func TestGatewayWebSocketAcceptFailureDiagnostic(t *testing.T) {
	controller, state := newTask5WebSocketController(t, func(ctx context.Context, conn *websocket.Conn) error { _, _, err := conn.Read(ctx); return err })
	gateway := newTask5Gateway(t, controller.URL, nil)
	reporter, out := newWebDiagnostics()
	gateway.Reporter = reporter
	req := httptest.NewRequest("GET", "/connections?token=private-query", nil)
	rec := httptest.NewRecorder()
	gateway.proxyWebSocket(rec, req)
	if rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("accept status=%d", rec.Code)
	}
	waitDone(t, state.done, "accept failure upstream cleanup")
	assertWebDiagnostics(t, out, "websocket.handshake.failed", "ERROR", 1)
}

func TestGatewayWebSocketGracefulClientCloseIsQuiet(t *testing.T) {
	for _, status := range []websocket.StatusCode{websocket.StatusNormalClosure, websocket.StatusGoingAway} {
		t.Run(status.String(), func(t *testing.T) {
			controller, state := newTask5WebSocketController(t, func(ctx context.Context, conn *websocket.Conn) error { _, _, err := conn.Read(ctx); return err })
			gateway := newTask5Gateway(t, controller.URL, nil)
			reporter, out := newWebDiagnostics()
			gateway.Reporter = reporter
			observer := newWebSocketRelayJoinObserver()
			gateway.wsObserver = observer
			stream := dialTask5GatewayStream(t, serveWebSocketGateway(t, gateway))
			waitDone(t, state.accepted, "upstream accepted")
			if err := stream.Close(status, ""); err != nil {
				t.Fatal(err)
			}
			select {
			case active := <-observer.handlerResult:
				if active != 0 {
					t.Fatal("handler left relay active")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("handler did not join relays")
			}
			waitDone(t, state.done, "graceful upstream close")
			assertWebDiagnostics(t, out, "", "", 0)
		})
	}
}

func TestControllerProxyDiagnostics_PreservesLocalMetadata(t *testing.T) {
	reporter, out := newWebDiagnostics()
	proxy, err := NewControllerProxy(ProxyOptions{ControllerURL: "http://private-address", Reporter: reporter, Transport: task5RoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if len(r.Header.Values("X-Operation-ID")) != 0 {
			t.Error("new metadata header sent")
		}
		return nil, os.ErrPermission
	})})
	if err != nil {
		t.Fatal(err)
	}
	ctx := logging.WithOperation(context.Background(), logging.OperationMetadata{ID: "local-read-1", Name: "local.read"})
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest("GET", "/version", nil).WithContext(ctx))
	var record map[string]any
	if err := json.Unmarshal([]byte(out.text()), &record); err != nil {
		t.Fatal(err)
	}
	if record["operation_id"] != "local-read-1" || record["operation"] != "local.read" {
		t.Fatal("proxy discarded local context")
	}
}

func TestGatewayWebSocketReadLimitKeepsSingleFailureOwner(t *testing.T) {
	send := make(chan struct{})
	controller, state := newTask5WebSocketController(t, func(ctx context.Context, conn *websocket.Conn) error {
		select {
		case <-send:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := conn.Write(ctx, websocket.MessageText, bytes.Repeat([]byte("private-body"), 8192)); err != nil {
			return err
		}
		_, _, err := conn.Read(ctx)
		return err
	})
	gateway := newTask5Gateway(t, controller.URL, nil)
	reporter, out := newWebDiagnostics()
	gateway.Reporter = reporter
	observer := newWebSocketRelayJoinObserver()
	gateway.wsObserver = observer
	stream := dialTask5GatewayStream(t, serveWebSocketGateway(t, gateway))
	stream.SetReadLimit(1 << 20)
	waitDone(t, state.accepted, "upstream accepted")
	close(send)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := stream.Read(ctx); err == nil {
		t.Fatal("oversized upstream message passed gateway read bound")
	}
	if ctx.Err() != nil {
		t.Fatal("browser stopped from test timeout")
	}
	select {
	case active := <-observer.handlerResult:
		if active != 0 {
			t.Fatal("handler left relay active")
		}
	case <-ctx.Done():
		t.Fatal("handler did not join relays")
	}
	waitDone(t, state.done, "read-limit upstream cleanup")
	assertWebDiagnostics(t, out, "websocket.relay.failed", "ERROR", 1)
}

// delayedWebSocketReadObserver delays only the first completion's publication.
// Tests keep reverse traffic idle until the native close-reading relay finishes.
type delayedWebSocketReadObserver struct {
	*webSocketRelayJoinObserver
	finished     atomic.Int32
	readFinished chan struct{}
	releaseRead  chan struct{}
}

func (o *delayedWebSocketReadObserver) relayFinished() {
	o.webSocketRelayJoinObserver.relayFinished()
	if o.finished.Add(1) == 1 {
		close(o.readFinished)
		<-o.releaseRead
	}
}

func TestGatewayWebSocketCloseReadArrivesAfterInducedWrite(t *testing.T) {
	for _, status := range []websocket.StatusCode{websocket.StatusNormalClosure, websocket.StatusGoingAway, websocket.StatusPolicyViolation} {
		t.Run(status.String(), func(t *testing.T) {
			send := make(chan struct{})
			controller, state := newTask5WebSocketController(t, func(ctx context.Context, conn *websocket.Conn) error {
				select {
				case <-send:
				case <-ctx.Done():
					return ctx.Err()
				}
				if err := conn.Write(ctx, websocket.MessageText, []byte("private-body")); err != nil {
					return err
				}
				_, _, err := conn.Read(ctx)
				return err
			})
			gateway := newTask5Gateway(t, controller.URL, nil)
			reporter, out := newWebDiagnostics()
			observedStatus := make(chan websocket.StatusCode, 1)
			gateway.Reporter = func(ctx context.Context, record diagnostics.Record) {
				observedStatus <- websocket.CloseStatus(record.Err)
				reporter(ctx, record)
			}
			observer := &delayedWebSocketReadObserver{webSocketRelayJoinObserver: newWebSocketRelayJoinObserver(), readFinished: make(chan struct{}), releaseRead: make(chan struct{})}
			var release sync.Once
			releaseRead := func() { release.Do(func() { close(observer.releaseRead) }) }
			// Always release the observer before gateway cleanup, including test failure.
			gateway.wsObserver = observer
			base := serveWebSocketGateway(t, gateway)
			t.Cleanup(releaseRead)
			stream := dialTask5GatewayStream(t, base)
			waitDone(t, state.accepted, "upstream accepted")
			if err := stream.Close(status, "private-body"); err != nil {
				t.Fatal(err)
			}
			waitDone(t, observer.readFinished, "native close-read completion")
			close(send)
			// Only the closed-write result can reach the owner while read is held.
			// Its original close order shuts down upstream before waiting for read.
			waitDone(t, state.done, "owner closed upstream after write result")
			select {
			case <-observer.handlerResult:
				t.Fatal("handler did not join delayed reader")
			default:
			}
			assertWebDiagnostics(t, out, "", "", 0)
			releaseRead()
			select {
			case active := <-observer.handlerResult:
				if active != 0 {
					t.Fatal("handler left relay active")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("handler did not join relays")
			}
			want := 0
			if status == websocket.StatusPolicyViolation {
				want = 1
			}
			assertWebDiagnostics(t, out, "websocket.relay.failed", "ERROR", want)
			if want == 1 {
				select {
				case got := <-observedStatus:
					if got != status {
						t.Fatalf("reported close status=%v, want actual peer close %v", got, status)
					}
				default:
					t.Fatal("real close failure was not reported")
				}
			}
		})
	}
}

func TestWebSocketRelayFailure_PreservesIndependentFaults(t *testing.T) {
	client, upstream := new(websocket.Conn), new(websocket.Conn)
	normalRead := webSocketRelayResult{source: client, peer: upstream, reading: true, err: websocket.CloseError{Code: websocket.StatusNormalClosure}}
	for _, tt := range []struct {
		name          string
		first, second webSocketRelayResult
		want          error
	}{
		{"unrelated write survives same peer normal close", webSocketRelayResult{source: upstream, peer: client, err: os.ErrPermission}, normalRead, os.ErrPermission},
		{"mixed close frame and independent fault", webSocketRelayResult{source: client, peer: upstream, reading: true, err: errors.Join(normalRead.err, os.ErrPermission)}, webSocketRelayResult{err: context.Canceled, stopped: true}, os.ErrPermission},
		{"mixed closed and independent fault", webSocketRelayResult{source: upstream, peer: client, err: errors.Join(net.ErrClosed, os.ErrPermission)}, normalRead, os.ErrPermission},
		{"closed write without close evidence", webSocketRelayResult{source: upstream, peer: client, err: net.ErrClosed}, webSocketRelayResult{source: client, peer: upstream, reading: true, err: context.Canceled, stopped: true}, net.ErrClosed},
		{"different connection close is not evidence", webSocketRelayResult{source: client, peer: upstream, err: net.ErrClosed}, normalRead, net.ErrClosed},
		{"independent second read after normal close", normalRead, webSocketRelayResult{source: upstream, peer: client, reading: true, err: os.ErrPermission}, os.ErrPermission},
		{"real second fault during shutdown", normalRead, webSocketRelayResult{source: upstream, peer: client, reading: true, err: errors.Join(context.Canceled, os.ErrPermission), stopped: true}, os.ErrPermission},
		{"induced second closed result", normalRead, webSocketRelayResult{source: upstream, peer: client, reading: true, err: net.ErrClosed, stopped: true}, nil},
		{"normal read arrives second", webSocketRelayResult{source: upstream, peer: client, err: fmt.Errorf("write: %w", net.ErrClosed)}, normalRead, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := webSocketRelayFailure(context.Background(), tt.first, tt.second)
			if !errors.Is(got, tt.want) {
				t.Fatalf("selected error=%v, want cause %v", got, tt.want)
			}
		})
	}
}
