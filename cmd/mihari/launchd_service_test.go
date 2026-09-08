package main

import (
	"context"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestLaunchdService_RequiresProcessGroupLeaderBeforeStartup(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pid, group int
		err        error
	}{
		{"different-group", 42, 40, nil},
		{"unknown-group", 42, 0, errors.New("getpgid failed")},
		{"invalid-pid", 0, 0, nil},
		{"reserved-pid", 1, 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := runLaunchdServiceWithGroup(context.Background(), tc.pid, func(int) (int, error) { return tc.group, tc.err }, func(context.Context, bool) error { calls++; return nil })
			var apiError protocol.APIError
			if !errors.As(err, &apiError) || apiError.Code != protocol.CodeInvalidState || calls != 0 {
				t.Fatalf("unsafe group reached startup: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestLaunchdService_GroupLeaderPropagatesModeAndGateResult(t *testing.T) {
	ctx := context.Background()
	want := errors.New("install activation gate refused")
	calls := 0
	err := runLaunchdServiceWithGroup(ctx, 42, func(pid int) (int, error) {
		if pid != 42 {
			t.Fatalf("queried pid=%d", pid)
		}
		return 42, nil
	}, func(got context.Context, share bool) error {
		calls++
		if got != ctx || !share {
			t.Fatal("launchd lost context or explicit shared group mode")
		}
		return want
	})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("startup calls=%d err=%v", calls, err)
	}
}

func TestLaunchdService_CanceledBeforeStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := runLaunchdServiceWithGroup(ctx, 42, func(int) (int, error) { return 42, nil }, func(context.Context, bool) error { calls++; return nil })
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("canceled startup calls=%d err=%v", calls, err)
	}
}
