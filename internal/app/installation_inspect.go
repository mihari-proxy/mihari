package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
)

var (
	ErrInstallationPermissionRequired = errors.New("installation permission required")
	ErrInstallationObservationUnknown = errors.New("installation observation unknown")
)

// InstallationSnapshot is one read-only observation of the control record and operation lock.
type InstallationSnapshot struct {
	Present         bool
	OperationLocked bool
	State           []byte
	Legacy          *InstallationManifest
}

// InstallationResourceObservation reports whether a manifest's resources still match.
type InstallationResourceObservation struct {
	Matches bool
}

// InstallationServiceObservation reports current service state, policy, and health.
type InstallationServiceObservation struct {
	State   string
	Enabled bool
	Ready   bool
}

// InstallationObserver exposes only read-only installation observations.
type InstallationObserver interface {
	Snapshot(context.Context) (InstallationSnapshot, error)
	VerifyManifest(context.Context, InstallationManifest) (InstallationResourceObservation, error)
	ObserveService(context.Context) (InstallationServiceObservation, error)
}

// InstallationManagerOptions configures installation use cases.
type InstallationManagerOptions struct {
	Observer InstallationObserver
	Backend  InstallationBackend
	Random   io.Reader
}

// InstallationManager owns installation status and repair use cases.
type InstallationManager struct {
	observer InstallationObserver
	backend  InstallationBackend
	random   io.Reader
	plansMu  sync.Mutex
	plans    map[string]authorizedInstallationPlan
}

// NewInstallationManager creates an installation manager.
func NewInstallationManager(options InstallationManagerOptions) *InstallationManager {
	return newInstallationManager(options)
}

// Inspect returns a redacted read-only installation status.
func (m *InstallationManager) Inspect(ctx context.Context) (InstallationStatus, error) {
	if err := ctx.Err(); err != nil {
		return InstallationStatus{}, err
	}
	if m == nil || m.observer == nil {
		return InstallationStatus{}, invalidInstallationInspection()
	}
	first, err := m.observer.Snapshot(ctx)
	if err != nil {
		if status, ok := installationObservationStatus(err, InstallationReasonRecordInvalid); ok {
			return status, nil
		}
		return InstallationStatus{}, err
	}

	status := InstallationStatus{Schema: InstallationStatusSchema, Kind: InstallationKindUnknown, ServiceState: InstallServiceUnknown, Reason: InstallationReasonRecordInvalid}
	var manifest *InstallationManifest
	legacy := false
	validSnapshot := true
	switch {
	case first.Present && (len(first.State) == 0 || first.Legacy != nil):
		validSnapshot = false
	case !first.Present && (len(first.State) != 0 || first.OperationLocked):
		validSnapshot = false
	case first.Present:
		state, decodeErr := DecodeInstallationState(bytes.NewReader(first.State))
		if decodeErr != nil {
			validSnapshot = false
			break
		}
		status.ID = state.ID
		if first.OperationLocked {
			status.Kind = InstallationKindInProgress
			status.Reason = InstallationReasonInstalling
		} else if state.State == InstallationStateApplying {
			status.Kind = InstallationKindInterrupted
			status.Reason = InstallationReasonOperationInterrupted
		} else if state.Target.Installed {
			status.Kind = InstallationKindInstalled
			status.Reason = ""
			manifest = &state.Target
		} else {
			status.Kind = InstallationKindNotInstalled
			status.Reason = ""
		}
	case first.Legacy != nil:
		if err := validateInstallationManifest(*first.Legacy, false); err != nil || !first.Legacy.Installed {
			validSnapshot = false
			break
		}
		status.Kind = InstallationKindInstalled
		status.Reason = InstallationReasonLegacyRecordAbsent
		manifest = first.Legacy
		legacy = true
	default:
		status.Kind = InstallationKindNotInstalled
		status.Reason = ""
	}

	if validSnapshot && manifest != nil {
		observation, verifyErr := m.observer.VerifyManifest(ctx, *manifest)
		if verifyErr != nil {
			if mapped, ok := installationObservationStatus(verifyErr, InstallationReasonResourceMismatch); ok {
				mapped.ID = status.ID
				status = mapped
				validSnapshot = false
			} else {
				return InstallationStatus{}, verifyErr
			}
		} else if !observation.Matches {
			status.Kind = InstallationKindUnknown
			status.Reason = InstallationReasonResourceMismatch
			status.ID = ""
			if first.Present {
				state, _ := DecodeInstallationState(bytes.NewReader(first.State))
				status.ID = state.ID
			}
			validSnapshot = false
		}
	}

	service, serviceErr := m.observer.ObserveService(ctx)
	serviceValid := serviceErr == nil && validInstallationServiceState(service.State)
	if serviceErr != nil {
		if mapped, ok := installationObservationStatus(serviceErr, InstallationReasonRecordInvalid); ok {
			if validSnapshot && status.Kind == InstallationKindNotInstalled {
				mapped.ID = status.ID
				status = mapped
				validSnapshot = false
			} else {
				status.ServiceState = InstallServiceUnknown
			}
		} else {
			return InstallationStatus{}, serviceErr
		}
	} else if !validInstallationServiceState(service.State) {
		status.ServiceState = InstallServiceUnknown
		if validSnapshot && status.Kind == InstallationKindNotInstalled {
			status.Kind = InstallationKindUnknown
			status.Reason = InstallationReasonRecordInvalid
			validSnapshot = false
		}
	} else {
		status.ServiceState = service.State
	}

	second, err := m.observer.Snapshot(ctx)
	if err != nil {
		if mapped, ok := installationObservationStatus(err, InstallationReasonRecordInvalid); ok {
			return mapped, nil
		}
		return InstallationStatus{}, err
	}
	if !sameInstallationSnapshot(first, second) {
		return InstallationStatus{Schema: InstallationStatusSchema, Kind: InstallationKindUnknown, ServiceState: InstallServiceUnknown, Reason: InstallationReasonRecordInvalid}, nil
	}
	if !validSnapshot {
		status.StartFailed = false
		return status, nil
	}
	if serviceValid && status.Kind == InstallationKindNotInstalled && service.State != InstallServiceNotInstalled {
		status.Kind = InstallationKindUnknown
		status.Reason = InstallationReasonRecordInvalid
	}
	if serviceValid && status.Kind == InstallationKindInstalled && !legacy {
		if service.State != InstallServiceUnknown && service.State != InstallServiceNotInstalled && manifest != nil && service.Enabled != manifest.Enabled {
			status.Reason = InstallationReasonServicePolicyMismatch
		} else if service.State == InstallServiceRunning && !service.Ready {
			status.Reason = InstallationReasonServiceNotReady
		}
	}
	status.StartFailed = false
	return status, nil
}

func installationObservationStatus(err error, unknownReason string) (InstallationStatus, bool) {
	switch {
	case errors.Is(err, ErrInstallationPermissionRequired):
		return InstallationStatus{Schema: InstallationStatusSchema, Kind: InstallationKindPermissionRequired, ServiceState: InstallServiceUnknown, Reason: InstallationReasonPermissionRequired}, true
	case errors.Is(err, ErrInstallationObservationUnknown):
		return InstallationStatus{Schema: InstallationStatusSchema, Kind: InstallationKindUnknown, ServiceState: InstallServiceUnknown, Reason: unknownReason}, true
	default:
		return InstallationStatus{}, false
	}
}

func sameInstallationSnapshot(left, right InstallationSnapshot) bool {
	return left.Present == right.Present && left.OperationLocked == right.OperationLocked && bytes.Equal(left.State, right.State) && reflect.DeepEqual(left.Legacy, right.Legacy)
}

func validInstallationServiceState(state string) bool {
	switch state {
	case InstallServiceRunning, InstallServiceStopped, InstallServiceNotInstalled, InstallServiceUnknown:
		return true
	default:
		return false
	}
}

func invalidInstallationInspection() error {
	return errors.New("installation observer is required")
}
