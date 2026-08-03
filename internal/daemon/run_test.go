package daemon

import (
	"context"
	"testing"
	"time"

	transporttest "github.com/LeeShunEE/mihari/internal/control/transport/testutil"
)

func TestRunStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	endpoint := transporttest.Endpoint(t)
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Endpoint: endpoint,
			Token:    "token",
			Version:  "dev",
			Ready:    ready,
		})
	}()

	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not become ready")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not stop")
	}
}
