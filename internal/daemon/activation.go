package daemon

import (
	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// BusinessMutationAllowed reports whether a persistent install phase may run
// Manager mutations, background refresh, or core start. Validation mode never
// allows business writes. An empty phase means no Unix install journal.
func BusinessMutationAllowed(phase string, validationMode bool) bool {
	if validationMode {
		return false
	}
	switch phase {
	case "", app.InstallPhaseActivationCommitted, app.InstallPhaseComplete:
		return true
	default:
		return false
	}
}

// CheckOrdinaryDaemonStart is the no-lock journal gate used by a normal daemon.
func CheckOrdinaryDaemonStart(journal app.InstallJournal, present bool) error {
	if !present {
		return nil
	}
	return app.CheckDaemonInstallJournal(journal)
}

// BootstrapDecision is the foreground Unix start classification. T18 wires
// default system mode; this task only exposes the decision.
type BootstrapDecision struct {
	AllowDaemon     bool
	RecoverRequired bool
	RunValidation   bool
	CreateData      bool
	MigrateRequired bool
	TargetAuthority bool
	OldInit         bool
	ActivationPhase string
}

// DecideForegroundBootstrap classifies root green-field, pending recover,
// activated target start, and non-root old initialization. It does not enable
// default system mode.
func DecideForegroundBootstrap(journal app.InstallJournal, present, sourcePresent, root, system bool) (BootstrapDecision, error) {
	if !root {
		return BootstrapDecision{AllowDaemon: true, OldInit: true}, nil
	}
	if present {
		if app.InstallPending(journal) {
			return BootstrapDecision{RecoverRequired: true, ActivationPhase: journal.Phase}, nil
		}
		switch journal.Phase {
		case app.InstallPhaseActivationCommitted, app.InstallPhaseComplete:
			return BootstrapDecision{
				AllowDaemon:     true,
				TargetAuthority: journal.RecoveryAuthority == app.InstallAuthorityTarget || journal.Phase == app.InstallPhaseComplete,
				ActivationPhase: journal.Phase,
			}, nil
		default:
			return BootstrapDecision{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "install recovery required"}
		}
	}
	if sourcePresent {
		return BootstrapDecision{MigrateRequired: true}, nil
	}
	return BootstrapDecision{CreateData: true, RunValidation: true}, nil
}

func activationRefused() error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: "install activation is required"}
}
