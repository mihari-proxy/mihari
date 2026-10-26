//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/service"
)

type endpointServiceProbe struct {
	service.RecoveryAdapter
	definition service.Definition
	err        error
	calls      int
}

func (p *endpointServiceProbe) Probe(context.Context) (service.Definition, error) {
	p.calls++
	return p.definition, p.err
}

func TestUnixInstaller_ManagedDaemonPIDReadOnly(t *testing.T) {
	for _, running := range []bool{true, false} {
		probe := &endpointServiceProbe{definition: service.Definition{Status: service.StatusRunning, Running: running, Process: service.ProcessIdentity{PID: 42}}}
		i := &UnixInstaller{adapter: func(service.ActionHook) service.RecoveryAdapter { return probe }}
		pid, err := i.ManagedDaemonPID(context.Background())
		want := 0
		if running {
			want = 42
		}
		if err != nil || pid != want || probe.calls != 1 {
			t.Fatalf("pid=%d calls=%d err=%v", pid, probe.calls, err)
		}
	}
}

func TestUnixInstaller_ManagedDaemonPIDErrorAndCancellation(t *testing.T) {
	probe := &endpointServiceProbe{err: errors.New("service inspection failed")}
	i := &UnixInstaller{adapter: func(service.ActionHook) service.RecoveryAdapter { return probe }}
	_, err := i.ManagedDaemonPID(context.Background())
	if !errors.Is(err, probe.err) {
		t.Fatalf("err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = i.ManagedDaemonPID(ctx)
	if !errors.Is(err, context.Canceled) || probe.calls != 1 {
		t.Fatalf("calls=%d err=%v", probe.calls, err)
	}
}
