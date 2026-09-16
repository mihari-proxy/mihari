package system

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/update"
	"strings"
	"testing"
)

// TestLocalTaskDiagnostics_PreparationAsyncResultsCarryDistinctValues keeps operation metadata and generation guards separate across overlapping preparations.
func TestLocalTaskDiagnostics_PreparationAsyncResultsCarryDistinctValues(t *testing.T) {
	m, _ := replacementFixture(t)
	first := m.startMihariPreparation()
	m.CancelMihariPreparation()
	second := m.startMihariPreparation()
	if first == nil || second == nil {
		t.Fatal("preparation missing")
	}
	b := firstSystemPageResult(t, second).(preparedMihariResultMsg)
	a := firstSystemPageResult(t, first).(preparedMihariResultMsg)
	if a.operation.ID == "" || b.operation.ID == "" || a.operation.ID == b.operation.ID || a.operation.Name != "self.prepare" || b.operation.Name != "self.prepare" {
		t.Fatalf("async preparation metadata missing/mixed: %+v %+v", a.operation, b.operation)
	}
	if a.generation == b.generation {
		t.Fatal("generation guard changed")
	}
}

type failedPreparationUpdater struct {
	replacementUpdater
	cause error
}

func (u *failedPreparationUpdater) Prepare(ctx context.Context, _, _, _ string) (update.PreparedUpdate, error) {
	return update.PreparedUpdate{}, errors.Join(ctx.Err(), u.cause)
}

func TestPreparationCancellation_RetainsCleanupFailureWithoutInventingCancelError(t *testing.T) {
	for _, cleanup := range []bool{false, true} {
		m, updater := replacementFixture(t)
		failed := &failedPreparationUpdater{replacementUpdater: *updater}
		if cleanup {
			failed.cause = errors.New("cleanup token=fixture-preparation")
		}
		m.selfUpdater = failed
		cmd := m.startMihariPreparation()
		m.CancelMihariPreparation()
		result := firstSystemPageResult(t, cmd).(preparedMihariResultMsg)
		failures := []error{result.Err()}
		if outcome, ok := any(result).(interface{ DiagnosticErrors() []error }); ok {
			failures = outcome.DiagnosticErrors()
		}
		if !cleanup && len(failures) != 0 {
			t.Fatal("owned preparation cancellation became an error record")
		}
		if cleanup && (len(failures) != 1 || !strings.Contains(failures[0].Error(), "fixture-preparation")) {
			t.Fatal("cleanup failure lost")
		}
		if !errors.Is(result.Err(), context.Canceled) {
			t.Fatal("business result lost cancellation")
		}
	}
}
