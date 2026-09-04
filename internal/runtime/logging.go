package runtime

import (
	"context"
	"log/slog"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
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
	result, err := m.doOperation(ctx, "logging:"+operation.ID, func() (any, error) {
		if err := m.lockMutation(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err := m.checkIfRevision(operation.IfRevision); err != nil {
			return nil, err
		}

		candidate, err := m.prepareSettings(func(settings *config.Settings) error {
			effective := settings.EffectiveLogging()
			if update.Level != nil {
				effective.Level = *update.Level
			}
			if update.MaxSizeMB != nil {
				effective.MaxSizeMB = *update.MaxSizeMB
			}
			if update.MaxFiles != nil {
				effective.MaxFiles = *update.MaxFiles
			}
			settings.SetLogging(effective)
			return nil
		})
		if err != nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid logging configuration"}
		}
		beforeLogging := candidate.before.EffectiveLogging()
		afterLogging := candidate.after.EffectiveLogging()
		cfg, err := loggingConfig(afterLogging)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if beforeLogging != afterLogging {
			if _, err := m.saveSettingsCandidate(candidate); err != nil {
				return nil, err
			}
			m.publishSettings(candidate)
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
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

func loggingConfig(settings config.LoggingSettings) (logging.Config, error) {
	var level slog.Level
	switch settings.Level {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return logging.Config{}, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid logging level"}
	}
	if settings.MaxSizeMB < 1 || settings.MaxSizeMB > 100 || settings.MaxFiles < 1 || settings.MaxFiles > 10 {
		return logging.Config{}, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid logging limits"}
	}
	return logging.Config{
		Level: level, MaxSizeBytes: settings.MaxSizeMB * 1024 * 1024, MaxFiles: int(settings.MaxFiles),
	}, nil
}

func (m *Manager) loggingStatusLocked(settings config.LoggingSettings, revision uint64) protocol.LoggingStatus {
	return protocol.LoggingStatus{
		Schema: "mihari/v1", Revision: revision, Level: settings.Level,
		MaxSizeMB: settings.MaxSizeMB, MaxFiles: settings.MaxFiles, Dir: m.logging.Dir(),
	}
}

func loggingUnavailable() error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: "logging runtime is unavailable"}
}
