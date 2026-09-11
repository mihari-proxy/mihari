package supervisor

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
)

type supervisorDiagnosticRecord struct {
	operation logging.OperationMetadata
	record    diagnostics.Record
}

type supervisorDiagnosticRecorder struct {
	mu      sync.Mutex
	records []supervisorDiagnosticRecord
	notify  chan struct{}
}

func newSupervisorDiagnosticRecorder() *supervisorDiagnosticRecorder {
	return &supervisorDiagnosticRecorder{notify: make(chan struct{}, 16)}
}

func (r *supervisorDiagnosticRecorder) report(ctx context.Context, record diagnostics.Record) {
	operation, _ := logging.OperationFromContext(ctx)
	r.mu.Lock()
	r.records = append(r.records, supervisorDiagnosticRecord{operation: operation, record: record})
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

func (r *supervisorDiagnosticRecorder) snapshot() []supervisorDiagnosticRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]supervisorDiagnosticRecord(nil), r.records...)
}

func (r *supervisorDiagnosticRecorder) next(t *testing.T) supervisorDiagnosticRecord {
	t.Helper()
	select {
	case <-r.notify:
		records := r.snapshot()
		return records[len(records)-1]
	case <-time.After(3 * time.Second):
		t.Fatal("diagnostic record was not reported")
		return supervisorDiagnosticRecord{}
	}
}

func (r *supervisorDiagnosticRecorder) waitForCount(t *testing.T, count int) []supervisorDiagnosticRecord {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		if records := r.snapshot(); len(records) >= count {
			return records
		}
		select {
		case <-r.notify:
		case <-deadline.C:
			t.Fatalf("diagnostic record count=%d, want at least %d", len(r.snapshot()), count)
		}
	}
}

type failingStarter struct{ err error }

func (s failingStarter) Start() (Child, error) { return nil, s.err }

type supervisorDiagnosticRun struct {
	cancel context.CancelFunc
	joined chan struct{}
	err    error
}

func startSupervisorDiagnosticRun(t *testing.T, ctx context.Context, cancel context.CancelFunc, supervisor *Supervisor) *supervisorDiagnosticRun {
	t.Helper()
	run := &supervisorDiagnosticRun{cancel: cancel, joined: make(chan struct{})}
	go func() {
		defer close(run.joined)
		run.err = supervisor.Run(ctx)
	}()
	t.Cleanup(func() { run.stop(t) })
	return run
}

func (r *supervisorDiagnosticRun) stop(t *testing.T) {
	t.Helper()
	r.cancel()
	select {
	case <-r.joined:
		if r.err != nil {
			t.Errorf("supervisor run error: %v", r.err)
		}
	case <-time.After(3 * time.Second):
		t.Error("supervisor did not stop during cleanup")
	}
}

func TestSupervisorDiagnostic_AutonomousStartFailureIsSafeAndUncorrelated(t *testing.T) {
	cause := &os.PathError{Op: "fork", Path: "/private/mihomo", Err: os.ErrPermission}
	recorder := newSupervisorDiagnosticRecorder()
	waiter := newFakeWaiter()
	observations := &observationLog{}
	s := New(Options{
		Starter: failingStarter{err: cause}, Waiter: waiter, Observe: observations.add,
		DiagnosticReporter: recorder.report,
	})
	ctx, cancel := context.WithCancel(logging.WithOperation(context.Background(), logging.OperationMetadata{ID: "last-request", Name: "core.restart"}))
	startSupervisorDiagnosticRun(t, ctx, cancel, s)

	record := recorder.next(t)
	if record.operation != (logging.OperationMetadata{}) || record.record.Component != "supervisor" || record.record.Event != "core.start.failed" || !errors.Is(record.record.Err, cause) {
		t.Fatalf("record=%#v", record)
	}
	waiter.next(t)
	values := observations.snapshot()
	if len(values) == 0 || values[len(values)-1].LastError != "mihomo process start failed" {
		t.Fatalf("observations=%#v", values)
	}
	if values[len(values)-1].LastError == cause.Error() {
		t.Fatal("LastError exposed the start cause")
	}
}

func TestSupervisorDiagnostic_UnexpectedExitKeepsCause(t *testing.T) {
	starter := newFakeStarter()
	waiter := newFakeWaiter()
	recorder := newSupervisorDiagnosticRecorder()
	observations := &observationLog{}
	s := New(Options{Starter: starter, Waiter: waiter, Observe: observations.add, DiagnosticReporter: recorder.report})
	ctx, cancel := context.WithCancel(context.Background())
	startSupervisorDiagnosticRun(t, ctx, cancel, s)
	cause := &os.PathError{Op: "wait", Path: "/private/mihomo", Err: os.ErrInvalid}

	starter.next(t).exit(cause)
	record := recorder.next(t)
	if record.record.Event != "core.exit.unexpected" || !errors.Is(record.record.Err, cause) {
		t.Fatalf("record=%#v", record)
	}
	waiter.next(t)
	values := observations.snapshot()
	if len(values) == 0 || values[len(values)-1].LastError != "mihomo process exited unexpectedly" {
		t.Fatalf("observations=%#v", values)
	}
}

func TestSupervisorDiagnostic_HealthTransitionKeepsThirdCause(t *testing.T) {
	starter := newFakeStarter()
	waiter := newFakeWaiter()
	recorder := newSupervisorDiagnosticRecorder()
	observations := &observationLog{}
	causes := []*os.PathError{
		{Op: "health", Path: "/private/first", Err: os.ErrInvalid},
		{Op: "health", Path: "/private/second", Err: os.ErrInvalid},
		{Op: "health", Path: "/private/third", Err: os.ErrPermission},
	}
	var healthMu sync.Mutex
	healthCalls := 0
	s := New(Options{
		Starter: starter, Waiter: waiter, Observe: observations.add, DiagnosticReporter: recorder.report,
		Health: func(context.Context) error {
			healthMu.Lock()
			defer healthMu.Unlock()
			cause := causes[healthCalls]
			healthCalls++
			return cause
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	startSupervisorDiagnosticRun(t, ctx, cancel, s)

	child := starter.next(t)
	waiter.next(t).release()
	for failure := 0; failure < 2; failure++ {
		waiter.next(t).release()
	}
	select {
	case <-child.terminated:
	case <-time.After(3 * time.Second):
		t.Fatal("health transition did not stop the child")
	}
	record := recorder.next(t)
	if record.record.Event != "core.health.failed" || !errors.Is(record.record.Err, causes[2]) {
		t.Fatalf("record=%#v", record)
	}
	waiter.next(t)
	values := observations.snapshot()
	if len(values) == 0 || values[len(values)-1].LastError != "mihomo health check failed three times" || strings.Contains(values[len(values)-1].LastError, "third") {
		t.Fatalf("observations=%#v", values)
	}
}

func TestSupervisorDiagnostic_ThirdHealthCancellationDoesNotEmitFailure(t *testing.T) {
	starter := newFakeStarter()
	waiter := newFakeWaiter()
	recorder := newSupervisorDiagnosticRecorder()
	thirdHealthStarted := make(chan struct{})
	releaseThirdHealth := make(chan struct{})
	killStarted := make(chan struct{})
	releaseKill := make(chan struct{})
	var releaseThirdHealthOnce sync.Once
	var releaseKillOnce sync.Once
	releaseThirdHealthGate := func() { releaseThirdHealthOnce.Do(func() { close(releaseThirdHealth) }) }
	releaseKillGate := func() { releaseKillOnce.Do(func() { close(releaseKill) }) }
	releaseGates := func() {
		releaseThirdHealthGate()
		releaseKillGate()
	}
	healthCalls := 0
	s := New(Options{
		Starter: starter, Waiter: waiter, StopTimeout: time.Millisecond, DiagnosticReporter: recorder.report,
		Health: func(context.Context) error {
			healthCalls++
			if healthCalls < 3 {
				return os.ErrInvalid
			}
			close(thirdHealthStarted)
			<-releaseThirdHealth
			return context.Canceled
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	run := startSupervisorDiagnosticRun(t, ctx, cancel, s)
	t.Cleanup(releaseGates)

	child := starter.next(t)
	child.setHangOnTerminate(true)
	child.setKillHook(func() {
		close(killStarted)
		<-releaseKill
	})
	for wait := 0; wait < 3; wait++ {
		waiter.next(t).release()
	}
	select {
	case <-thirdHealthStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("third health check did not start")
	}
	releaseThirdHealthGate()
	select {
	case <-killStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("health failure did not enter child cleanup")
	}
	cancel()
	releaseGates()
	run.stop(t)

	for _, record := range recorder.snapshot() {
		if record.record.Event == "core.health.failed" {
			t.Fatalf("reported normal health cancellation: %#v", record)
		}
	}
}

func TestSupervisorDiagnostic_HealthAndTerminationFailuresKeepBothCauses(t *testing.T) {
	starter := newFakeStarter()
	waiter := newFakeWaiter()
	recorder := newSupervisorDiagnosticRecorder()
	healthCause := &os.PathError{Op: "health", Path: "/private/health-secret", Err: os.ErrPermission}
	terminationCause := &os.SyscallError{Syscall: "terminate", Err: os.ErrPermission}
	s := New(Options{
		Starter: starter, Waiter: waiter, DiagnosticReporter: recorder.report,
		Health: func(context.Context) error { return healthCause },
	})
	ctx, cancel := context.WithCancel(context.Background())
	startSupervisorDiagnosticRun(t, ctx, cancel, s)

	child := starter.next(t)
	child.mu.Lock()
	child.terminateErr = terminationCause
	child.mu.Unlock()
	waiter.next(t).release()
	for failure := 0; failure < 2; failure++ {
		waiter.next(t).release()
	}

	records := recorder.waitForCount(t, 2)
	if records[0].record.Event != "core.health.failed" || !errors.Is(records[0].record.Err, healthCause) || records[0].record.Err.Error() != "mihomo health check failed three times" {
		t.Fatalf("health record=%#v", records[0])
	}
	if records[1].record.Event != "core.termination.failed" || !errors.Is(records[1].record.Err, terminationCause) || records[1].record.Err.Error() != "mihomo process termination failed" {
		t.Fatalf("termination record=%#v", records[1])
	}
}

func TestSupervisorDiagnostic_AutonomousTerminationFailureIsReported(t *testing.T) {
	starter := newFakeStarter()
	recorder := newSupervisorDiagnosticRecorder()
	s := New(Options{Starter: starter, DiagnosticReporter: recorder.report})
	ctx, cancel := context.WithCancel(context.Background())
	startSupervisorDiagnosticRun(t, ctx, cancel, s)
	child := starter.next(t)
	cause := &os.PathError{Op: "terminate", Path: "/private/mihomo", Err: os.ErrPermission}
	child.mu.Lock()
	child.terminateErr = cause
	child.mu.Unlock()

	cancel()
	record := recorder.next(t)
	if record.record.Event != "core.termination.failed" || !errors.Is(record.record.Err, cause) {
		t.Fatalf("record=%#v", record)
	}
}

func TestSupervisorDiagnostic_ExplicitRestartFailureReturnsToOwnerWithoutReport(t *testing.T) {
	starter := newFakeStarter()
	recorder := newSupervisorDiagnosticRecorder()
	s := New(Options{Starter: starter, DiagnosticReporter: recorder.report})
	ctx, cancel := context.WithCancel(context.Background())
	startSupervisorDiagnosticRun(t, ctx, cancel, s)
	child := starter.next(t)
	cause := &os.PathError{Op: "terminate", Path: "/private/mihomo", Err: os.ErrPermission}
	child.mu.Lock()
	child.terminateErr = cause
	child.mu.Unlock()

	if err := s.Restart(context.Background()); !errors.Is(err, cause) {
		t.Fatalf("restart error=%v", err)
	}
	if records := recorder.snapshot(); len(records) != 0 {
		t.Fatalf("explicit restart was reported by supervisor: %#v", records)
	}
}

func TestSupervisorDiagnostic_ExplicitMaintenanceFailureReturnsToOwnerWithoutReport(t *testing.T) {
	starter := newFakeStarter()
	recorder := newSupervisorDiagnosticRecorder()
	s := New(Options{Starter: starter, DiagnosticReporter: recorder.report})
	ctx, cancel := context.WithCancel(context.Background())
	startSupervisorDiagnosticRun(t, ctx, cancel, s)
	starter.next(t)
	cause := &os.PathError{Op: "rename", Path: "/private/mihomo", Err: os.ErrPermission}

	err := s.Maintain(context.Background(), func() error { return cause })

	if !errors.Is(err, cause) {
		t.Fatalf("maintenance error=%v", err)
	}
	starter.next(t)
	if records := recorder.snapshot(); len(records) != 0 {
		t.Fatalf("explicit maintenance was reported by supervisor: %#v", records)
	}
}

func TestSupervisorDiagnostic_HealthyChecksDoNotEmit(t *testing.T) {
	starter := newFakeStarter()
	waiter := newFakeWaiter()
	recorder := newSupervisorDiagnosticRecorder()
	healthCalled := make(chan struct{}, 1)
	s := New(Options{
		Starter: starter, Waiter: waiter, DiagnosticReporter: recorder.report,
		Health: func(context.Context) error {
			healthCalled <- struct{}{}
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	startSupervisorDiagnosticRun(t, ctx, cancel, s)
	starter.next(t)
	waiter.next(t).release()
	select {
	case <-healthCalled:
	case <-time.After(3 * time.Second):
		t.Fatal("health check did not run")
	}
	if records := recorder.snapshot(); len(records) != 0 {
		t.Fatalf("healthy loop emitted diagnostics: %#v", records)
	}
}
