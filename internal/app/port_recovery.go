package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/platform"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
)

// ManagedPortConflict identifies the narrow, recoverable startup failure while
// retaining the existing safe API error and JSON contract.
type ManagedPortConflict struct{ failure protocol.APIError }

func (e *ManagedPortConflict) Error() string { return e.failure.Message }
func (e *ManagedPortConflict) Unwrap() error { return e.failure }

// PortConflict marks only confirmed address-in-use errors.
func (e *ManagedPortConflict) PortConflict() bool { return true }

// PortRecovery exposes only onboarding reads and endpoint updates. Its Manager
// is never installed as the full control runtime or started as a business owner.
type PortRecovery struct{ manager *runtimeapi.Manager }

// NewPortRecovery builds the daemon-owned recovery surface after a confirmed
// port conflict, using the existing settings transaction and onboarding format.
func NewPortRecovery(paths platform.Paths, store *state.Store, cause error, reporter diagnostics.Reporter) (*PortRecovery, error) {
	var conflict *ManagedPortConflict
	if !errors.As(cause, &conflict) {
		return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "startup failure is not a port conflict"}
	}
	// Startup resource recovery may have committed newer settings before the
	// port probe failed. Never use the caller's pre-recovery snapshot to write.
	settings, err := config.Load(paths.Settings)
	if err != nil {
		return nil, fmt.Errorf("load port recovery settings: %w", err)
	}
	service, err := onboarding.Open(onboarding.Options{StatePath: paths.Onboarding, InitialSetupRequired: true})
	if err != nil {
		return nil, fmt.Errorf("open port recovery onboarding: %w", err)
	}
	manager := runtimeapi.New(runtimeapi.Options{Store: store, Settings: settings, SettingsPath: paths.Settings, Onboarding: service, DiagnosticReporter: reporter})
	return &PortRecovery{manager: manager}, nil
}

// OnboardingStatus reads saved endpoint values without claiming recovery is complete.
func (r *PortRecovery) OnboardingStatus(ctx context.Context) (onboarding.Snapshot, error) {
	return r.manager.OnboardingStatus(ctx)
}

// UpdateOnboarding only permits endpoint changes; completion requires a normal runtime.
func (r *PortRecovery) UpdateOnboarding(ctx context.Context, op runtimeapi.Operation, update onboarding.Update) (onboarding.Snapshot, error) {
	if update.Complete != nil {
		return onboarding.Snapshot{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "restart the daemon with valid ports before completing setup"}
	}
	return r.manager.UpdateOnboarding(ctx, op, update)
}
