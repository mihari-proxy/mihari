package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/session"
)

// This storage fixture exercises client/session refresh over a real HTTP/WS
// transport. Trusted filesystem discovery and peer verification have separate
// Unix tests; an httptest adapter does not claim those public-root guarantees.
type readOnlyCredentialFixture struct{ path string }

func (f readOnlyCredentialFixture) Load(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(raw), "\n"), nil
}

func startCredentialDaemonFixture(t *testing.T, address, token string) (*httptest.Server, func(), <-chan string) {
	t.Helper()
	stop := make(chan struct{})
	started := make(chan string, 8)
	var once sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			if err := json.NewEncoder(w).Encode(protocol.NewError(protocol.CodePermissionDenied, "control authentication failed", nil)); err != nil {
				t.Error(err)
			}
			return
		}
		if r.URL.Path == "/v1/status" {
			if err := json.NewEncoder(w).Encode(protocol.Status{Schema: "mihari/v1", Health: "ok"}); err != nil {
				t.Error(err)
			}
			return
		}
		kind := strings.TrimPrefix(r.URL.Path, "/v1/streams/")
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		streamCtx := conn.CloseRead(r.Context())
		select {
		case started <- kind:
		case <-stop:
			return
		}
		select {
		case <-stop:
		case <-streamCtx.Done():
		}
	})
	s := httptest.NewUnstartedServer(handler)
	if address != "" {
		if err := s.Listener.Close(); err != nil {
			t.Fatal(err)
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		s.Listener = listener
	}
	s.Start()
	closeServer := func() { once.Do(func() { close(stop); s.CloseClientConnections(); s.Close() }) }
	t.Cleanup(closeServer)
	return s, closeServer, started
}

func awaitCredentialEvent(t *testing.T, events <-chan session.Event, accept func(session.Event) bool) session.Event {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("session closed before recovery")
			}
			if accept(event) {
				return event
			}
		case <-deadline.C:
			t.Fatal("session did not reach expected credential state")
		}
	}
}

func TestCredentialRefresh_LongLivedSessionRecoversAfterStopDeleteStart(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "control.token")
	write := func(token string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	first := strings.Repeat("a", 64)
	write(first)
	s, stop, started := startCredentialDaemonFixture(t, "", first)
	address := s.Listener.Addr().String()
	httpClient := s.Client()
	c := controlclient.NewHTTPWithCredentialProvider(s.URL, readOnlyCredentialFixture{path}, httpClient)
	sess := session.New(c, session.Options{Backoff: func(int) time.Duration { return 5 * time.Millisecond }, PollInterval: 5 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events := sess.Start(ctx)
	defer sess.Close()
	awaitCredentialEvent(t, events, func(e session.Event) bool { return e.Kind == session.EventConnected })
	for range 4 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("initial streams did not start")
		}
	}
	// Unsupported online rewrite is refused by the fixed server token; it
	// cannot cause the client to retry its previous successfully loaded value.
	write(strings.Repeat("b", 64))
	event := awaitCredentialEvent(t, events, func(e session.Event) bool {
		var api protocol.APIError
		return e.Kind == session.EventReconnecting && errors.As(e.Err, &api) && api.Code == protocol.CodePermissionDenied
	})
	if !strings.Contains(event.Err.Error(), "restart the service") {
		t.Fatal("online rewrite refusal lacked conditional advice")
	}
	stop()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	awaitCredentialEvent(t, events, func(e session.Event) bool {
		var api protocol.APIError
		return e.Kind == session.EventReconnecting && errors.As(e.Err, &api) && api.Code == protocol.CodeDaemonUnavailable
	})
	third := strings.Repeat("c", 64)
	write(third)
	_, _, restarted := startCredentialDaemonFixture(t, address, third)
	awaitCredentialEvent(t, events, func(e session.Event) bool { return e.Kind == session.EventConnected })
	for range 4 {
		select {
		case <-restarted:
		case <-ctx.Done():
			t.Fatal("streams did not reconnect using the regenerated token")
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "control.token" {
		t.Fatal("client created settings or directories")
	}
}
