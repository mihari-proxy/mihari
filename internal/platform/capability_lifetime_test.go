package platform

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestCapabilityLifetime_CloseWaitsWithoutHoldingStateMutex(t *testing.T) {
	fs, err := NewPrivateFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	finish, err := fs.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- fs.Close() }()
	select {
	case <-fs.closing:
	case <-time.After(time.Second):
		finish()
		t.Fatal("Close did not signal pending shutdown")
	}
	fs.mu.Lock()
	closed := fs.closed
	fs.mu.Unlock()
	if !closed {
		finish()
		t.Fatal("Close did not record ownership")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fs.begin(ctx); !errors.Is(err, os.ErrClosed) {
		finish()
		t.Fatalf("new operation accepted during shutdown: %v", err)
	}
	select {
	case <-done:
		finish()
		t.Fatal("Close released an active capability")
	default:
	}
	finish()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not finish")
	}
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityLifetime_QueuedOperationCancels(t *testing.T) {
	var lifetime capabilityLifetime
	finish, err := lifetime.begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lifetime.begin(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation: %v", err)
	}
}
