package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/supervisor"
)

func TestResourceActivation_RejectsMutationButAllowsObserve(t *testing.T) {
	m := New(Options{})
	if err := m.lockMutation(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.resourceActivation = &resourceActivationOwner{}
	m.unlock()
	err := m.lockMutation(context.Background())
	if err == nil {
		m.unlock()
		t.Fatal("conflicting mutation acquired ownership")
	}
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatalf("wrong busy result: %v", err)
	}
	m.Observe(supervisor.Observation{PID: 73})
	if m.Snapshot().Core.PID != 73 {
		t.Fatal("Observe blocked by activation")
	}
}

func TestResourceActivation_CoreInstallRejectsBeforePreparation(t *testing.T) {
	installer := &fakeInstaller{}
	m := New(Options{Installer: installer})
	if err := m.lockMutation(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.resourceActivation = &resourceActivationOwner{}
	m.releaseMutation()
	_, err := m.Install(context.Background(), Operation{ID: "different-operation", Source: "test"})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState || installer.calls.Load() != 0 {
		t.Fatalf("err=%v prepare-calls=%d", err, installer.calls.Load())
	}
}

func TestResourceActivation_ConflictDoesNotTriggerRecursiveStop(t *testing.T) {
	s := &activationSupervisor{}
	m := New(Options{Supervisor: s})
	if err := m.lockMutation(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.resourceActivation = &resourceActivationOwner{}
	m.stopCoreOnUnlock.Store(true)
	m.releaseMutation()
	if err := m.lockMutation(context.Background()); err == nil {
		m.unlock()
		t.Fatal("conflicting mutation entered")
	}
	if s.maintained != 0 || !m.stopCoreOnUnlock.Load() {
		t.Fatal("conflict release recursively entered supervisor maintenance")
	}
}

type activationSupervisor struct{ maintained int }

func (*activationSupervisor) Run(context.Context) error     { return nil }
func (*activationSupervisor) Restart(context.Context) error { return nil }
func (s *activationSupervisor) Maintain(_ context.Context, work func() error) error {
	s.maintained++
	return work()
}

type fakeResourcePlan struct{ tx *fakeResourceTransaction }

func (p fakeResourcePlan) Begin(context.Context) (resourceActivationTransaction, error) {
	return p.tx, nil
}
func (fakeResourcePlan) Identity() (string, uint64) { return "", 0 }

type fakeResourceTransaction struct {
	mu              sync.Mutex
	steps           []string
	validateStarted chan struct{}
	validateRelease chan struct{}
	validateErr     error
	recheckErr      error
	publishErr      error
	finishErr       error
	restoreErr      error
	restoreCanceled bool
	restored        bool
}

func (t *fakeResourceTransaction) record(step string) {
	t.mu.Lock()
	t.steps = append(t.steps, step)
	t.mu.Unlock()
}
func (t *fakeResourceTransaction) Activate(context.Context) error { t.record("activate"); return nil }
func (t *fakeResourceTransaction) Validate(ctx context.Context) error {
	t.record("validate")
	close(t.validateStarted)
	select {
	case <-t.validateRelease:
		return t.validateErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (t *fakeResourceTransaction) Recheck(context.Context) error {
	t.record("recheck")
	return t.recheckErr
}
func (t *fakeResourceTransaction) Publish(context.Context) error {
	t.record("publish")
	return t.publishErr
}
func (t *fakeResourceTransaction) Finish(context.Context) error {
	t.record("finish")
	return t.finishErr
}
func (t *fakeResourceTransaction) Restore(ctx context.Context) error {
	t.record("restore")
	t.restored = true
	t.restoreCanceled = ctx.Err() != nil
	return t.restoreErr
}

func TestResourceActivation_DoubleFailureDegradesAndRetainsMutationBlock(t *testing.T) {
	m := New(Options{Supervisor: &activationSupervisor{}})
	tx := &fakeResourceTransaction{
		validateStarted: make(chan struct{}), validateRelease: make(chan struct{}),
		validateErr: errors.New("validation failed"), restoreErr: errors.New("restore failed"),
	}
	close(tx.validateRelease)
	err := m.activateResourcePlan(context.Background(), Operation{}, m.currentConfigGeneration(), fakeResourcePlan{tx})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure || api.Details["degraded"] != true {
		t.Fatalf("result = %v", err)
	}
	if m.resourceActivation == nil || !m.mutationDegraded.Load() {
		t.Fatal("failed rollback released mutation authority")
	}
	if err = m.lockMutation(context.Background()); err == nil {
		m.unlock()
		t.Fatal("mutation entered after failed activation rollback")
	}
}

func TestResourceActivation_EachPostSwapFailureRollsBack(t *testing.T) {
	for _, stage := range []string{"recheck", "publish", "finish"} {
		t.Run(stage, func(t *testing.T) {
			m := New(Options{Supervisor: &activationSupervisor{}})
			tx := &fakeResourceTransaction{validateStarted: make(chan struct{}), validateRelease: make(chan struct{})}
			close(tx.validateRelease)
			switch stage {
			case "recheck":
				tx.recheckErr = errors.New(stage)
			case "publish":
				tx.publishErr = errors.New(stage)
			case "finish":
				tx.finishErr = errors.New(stage)
			}
			if err := m.activateResourcePlan(context.Background(), Operation{}, m.currentConfigGeneration(), fakeResourcePlan{tx}); err == nil {
				t.Fatal("failure was discarded")
			}
			if !tx.restored || m.resourceActivation != nil {
				t.Fatal("post-swap failure did not converge rollback")
			}
		})
	}
}
func (t *fakeResourceTransaction) Close() { t.record("close") }

func TestResourceActivation_ManagerRetainsOwnerAcrossUnlockedValidation(t *testing.T) {
	s := &activationSupervisor{}
	m := New(Options{Supervisor: s})
	tx := &fakeResourceTransaction{validateStarted: make(chan struct{}), validateRelease: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		result <- m.activateResourcePlan(context.Background(), Operation{}, m.currentConfigGeneration(), fakeResourcePlan{tx})
	}()
	<-tx.validateStarted

	err := m.lockMutation(context.Background())
	if err == nil {
		m.unlock()
		t.Fatal("conflicting mutation acquired activation ownership")
	}
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatalf("wrong conflict result: %v", err)
	}
	m.Observe(supervisor.Observation{PID: 91})
	if m.Snapshot().Core.PID != 91 {
		t.Fatal("Observe did not progress during validation")
	}
	close(tx.validateRelease)
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	if s.maintained != 1 || m.resourceActivation != nil {
		t.Fatal("activation ownership was not settled")
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	want := []string{"activate", "validate", "recheck", "publish", "finish", "close"}
	if len(tx.steps) != len(want) {
		t.Fatalf("steps = %v", tx.steps)
	}
	for i := range want {
		if tx.steps[i] != want[i] {
			t.Fatalf("steps = %v", tx.steps)
		}
	}
}

func TestResourceActivation_CancellationRetainsRollbackOwnership(t *testing.T) {
	m := New(Options{Supervisor: &activationSupervisor{}})
	tx := &fakeResourceTransaction{validateStarted: make(chan struct{}), validateRelease: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- m.activateResourcePlan(ctx, Operation{}, m.currentConfigGeneration(), fakeResourcePlan{tx})
	}()
	<-tx.validateStarted
	cancel()
	err := <-result
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("result = %v", err)
	}
	if !tx.restored || tx.restoreCanceled || m.resourceActivation != nil {
		t.Fatal("cancellation detached rollback ownership")
	}
}

func TestResourceActivation_StaleGenerationRollsBack(t *testing.T) {
	m := New(Options{Supervisor: &activationSupervisor{}})
	generation := m.currentConfigGeneration()
	tx := &fakeResourceTransaction{validateStarted: make(chan struct{}), validateRelease: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		result <- m.activateResourcePlan(context.Background(), Operation{}, generation, fakeResourcePlan{tx})
	}()
	<-tx.validateStarted
	m.settingsMu.Lock()
	m.configGeneration++
	m.settingsMu.Unlock()
	close(tx.validateRelease)
	err := <-result
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
		t.Fatalf("result = %v", err)
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if len(tx.steps) < 2 || tx.steps[len(tx.steps)-2] != "restore" || tx.steps[len(tx.steps)-1] != "close" {
		t.Fatalf("stale activation did not roll back: %v", tx.steps)
	}
}
