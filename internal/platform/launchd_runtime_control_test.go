package platform

import (
	"context"
	"errors"
	"os"
	"testing"
)

type launchdRuntimeFake struct {
	state       []byte
	publication InstallPublication
	publishErr  error
	publishes   int
}

func (f *launchdRuntimeFake) readState(context.Context) ([]byte, error) {
	return append([]byte(nil), f.state...), nil
}
func (f *launchdRuntimeFake) publishState(_ context.Context, _ string, next []byte) (InstallPublication, error) {
	f.publishes++
	f.state = append([]byte(nil), next...)
	return f.publication, f.publishErr
}
func (f *launchdRuntimeFake) confirmState(context.Context, string) error { return nil }
func (f *launchdRuntimeFake) lockStartup(context.Context) (installControlLockPlatform, error) {
	return installControlFakeLock{}, nil
}
func (f *launchdRuntimeFake) close() error { return nil }

func TestLaunchdRuntimeControl_PublishRequiresOwnHeldGate(t *testing.T) {
	ctx := context.Background()
	firstFake := &launchdRuntimeFake{state: []byte(`{"generation":null}`), publication: InstallPublication{Published: true, Durable: true}}
	secondFake := &launchdRuntimeFake{state: append([]byte(nil), firstFake.state...), publication: firstFake.publication}
	first, second := newLaunchdRuntimeControl(firstFake), newLaunchdRuntimeControl(secondFake)
	gate, err := first.LockStartup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, gate.Close)
	_, digest, err := second.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = second.PublishState(ctx, digest, []byte(`{"generation":"next"}`)); !errors.Is(err, ErrInstallControlBusy) {
		t.Fatalf("foreign-gate publish error=%v", err)
	}
	if secondFake.publishes != 0 {
		t.Fatalf("backend publishes=%d", secondFake.publishes)
	}
}

func TestLaunchdRuntimeControl_PublishAdapterRejectsVisibleNondurableResult(t *testing.T) {
	ctx := context.Background()
	fake := &launchdRuntimeFake{state: []byte(`{"generation":null}`), publication: InstallPublication{Published: true}}
	control := newLaunchdRuntimeControl(fake)
	gate, err := control.LockStartup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer assertInstallTestClose(t, gate.Close)
	_, digest, err := control.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = control.Publish(ctx, digest, []byte(`{"generation":"next"}`)); !errors.Is(err, ErrLaunchdRuntimeNotDurable) {
		t.Fatalf("Publish error=%v", err)
	}
}

func TestLaunchdRuntimeControl_RejectsOversizeAndClosedCalls(t *testing.T) {
	ctx := context.Background()
	fake := &launchdRuntimeFake{state: make([]byte, launchdRuntimeStateMaxBytes+1)}
	control := newLaunchdRuntimeControl(fake)
	if _, _, err := control.Read(ctx); !errors.Is(err, ErrLaunchdRuntimeStateTooLarge) {
		t.Fatalf("Read error=%v", err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := control.LockStartup(ctx); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed LockStartup error=%v", err)
	}
	var nilControl *LaunchdRuntimeControl
	if _, _, err := nilControl.Read(ctx); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("nil Read error=%v", err)
	}
}
