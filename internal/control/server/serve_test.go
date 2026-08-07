package server

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/state"
)

// TestServeContextCancelToleratesShutdownTimeout is a regression guard for the
// flaky integration test TestControlPlaneLifecycleAndConcurrentStatus: Serve
// used to return http.Shutdown's error verbatim, so when in-flight connections
// prevented draining within the shutdown budget, daemon.Run surfaced a spurious
// "context deadline exceeded" and flaked the integration test under -race on
// slow CI. Graceful-shutdown timeout is acceptable on the cancellation path,
// so Serve must return nil unless a genuine (non-deadline) error occurs.
func TestServeContextCancelToleratesShutdownTimeout(t *testing.T) {
	store := state.NewStore(state.Snapshot{Version: "test", Health: "ok"})
	srv := New(Options{Token: "t", Store: store})
	// Short budget so the regression is observable quickly; the handler below
	// never drains on its own, so Shutdown is guaranteed to hit the deadline.
	srv.shutdownTimeout = 50 * time.Millisecond

	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	srv.http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(handlerStarted)
		<-releaseHandler
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ctx, listener) }()

	// Fire an in-flight request that blocks inside the handler, keeping the
	// connection active so Shutdown cannot drain and must time out.
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String() + "/")
		if err == nil {
			resp.Body.Close()
		}
	}()

	select {
	case <-handlerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request never reached the handler")
	}

	// Cancellation enters the graceful-shutdown path. The blocked handler
	// cannot drain, so http.Shutdown returns DeadlineExceeded after the budget.
	cancel()

	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve returned %v on context cancel; want nil (graceful-shutdown timeout must not surface as an error)", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after context cancel")
	}

	close(releaseHandler)
}
