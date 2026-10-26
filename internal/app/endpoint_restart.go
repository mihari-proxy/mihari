package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mihari-proxy/mihari/internal/buildinfo"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

// EndpointRestartClient reads the selected daemon over authenticated local IPC.
type EndpointRestartClient interface {
	Status(context.Context) (protocol.Status, error)
	Onboarding(context.Context) (protocol.OnboardingStatus, error)
	Core(context.Context) (protocol.CoreStatus, error)
}

// EndpointRestartService identifies and restarts only the installed service.
type EndpointRestartService interface {
	ManagedDaemonPID(context.Context) (int, error)
	Restart() error
}

// EndpointRestartOptions supplies the service boundary and cancellable waiter.
type EndpointRestartOptions struct {
	CurrentVersion string
	Client         EndpointRestartClient
	Service        EndpointRestartService
	Elevated       func() bool
	Wait           func(context.Context, time.Duration) error
}

// EndpointRestartResult confirms the new daemon and its applied endpoint settings.
type EndpointRestartResult struct {
	Status protocol.Status
	Ports  protocol.OnboardingStatus
}

// RestartSavedEndpoints applies already committed ports without writing settings.
func RestartSavedEndpoints(ctx context.Context, target protocol.OnboardingStatus, opts EndpointRestartOptions) (EndpointRestartResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return EndpointRestartResult{}, err
	}
	if opts.Client == nil {
		return EndpointRestartResult{}, endpointRestartError("Local daemon connection is unavailable")
	}
	before, err := readEndpointRestartState(ctx, opts.Client)
	if err != nil {
		return EndpointRestartResult{}, err
	}
	version := opts.CurrentVersion
	if version == "" {
		version = buildinfo.Version
	}
	if err := endpointVersionError(before.Status, version); err != nil {
		return before, err
	}
	if !sameEndpointTargets(before.Ports, target) {
		return before, protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "Saved ports changed before the service restart; recheck the latest configuration"}
	}
	requireCore := false
	if slices.Contains(before.Status.Capabilities, protocol.CapabilityCore) {
		core, coreErr := opts.Client.Core(ctx)
		if coreErr != nil {
			return before, fmt.Errorf("read core readiness before endpoint restart: %w", coreErr)
		}
		requireCore = core.LocalReady || core.Status == "running" || core.Status == "starting"
	}
	needsRestart := before.Ports.RestartRequired || before.Status.Health != "ok"
	if needsRestart {
		if err := restartEndpointOwner(ctx, before.Status, opts); err != nil {
			return before, err
		}
	}
	wait := opts.Wait
	if wait == nil {
		wait = waitEndpointRestart
	}
	for {
		current, readErr := readEndpointRestartState(ctx, opts.Client)
		if readErr == nil {
			if err := endpointVersionError(current.Status, version); err != nil {
				return current, err
			}
		}
		if readErr == nil && current.Status.PID > 0 &&
			(!needsRestart || current.Status.PID != before.Status.PID || !current.Status.StartedAt.Equal(before.Status.StartedAt)) &&
			current.Status.Health == "ok" && !current.Ports.RestartRequired && sameEndpointTargets(current.Ports, target) {
			if !requireCore && !slices.Contains(current.Status.Capabilities, protocol.CapabilityCore) {
				return current, nil
			}
			core, coreErr := opts.Client.Core(ctx)
			coreOptional := !requireCore && !core.LocalReady && core.Status == "missing"
			if coreErr == nil && (core.Status == "running" || coreOptional) {
				confirmed, confirmErr := readEndpointRestartState(ctx, opts.Client)
				if confirmErr == nil {
					if err := endpointVersionError(confirmed.Status, version); err != nil {
						return confirmed, err
					}
				}
				if confirmErr == nil && confirmed.Status.PID == current.Status.PID && confirmed.Status.StartedAt.Equal(current.Status.StartedAt) &&
					confirmed.Status.Health == "ok" && !confirmed.Ports.RestartRequired && sameEndpointTargets(confirmed.Ports, target) {
					return confirmed, nil
				}
				coreErr = confirmErr
			}
			readErr = coreErr
		}
		if readErr == nil {
			readErr = endpointRestartError("Waiting for a new healthy Mihari daemon to apply the saved ports")
			if current.Status.Health == "degraded" && current.Status.LastError != "" {
				readErr = protocol.APIError{Code: protocol.CodeInvalidState, Message: "The restarted Mihari daemon is degraded", Details: map[string]any{"last_error": current.Status.LastError}}
			}
		}
		if err := wait(ctx, 200*time.Millisecond); err != nil {
			return before, diagnostics.Wrap(endpointRestartError("Ports are saved, but the daemon has not confirmed their application"), errors.Join(err, readErr))
		}
	}
}

func endpointVersionError(status protocol.Status, version string) error {
	if version == "" || status.DaemonVersion != version {
		return endpointRestartError(fmt.Sprintf("Ports are saved. Daemon version %q must match CLI version %q; synchronize the installed Mihari binary before applying ports", status.DaemonVersion, version))
	}
	return nil
}

func restartEndpointOwner(ctx context.Context, before protocol.Status, opts EndpointRestartOptions) error {
	if opts.Elevated == nil || !opts.Elevated() {
		return protocol.APIError{Code: protocol.CodePermissionDenied, Message: "Ports are saved. Run mihari service restart in an administrator/root terminal to apply them"}
	}
	if opts.Service == nil {
		return endpointRestartError("Ports are saved. Stop the foreground daemon and run mihari daemon again to apply them")
	}
	pid, err := opts.Service.ManagedDaemonPID(ctx)
	if err != nil {
		return fmt.Errorf("identify the installed Mihari service: %w", err)
	}
	if pid <= 0 || pid != before.PID {
		return endpointRestartError("Ports are saved, but the connected daemon is not the installed service. Stop that daemon and run mihari daemon again")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := opts.Service.Restart(); err != nil {
		return diagnostics.Wrap(endpointRestartError("Ports are saved, but the Mihari service could not be restarted"), err)
	}
	return nil
}

func sameEndpointTargets(a, b protocol.OnboardingStatus) bool {
	return a.MixedAddr == b.MixedAddr && a.ControllerAddr == b.ControllerAddr && a.WebAddr == b.WebAddr
}

func readEndpointRestartState(ctx context.Context, client EndpointRestartClient) (EndpointRestartResult, error) {
	before, err := client.Status(ctx)
	if err != nil {
		return EndpointRestartResult{}, fmt.Errorf("read daemon before checking saved ports: %w", err)
	}
	ports, err := client.Onboarding(ctx)
	if err != nil {
		return EndpointRestartResult{}, fmt.Errorf("read saved ports: %w", err)
	}
	after, err := client.Status(ctx)
	if err != nil {
		return EndpointRestartResult{}, fmt.Errorf("confirm daemon identity after checking saved ports: %w", err)
	}
	if before.PID != after.PID || !before.StartedAt.Equal(after.StartedAt) {
		return EndpointRestartResult{}, endpointRestartError("Daemon identity changed while checking saved ports")
	}
	return EndpointRestartResult{Status: after, Ports: ports}, nil
}

func endpointRestartError(message string) protocol.APIError {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: message}
}

func waitEndpointRestart(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
