package service

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinSignalIdentity_ExitAfterLookupIsStopped(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"exited", unix.ESRCH},
		{"permission denied", unix.EPERM},
		{"delivered", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookedUp, signaled := false, false
			id := ProcessIdentity{PID: 42, BootID: "boot", StartUnix: 123}
			err := signalDarwinIdentity(context.Background(), id, "TERM", func(_ context.Context, got ProcessIdentity) (bool, error) {
				if got != id {
					t.Fatal("looked up a different process identity")
				}
				lookedUp = true
				return true, nil
			}, func(pid int, sig unix.Signal) error {
				if !lookedUp || pid != id.PID || sig != unix.SIGTERM {
					t.Fatal("signal sent without a verified identity")
				}
				signaled = true
				return tc.err
			})
			want := tc.err
			if errors.Is(want, unix.ESRCH) {
				want = nil
			}
			if !signaled || !errors.Is(err, want) {
				t.Fatalf("signal result=%v want=%v", err, want)
			}
		})
	}
}
