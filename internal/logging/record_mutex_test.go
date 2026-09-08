package logging

import (
	"context"
	"errors"
	"testing"
)

func TestRuntime_RecordGateCancellationDoesNotAcquireLater(t *testing.T) {
	runtime := &Runtime{rotator: &RotatingWriter{}}
	leave := runtime.EnterRecordMutex()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := runtime.EnterRecordMutexContext(ctx); !errors.Is(err, context.Canceled) || release != nil {
		leave()
		t.Fatalf("canceled acquisition: release present=%v error=%v", release != nil, err)
	}
	leave()
	// A canceled waiter must never acquire the writer gate after the holder exits.
	release, err := runtime.EnterRecordMutexContext(context.Background())
	if err != nil || release == nil {
		t.Fatalf("gate unavailable after canceled waiter: %v", err)
	}
	release()
}

func TestRuntime_RecordGateRejectsCanceledContextWhenFree(t *testing.T) {
	runtime := &Runtime{rotator: &RotatingWriter{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := runtime.EnterRecordMutexContext(ctx); !errors.Is(err, context.Canceled) || release != nil {
		t.Fatalf("free gate accepted canceled acquisition: %v", err)
	}
}

func TestRuntime_RecordGateSharesWriterOwnership(t *testing.T) {
	runtime := &Runtime{rotator: &RotatingWriter{}}
	release, err := runtime.EnterRecordMutexContext(context.Background())
	if err != nil || release == nil {
		t.Fatalf("acquire snapshot gate: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		leave, err := runtime.EnterRecordMutexContext(ctx)
		if leave != nil {
			leave()
		}
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		release()
		t.Fatalf("second snapshot acquired held gate: %v", err)
	}
	release()
	leave := runtime.EnterRecordMutex()
	leave()
}
