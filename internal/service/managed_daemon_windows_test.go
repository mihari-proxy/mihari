package service

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/windows/svc"
)

func TestManagedDaemonPID_RequiresRunningService(t *testing.T) {
	for _, state := range []svc.State{svc.Running, svc.Stopped, svc.StartPending, svc.StopPending} {
		pid, err := managedDaemonPID(context.Background(), func() (svc.Status, error) {
			return svc.Status{State: state, ProcessId: 1234}, nil
		})
		want := 0
		if state == svc.Running {
			want = 1234
		}
		if err != nil || pid != want {
			t.Fatalf("state=%v pid=%d err=%v", state, pid, err)
		}
	}
}

func TestManagedDaemonPID_PreservesQueryFailure(t *testing.T) {
	want := errors.New("SCM query failed")
	_, err := managedDaemonPID(context.Background(), func() (svc.Status, error) { return svc.Status{}, want })
	if !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}

func TestManagedDaemonPID_CancellationSkipsQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := managedDaemonPID(ctx, func() (svc.Status, error) { t.Fatal("query after cancellation"); return svc.Status{}, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
