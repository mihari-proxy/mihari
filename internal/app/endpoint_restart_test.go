package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/buildinfo"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type endpointRestartFake struct {
	status     protocol.Status
	ports      protocol.OnboardingStatus
	pid        int
	pidErr     error
	restarts   int
	restartErr error
	onRestart  func()
	statusErr  error
	core       protocol.CoreStatus
	onCoreRead func()
}

func (f *endpointRestartFake) Status(context.Context) (protocol.Status, error) {
	return f.status, f.statusErr
}
func (f *endpointRestartFake) Onboarding(context.Context) (protocol.OnboardingStatus, error) {
	return f.ports, nil
}

func (f *endpointRestartFake) Core(context.Context) (protocol.CoreStatus, error) {
	if f.onCoreRead != nil {
		f.onCoreRead()
	}
	return f.core, nil
}
func (f *endpointRestartFake) ManagedDaemonPID(context.Context) (int, error) { return f.pid, f.pidErr }
func (f *endpointRestartFake) Restart() error {
	f.restarts++
	if f.onRestart != nil {
		f.onRestart()
	}
	return f.restartErr
}

func endpointRestartFixture() (*endpointRestartFake, EndpointRestartOptions) {
	f := &endpointRestartFake{
		status: protocol.Status{DaemonVersion: buildinfo.Version, PID: 42, StartedAt: time.Unix(100, 0), Health: "ok"}, pid: 42,
		ports: protocol.OnboardingStatus{MixedAddr: "127.0.0.1:19190", ControllerAddr: "127.0.0.1:9090", WebAddr: "127.0.0.1:9191", RestartRequired: true},
	}
	return f, EndpointRestartOptions{Client: f, Service: f, Elevated: func() bool { return true }}
}

func TestRestartSavedEndpoints_RequiresMatchingClientVersion(t *testing.T) {
	for _, phase := range []string{"before restart", "after restart", "missing version", "different prerelease case"} {
		t.Run(phase, func(t *testing.T) {
			f, opts := endpointRestartFixture()
			target := f.ports
			wantRestarts := 0
			switch phase {
			case "after restart":
				wantRestarts = 1
				f.onRestart = func() { f.status.PID++; f.status.DaemonVersion = "v0.0.1-other"; f.ports.RestartRequired = false }
			case "missing version":
				f.status.DaemonVersion = ""
			case "different prerelease case":
				opts.CurrentVersion = "v1.0.0-dev.A"
				f.status.DaemonVersion = "v1.0.0-dev.a"
			default:
				f.status.DaemonVersion = "v0.0.1-other"
			}
			opts.Wait = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
			_, err := RestartSavedEndpoints(context.Background(), target, opts)
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState || f.restarts != wantRestarts || !sameEndpointTargets(f.ports, target) {
				t.Fatalf("mismatched daemon accepted: restarts=%d err=%v", f.restarts, err)
			}
		})
	}
}

func TestRestartSavedEndpoints_CoreConfirmationRechecksEndpoints(t *testing.T) {
	f, opts := endpointRestartFixture()
	f.status.Capabilities = []string{protocol.CapabilityCore}
	f.core = protocol.CoreStatus{Status: "running", LocalReady: true}
	target := f.ports
	f.onRestart = func() { f.status.PID++; f.ports.RestartRequired = false }
	f.onCoreRead = func() {
		if f.restarts > 0 {
			f.ports.MixedAddr = "127.0.0.1:29190"
			f.ports.RestartRequired = true
		}
	}
	opts.Wait = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
	_, err := RestartSavedEndpoints(context.Background(), target, opts)
	if err == nil || f.restarts != 1 || f.ports.MixedAddr != "127.0.0.1:29190" {
		t.Fatalf("concurrent endpoint change was not preserved: restarts=%d err=%v", f.restarts, err)
	}
}

func TestRestartSavedEndpoints_VerifiesNewDaemonAndSavedPorts(t *testing.T) {
	f, opts := endpointRestartFixture()
	want := f.ports
	f.onRestart = func() { f.status.PID++; f.status.StartedAt = time.Unix(101, 0); f.ports.RestartRequired = false }
	got, err := RestartSavedEndpoints(context.Background(), want, opts)
	if err != nil || f.restarts != 1 || got.Status.PID != 43 || got.Ports.RestartRequired {
		t.Fatalf("result=%+v restarts=%d err=%v", got, f.restarts, err)
	}
}

func TestRestartSavedEndpoints_UsesRunningClientVersion(t *testing.T) {
	f, opts := endpointRestartFixture()
	opts.CurrentVersion = "v9.9.9-dev.2"
	f.status.DaemonVersion = opts.CurrentVersion
	f.ports.RestartRequired = false
	if _, err := RestartSavedEndpoints(context.Background(), f.ports, opts); err != nil {
		t.Fatal(err)
	}
}

func TestRestartSavedEndpoints_AlreadyAppliedDoesNotRestart(t *testing.T) {
	f, opts := endpointRestartFixture()
	want := f.ports
	f.ports.RestartRequired = false
	opts.Elevated = func() bool { return false }
	_, err := RestartSavedEndpoints(context.Background(), want, opts)
	if err != nil || f.restarts != 0 {
		t.Fatalf("restarts=%d err=%v", f.restarts, err)
	}
}

func TestRestartSavedEndpoints_RejectsUnsafeRestart(t *testing.T) {
	for _, condition := range []string{"permission", "foreground daemon", "service probe error", "foreign daemon", "changed ports", "cancelled", "unreachable"} {
		t.Run(condition, func(t *testing.T) {
			f, opts := endpointRestartFixture()
			want := f.ports
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch condition {
			case "permission":
				opts.Elevated = func() bool { return false }
			case "foreground daemon":
				opts.Service = nil
			case "service probe error":
				f.pidErr = errors.New("service inspection failed")
			case "foreign daemon":
				f.pid = 99
			case "changed ports":
				f.ports.MixedAddr = "127.0.0.1:29190"
			case "cancelled":
				cancel()
			case "unreachable":
				f.statusErr = errors.New("pipe unavailable")
			}
			_, err := RestartSavedEndpoints(ctx, want, opts)
			if err == nil || f.restarts != 0 || !f.ports.RestartRequired {
				t.Fatalf("ports=%+v restarts=%d err=%v", f.ports, f.restarts, err)
			}
			var api protocol.APIError
			switch condition {
			case "permission", "foreground daemon", "foreign daemon", "changed ports":
				code := protocol.CodeInvalidState
				if condition == "permission" {
					code = protocol.CodePermissionDenied
				}
				if condition == "changed ports" {
					code = protocol.CodeRevisionConflict
				}
				if !errors.As(err, &api) || api.Code != code {
					t.Fatalf("expected %s, got %v", code, err)
				}
			case "service probe error":
				if !errors.Is(err, f.pidErr) {
					t.Fatalf("lost inspection failure: %v", err)
				}
			case "cancelled":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
			case "unreachable":
				if !errors.Is(err, f.statusErr) {
					t.Fatalf("lost connection failure: %v", err)
				}
			}
			if !sameEndpointTargets(f.ports, want) && condition != "changed ports" {
				t.Fatal("failed application changed saved ports")
			}
		})
	}
}

func TestRestartSavedEndpoints_RestartFailurePreservesCauseAndSavedPorts(t *testing.T) {
	f, opts := endpointRestartFixture()
	want := f.ports
	f.restartErr = errors.New("service restart failed")
	_, err := RestartSavedEndpoints(context.Background(), want, opts)
	if !errors.Is(err, f.restartErr) || f.restarts != 1 || !reflect.DeepEqual(f.ports, want) {
		t.Fatalf("ports=%+v restarts=%d err=%v", f.ports, f.restarts, err)
	}
}

func TestRestartSavedEndpoints_WaitsForActualApplication(t *testing.T) {
	for _, condition := range []string{"old daemon", "pending restart", "degraded", "changed ports", "reconnect"} {
		t.Run(condition, func(t *testing.T) {
			f, opts := endpointRestartFixture()
			want := f.ports
			f.onRestart = func() {
				f.status.PID++
				f.status.StartedAt = time.Unix(101, 0)
				f.ports.RestartRequired = false
				switch condition {
				case "old daemon":
					f.status.PID = 42
					f.status.StartedAt = time.Unix(100, 0)
				case "pending restart":
					f.ports.RestartRequired = true
				case "degraded":
					f.status.Health = "degraded"
				case "changed ports":
					f.ports.WebAddr = "127.0.0.1:29191"
				case "reconnect":
					f.statusErr = errors.New("reconnecting")
				}
			}
			waits := 0
			opts.Wait = func(context.Context, time.Duration) error {
				waits++
				f.status.PID = 43
				f.status.StartedAt = time.Unix(101, 0)
				f.status.Health = "ok"
				f.statusErr = nil
				f.ports = want
				f.ports.RestartRequired = false
				return nil
			}
			got, err := RestartSavedEndpoints(context.Background(), want, opts)
			if err != nil || waits != 1 || f.restarts != 1 || got.Status.PID != 43 {
				t.Fatalf("result=%+v waits=%d restarts=%d err=%v", got, waits, f.restarts, err)
			}
		})
	}
}

func TestRestartSavedEndpoints_TimeoutDoesNotRepeatRestart(t *testing.T) {
	f, opts := endpointRestartFixture()
	opts.Wait = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
	_, err := RestartSavedEndpoints(context.Background(), f.ports, opts)
	if !errors.Is(err, context.DeadlineExceeded) || f.restarts != 1 {
		t.Fatalf("restarts=%d err=%v", f.restarts, err)
	}
}

func TestRestartSavedEndpoints_WaitsForRunningCore(t *testing.T) {
	f, opts := endpointRestartFixture()
	f.status.Capabilities = []string{protocol.CapabilityCore}
	f.core = protocol.CoreStatus{Status: "running", LocalReady: true}
	f.onRestart = func() { f.status.PID++; f.ports.RestartRequired = false; f.core.Status = "starting" }
	waits := 0
	opts.Wait = func(context.Context, time.Duration) error { waits++; f.core.Status = "running"; return nil }
	_, err := RestartSavedEndpoints(context.Background(), f.ports, opts)
	if err != nil || waits != 1 || f.restarts != 1 {
		t.Fatalf("waits=%d restarts=%d err=%v", waits, f.restarts, err)
	}
}

func TestRestartSavedEndpoints_AlreadyRestartedWaitsWithoutRestartingAgain(t *testing.T) {
	f, opts := endpointRestartFixture()
	f.status.Capabilities = []string{protocol.CapabilityCore}
	f.core = protocol.CoreStatus{Status: "starting", LocalReady: true}
	f.ports.RestartRequired = false
	opts.Elevated = func() bool { return false }
	waits := 0
	opts.Wait = func(context.Context, time.Duration) error { waits++; f.core.Status = "running"; return nil }
	_, err := RestartSavedEndpoints(context.Background(), f.ports, opts)
	if err != nil || f.restarts != 0 || waits != 1 {
		t.Fatalf("restarts=%d waits=%d err=%v", f.restarts, waits, err)
	}
}

func TestRestartSavedEndpoints_RecoveryWaitsForNewCore(t *testing.T) {
	f, opts := endpointRestartFixture()
	f.status.Health = "degraded"
	f.onRestart = func() {
		f.status.PID++
		f.status.Health = "ok"
		f.ports.RestartRequired = false
		f.status.Capabilities = []string{protocol.CapabilityCore}
		f.core = protocol.CoreStatus{Status: "starting", LocalReady: true}
	}
	waits := 0
	opts.Wait = func(context.Context, time.Duration) error { waits++; f.core.Status = "running"; return nil }
	_, err := RestartSavedEndpoints(context.Background(), f.ports, opts)
	if err != nil || waits != 1 || f.restarts != 1 {
		t.Fatalf("waits=%d restarts=%d err=%v", waits, f.restarts, err)
	}
}

func TestRestartSavedEndpoints_UnknownCoreIsNotConfirmedMissing(t *testing.T) {
	f, opts := endpointRestartFixture()
	f.onRestart = func() {
		f.status.PID++
		f.ports.RestartRequired = false
		f.status.Capabilities = []string{protocol.CapabilityCore}
		f.core = protocol.CoreStatus{Status: "unknown"}
	}
	waits := 0
	opts.Wait = func(context.Context, time.Duration) error {
		waits++
		f.core = protocol.CoreStatus{Status: "missing"}
		return nil
	}
	_, err := RestartSavedEndpoints(context.Background(), f.ports, opts)
	if err != nil || waits != 1 {
		t.Fatalf("waits=%d err=%v", waits, err)
	}
}
