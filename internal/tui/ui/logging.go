package ui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"log/slog"
)

// LoggingSyncMsg carries root-gated logging availability and state to pages.
type LoggingSyncMsg struct {
	Epoch     uint64
	Status    protocol.LoggingStatus
	Available bool
}

// LoggingObservedMsg carries an epoch-tagged GET/PATCH result to the root gate.
type LoggingObservedMsg struct {
	Epoch     uint64
	Status    protocol.LoggingStatus
	Operation logging.OperationMetadata
}

// LocalTaskDiagnostics borrows the output and ID source for existing TUI tasks.
// It owns no resources, contexts, or execution state.
type LocalTaskDiagnostics struct {
	Reporter diagnostics.Reporter
	NewID    func() (string, error)
}

// Context reuses metadata already bound for this task, including a failed ID
// generation. Unnamed incoming IDs are also accepted from an initiating owner.
func (d LocalTaskDiagnostics) Context(ctx context.Context, name string) context.Context {
	operation, bound := logging.OperationFromContext(ctx)
	if bound && (operation.Name == name || operation.Name == "") {
		operation.Name = name
		return logging.WithOperation(ctx, operation)
	}
	return d.NewContext(ctx, name)
}

// NewContext starts a distinct diagnostic identity on the existing lifecycle.
// Failure to create an ID is reported without making it a task precondition.
func (d LocalTaskDiagnostics) NewContext(ctx context.Context, name string) context.Context {
	newID := d.NewID
	if newID == nil {
		newID = func() (string, error) {
			var raw [16]byte
			if _, err := rand.Read(raw[:]); err != nil {
				return "", err
			}
			return hex.EncodeToString(raw[:]), nil
		}
	}
	operation := logging.OperationMetadata{Name: name}
	id, err := newID()
	if err == nil && id != "" {
		operation.ID = id
	}
	ctx = logging.WithOperation(ctx, operation)
	if operation.ID == "" && d.Reporter != nil {
		d.Reporter(ctx, diagnostics.Record{Component: "tui", Event: "local_task.id_generation_failed", Level: slog.LevelWarn, Err: errors.New("diagnostic identity unavailable")})
	}
	return ctx
}

// ReportFailure records an unreported final task failure at its owner boundary.
func (d LocalTaskDiagnostics) ReportFailure(ctx context.Context, event string, err error) error {
	if d.Reporter == nil || diagnostics.AlreadyReported(err) {
		return err
	}
	level, report := diagnostics.FailureLevel(ctx, err)
	if !report {
		return err
	}
	d.Reporter(ctx, diagnostics.Record{Component: "tui", Event: event, Level: level, Err: err})
	return diagnostics.MarkReported(err)
}
