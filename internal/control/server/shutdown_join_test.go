package server

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

func TestServe_ForceClosesBeforeJoiningSnapshotWorker(t *testing.T) {
	s := newTestServer()
	s.shutdownTimeout = time.Millisecond
	entered := make(chan struct{})
	s.http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.snapshotWG.Add(1)
		defer s.snapshotWG.Done()
		close(entered)
		<-r.Context().Done()
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		resp, err := http.Get("http://" + ln.Addr().String())
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		_ = s.http.Close()
		<-done
		<-requestDone
		t.Fatal("shutdown did not force close the connection before joining the snapshot worker")
	}
	<-requestDone
}

func TestStatus_MachineSnapshotCapabilityBelongsToSource(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		s := newTestServer()
		if enabled {
			s.snapshotSource = newFixtureSnapshotSource()
		}
		request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
		request.Header.Set("Authorization", "Bearer test-token")
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		var got protocol.Status
		if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(got.Capabilities, protocol.MachineLogSnapshotCapability) != enabled {
			t.Fatalf("snapshot source=%v capabilities=%v", enabled, got.Capabilities)
		}
	}
}

type shutdownStreamRuntime struct {
	*fakeRuntime
	entered, finished chan struct{}
}

func (r *shutdownStreamRuntime) Stream(ctx context.Context, _ mihomo.StreamKind, _ func(json.RawMessage) error) error {
	close(r.entered)
	<-ctx.Done()
	close(r.finished)
	return ctx.Err()
}
func TestServe_JoinsHijackedStreamBeforeReturning(t *testing.T) {
	runtime := &shutdownStreamRuntime{fakeRuntime: &fakeRuntime{}, entered: make(chan struct{}), finished: make(chan struct{})}
	s := newTestServer()
	s.runtime = runtime
	s.http.Handler = s.Handler()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	conn, _, err := websocket.Dial(context.Background(), "ws://"+ln.Addr().String()+"/v1/streams/logs", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer test-token"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	<-runtime.entered
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server shutdown stuck")
	}
	select {
	case <-runtime.finished:
	default:
		_ = conn.CloseNow()
		t.Fatal("server returned while hijacked stream still owns runtime")
	}
}
