package runtime

import (
	"context"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/state"
)

// LoggingRuntime is the daemon-owned file logging configuration boundary.
type LoggingRuntime interface {
	Apply(context.Context, logging.Config)
	Config() logging.Config
	Dir() string
}

// LoggingUpdate is a partial file logging configuration mutation.
type LoggingUpdate struct {
	Level     *string
	MaxSizeMB *int64
	MaxFiles  *int64
}

// LoggingStatus returns the complete effective file logging configuration.
func (m *Manager) LoggingStatus(ctx context.Context) (protocol.LoggingStatus, error) {
	if m.logging == nil {
		return protocol.LoggingStatus{}, loggingUnavailable()
	}
	if err := m.lockMaintenance(ctx); err != nil {
		return protocol.LoggingStatus{}, err
	}
	defer m.unlock()
	return m.loggingStatusLocked(m.settingsSnapshot().EffectiveLogging(), m.store.Load().Revision), nil
}

// UpdateLogging atomically persists and applies a partial file logging update.
func (m *Manager) UpdateLogging(ctx context.Context, operation Operation, update LoggingUpdate) (protocol.LoggingStatus, error) {
	if m.logging == nil {
		return protocol.LoggingStatus{}, loggingUnavailable()
	}
	if err := validateLoggingUpdate(operation, update); err != nil {
		return protocol.LoggingStatus{}, err
	}
	result, err := m.doOperation(ctx, "logging:"+operation.ID, func(ctx context.Context) (any, error) {
		candidate, runtimeCandidate, err := m.prepareLoggingUpdate(ctx, operation, update)
		if err != nil {
			return nil, err
		}
		// prepareLoggingUpdate returns with mutation ownership, including stopped changes.
		defer m.unlock()
		defer func() { collectWarning(ctx, "logging", "candidate.cleanup.failed", runtimeCandidate.cleanup()) }()
		if runtimeCandidate.path != "" {
			candidate, err = m.commitLoggingCore(ctx, candidate, runtimeCandidate)
			if err != nil {
				return nil, err
			}
		} else {
			if _, err := m.saveSettingsCandidate(ctx, candidate); err != nil {
				return nil, err
			}
		}
		m.publishSettings(candidate)
		afterLogging := candidate.after.EffectiveLogging()
		if runtimeCandidate.path != "" {
			m.loggingUnsaved = false
			m.loggingObservation = loggingObservation{level: afterLogging.Level, state: "applied"}
		}
		if candidate.before.EffectiveLogging() != afterLogging {
			cfg, err := loggingConfig(afterLogging)
			if err != nil {
				return nil, err
			} // Validated before any side effect.
			m.logging.Apply(ctx, cfg)
		}

		committed, err := m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{
			ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision,
		}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			return snapshot, nil
		})
		if err != nil {
			return nil, err
		}
		return m.loggingStatusLocked(afterLogging, committed.Revision), nil
	})
	if err != nil {
		return protocol.LoggingStatus{}, err
	}
	return result.(protocol.LoggingStatus), nil
}

func validateLoggingUpdate(operation Operation, update LoggingUpdate) error {
	if operation.ID == "" {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "operation ID is required"}
	}
	if update.Level == nil && update.MaxSizeMB == nil && update.MaxFiles == nil {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "logging update is empty"}
	}
	if update.Level != nil && !validLoggingLevel(*update.Level) {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid logging level"}
	}
	if update.MaxSizeMB != nil && (*update.MaxSizeMB < 1 || *update.MaxSizeMB > 100) {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid logging max size"}
	}
	if update.MaxFiles != nil && (*update.MaxFiles < 1 || *update.MaxFiles > 10) {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid logging max files"}
	}
	return nil
}

func validLoggingLevel(level string) bool {
	return config.ActiveLoggingLevel(level)
}

func loggingConfig(settings config.LoggingSettings) (logging.Config, error) {
	if _, err := logging.ParseLevel(settings.Level); err != nil {
		return logging.Config{}, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid logging level"}, err)
	}
	cfg, err := logging.ConfigFromFields(settings.Level, settings.MaxSizeMB, settings.MaxFiles)
	if err != nil {
		return logging.Config{}, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid logging limits"}, err)
	}
	return cfg, nil
}

func (m *Manager) loggingStatusLocked(settings config.LoggingSettings, revision uint64) protocol.LoggingStatus {
	observed := m.loggingObservation
	if m.loggingCoreStopped() {
		observed = loggingObservation{state: "pending", message: "Saved; waiting for the core to start"}
	} else if observed.state == "" || m.mutationDegraded.Load() {
		observed = loggingObservation{state: "unknown", message: "Core logging state is unavailable"}
	}
	return protocol.LoggingStatus{
		Schema: "mihari/v1", Revision: revision, Level: settings.Level,
		MaxSizeMB: settings.MaxSizeMB, MaxFiles: settings.MaxFiles, Dir: m.logging.Dir(),
		CoreLevel: observed.level, SyncState: observed.state, SyncMessage: observed.message,
	}
}

func loggingUnavailable() error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: "logging runtime is unavailable"}
}
