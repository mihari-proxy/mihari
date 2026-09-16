package diagnostics

import (
	"context"
	"log/slog"
)

// Owner publishes occurrences to process history and an optional file outlet.
// It owns neither a goroutine nor the lifetime of the borrowed file outlet.
type Owner struct {
	history *History
	file    Reporter
}

// NewOwner connects an independent in-memory history to an optional reporter.
func NewOwner(history *History, file Reporter) *Owner {
	return &Owner{history: history, file: file}
}

// Report synchronously publishes a diagnostic at its executing owner boundary.
func (o *Owner) Report(ctx context.Context, record Record) {
	if record.Err != nil && !NormalCancellation(ctx, record.Err) && (record.Level >= slog.LevelInfo || record.receipt != nil) {
		snapshot, existing := Snapshot(record.Err)
		if !existing {
			snapshot = Describe(ctx, record)
		}
		if o.history != nil && snapshot.ID == "" {
			snapshot = o.history.Add(snapshot)
		}
		if record.receipt != nil {
			*record.receipt = snapshot
		}
		record.Snapshot = &snapshot
	}
	if o.file != nil {
		o.file(ctx, record)
	}
}
