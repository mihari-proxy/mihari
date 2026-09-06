package runtime

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"go.yaml.in/yaml/v3"
)

type fakeProviderResources struct {
	started, release chan struct{}
	plan             preparedProviderPlan
	prepareCalls     atomic.Int32
	input            subscription.PolicyInput
	batch            preparedResourcePlan
	batchCalls       atomic.Int32
	batchInput       subscription.PolicyInput
}

func (f *fakeProviderResources) Recover(context.Context) error { return nil }
func (f *fakeProviderResources) Snapshot(context.Context, subscription.PolicyInput) (resourceGraph, error) {
	return struct{}{}, nil
}
func (f *fakeProviderResources) PrepareProvider(_ context.Context, input subscription.PolicyInput, _ string, _ resourceGraph, _, _ string) (preparedProviderPlan, error) {
	f.input = input
	f.prepareCalls.Add(1)
	if f.started != nil {
		close(f.started)
		<-f.release
	}
	return f.plan, nil
}
func (f *fakeProviderResources) Prepare(_ context.Context, input subscription.PolicyInput, _ string, _ resourceGraph) (preparedResourcePlan, error) {
	f.batchCalls.Add(1)
	f.batchInput = input
	if f.batch == nil {
		return nil, errors.New("unexpected batch preparation")
	}
	if prepared, ok := f.batch.(*fakePreparedResources); ok && prepared.id == "" {
		prepared.id, prepared.gen = input.SubscriptionID, input.Generation
	}
	return f.batch, nil
}

type fakePreparedResources struct {
	id     string
	gen    uint64
	tx     *fakeResourceTransaction
	closed atomic.Int32
}

func (p *fakePreparedResources) Begin(context.Context) (resourceActivationTransaction, error) {
	return p.tx, nil
}
func (p *fakePreparedResources) Identity() (string, uint64)  { return p.id, p.gen }
func (p *fakePreparedResources) Close(context.Context) error { p.closed.Add(1); return nil }
func (f *fakeProviderResources) PrepareOffline(context.Context, subscription.PolicyInput, resourceGraph) (preparedResourcePlan, error) {
	f.batchCalls.Add(1)
	if f.batch == nil {
		return nil, errors.New("unexpected offline preparation")
	}
	return f.batch, nil
}

type fakePreparedProvider struct {
	rechecks atomic.Int32
	commits  atomic.Int32
	closed   atomic.Int32
	commit   func(context.Context, func(context.Context) error) error
}

func (p *fakePreparedProvider) Recheck(context.Context) error { p.rechecks.Add(1); return nil }
func (p *fakePreparedProvider) Commit(ctx context.Context, reload func(context.Context) error) error {
	p.commits.Add(1)
	if p.commit != nil {
		return p.commit(ctx, reload)
	}
	return reload(ctx)
}
func (p *fakePreparedProvider) Close(context.Context) error { p.closed.Add(1); return nil }

func rootProviderManager(t *testing.T) (*Manager, *subscription.Service, *fakeController, subscription.PublicProfile) {
	t.Helper()
	m, service, _, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies:\n  - {name: direct, type: direct}\nrule-providers:\n  domains: {type: http, behavior: domain, url: 'https://example.test/rules'}\nrules: ['RULE-SET,domains,DIRECT']\n"))
	}))
	controller := &fakeController{}
	m.controller = controller
	m.rootConfigInput = func(_ context.Context, document subscription.Document, settings config.Settings) (subscription.PolicyInput, error) {
		content, err := yaml.Marshal(document)
		return subscription.PolicyInput{YAML: content, CoreTag: "v1.19.30", OS: "linux", Arch: "amd64", Settings: settings}, err
	}
	profile, err := m.AddSubscription(context.Background(), Operation{ID: "provider-add", Source: "test"}, AddSubscriptionInput{Name: "fixture", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	return m, service, controller, profile
}

func TestRefreshProvider_PreparationDoesNotBlockMutationAndGenerationChangeConflicts(t *testing.T) {
	m, service, _, profile := rootProviderManager(t)
	started, release := make(chan struct{}), make(chan struct{})
	plan := &fakePreparedProvider{}
	resources := &fakeProviderResources{started: started, release: release, plan: plan}
	m.providerResources = resources
	result := make(chan error, 1)
	go func() {
		result <- m.RefreshProvider(context.Background(), Operation{ID: "op-1", Source: "cli"}, "domains")
	}()
	<-started
	if _, _, err := service.Mutate(func(catalog *subscription.Catalog) error {
		index := catalog.Index(profile.ID)
		catalog.Profiles[index].Generation++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.SelectProxy(context.Background(), Operation{ID: "parallel", Source: "test"}, "group", "proxy"); err != nil {
		t.Fatalf("parallel mutation was blocked: %v", err)
	}
	close(release)
	err := <-result
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
		t.Fatalf("refresh error=%v", err)
	}
	if plan.commits.Load() != 0 || plan.closed.Load() != 1 {
		t.Fatalf("commits=%d closed=%d", plan.commits.Load(), plan.closed.Load())
	}
}

func TestUpdateRuleProvider_DelegatesSameOperationAndCachesResult(t *testing.T) {
	m, _, controller, profile := rootProviderManager(t)
	plan := &fakePreparedProvider{}
	resources := &fakeProviderResources{plan: plan}
	m.providerResources = resources
	var reloads atomic.Int32
	controller.updateRuleProvider = func(context.Context, string) error { reloads.Add(1); return nil }
	revision := m.Snapshot().Revision
	op := Operation{ID: "op-1", Source: "cli", IfRevision: &revision}
	if err := m.UpdateRuleProvider(context.Background(), op, "domains"); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateRuleProvider(context.Background(), op, "domains"); err != nil {
		t.Fatal(err)
	}
	if resources.prepareCalls.Load() != 1 || plan.commits.Load() != 1 || reloads.Load() != 1 {
		t.Fatalf("prepare=%d commit=%d reload=%d", resources.prepareCalls.Load(), plan.commits.Load(), reloads.Load())
	}
	if resources.input.SubscriptionID != profile.ID || resources.input.Generation != profile.Generation {
		t.Fatalf("prepared identity=(%q,%d)", resources.input.SubscriptionID, resources.input.Generation)
	}
}

func TestRefreshProvider_RollbackFailureDegradesAndStopsTrustedCore(t *testing.T) {
	for _, test := range []struct {
		name       string
		commitErr  protocol.APIError
		wantStop   int
		wantHealth string
	}{
		{name: "old provider restored", commitErr: protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "provider update failed; previous resource restored"}, wantHealth: "ok"},
		{name: "old provider reload failed", commitErr: protocol.APIError{Code: protocol.CodeDataFailure, Message: "provider recovery could not be confirmed", Details: map[string]any{"degraded": true}}, wantStop: 1, wantHealth: "degraded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, _, _, _ := rootProviderManager(t)
			plan := &fakePreparedProvider{commit: func(context.Context, func(context.Context) error) error { return test.commitErr }}
			m.providerResources = &fakeProviderResources{plan: plan}
			m.trustedCore = &core.TrustedExecution{}
			supervisor := &activationSupervisor{}
			m.supervisor = supervisor
			err := m.RefreshProvider(context.Background(), Operation{ID: "provider-failure", Source: "cli"}, "domains")
			var apiError protocol.APIError
			if !errors.As(err, &apiError) || apiError.Code != test.commitErr.Code {
				t.Fatalf("err=%v", err)
			}
			if supervisor.maintained != test.wantStop || m.Snapshot().Health != test.wantHealth {
				t.Fatalf("maintained=%d health=%q", supervisor.maintained, m.Snapshot().Health)
			}
		})
	}
}

func TestRefreshSubscription_ManagedResourcesActivateWithCommittedGeneration(t *testing.T) {
	m, service, _, profile := rootProviderManager(t)
	tx := &fakeResourceTransaction{validateStarted: make(chan struct{}), validateRelease: make(chan struct{})}
	batch := &fakePreparedResources{id: profile.ID, gen: profile.Generation + 1, tx: tx}
	resources := &fakeProviderResources{batch: batch}
	m.providerResources = resources
	m.supervisor = &activationSupervisor{}
	type refreshResult struct {
		profile subscription.PublicProfile
		err     error
	}
	done := make(chan refreshResult, 1)
	go func() {
		refreshed, err := m.RefreshSubscription(context.Background(), Operation{ID: "managed-refresh", Source: "cli"}, profile.ID)
		done <- refreshResult{profile: refreshed, err: err}
	}()
	<-tx.validateStarted
	if current := service.Snapshot().Profiles[0].Generation; current != profile.Generation {
		t.Fatalf("catalog generation became visible before resource validation: %d", current)
	}
	close(tx.validateRelease)
	result := <-done
	refreshed, err := result.profile, result.err
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Generation != profile.Generation+1 || service.Snapshot().Profiles[0].Generation != refreshed.Generation {
		t.Fatalf("profile=%#v", refreshed)
	}
	if resources.batchCalls.Load() != 1 || batch.closed.Load() != 1 {
		t.Fatalf("batch calls=%d closed=%d", resources.batchCalls.Load(), batch.closed.Load())
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	want := []string{"activate", "validate", "recheck", "publish", "finish", "close"}
	if len(tx.steps) != len(want) {
		t.Fatalf("steps=%v", tx.steps)
	}
	for i := range want {
		if tx.steps[i] != want[i] {
			t.Fatalf("steps=%v", tx.steps)
		}
	}
}

func TestAddSubscription_ManagedResourcesBuildsAndActivatesFirstGeneration(t *testing.T) {
	m, service, _, url := subscriptionManager(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("proxies: []\nrules: ['MATCH,DIRECT']\n"))
	}))
	m.controller = &fakeController{}
	m.rootConfigInput = func(_ context.Context, document subscription.Document, settings config.Settings) (subscription.PolicyInput, error) {
		content, err := yaml.Marshal(document)
		return subscription.PolicyInput{YAML: content, CoreTag: "v1.19.30", OS: "linux", Arch: "amd64", Settings: settings}, err
	}
	tx := &fakeResourceTransaction{validateStarted: make(chan struct{}), validateRelease: make(chan struct{})}
	close(tx.validateRelease)
	batch := &fakePreparedResources{tx: tx}
	resources := &fakeProviderResources{batch: batch}
	m.providerResources = resources
	m.supervisor = &activationSupervisor{}
	profile, err := m.AddSubscription(context.Background(), Operation{ID: "managed-add", Source: "cli"}, AddSubscriptionInput{Name: "first", URL: url})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Generation != 1 || service.Snapshot().ActiveID != profile.ID || resources.batchInput.SubscriptionID != profile.ID || resources.batchInput.Generation != 1 {
		t.Fatalf("profile=%#v catalog=%#v input=(%q,%d)", profile, service.Snapshot(), resources.batchInput.SubscriptionID, resources.batchInput.Generation)
	}
}

func TestUseSubscription_ManagedResourcesActivatesOfflineCache(t *testing.T) {
	m, service, _, first := rootProviderManager(t)
	second, err := m.AddSubscription(context.Background(), Operation{ID: "provider-add-2", Source: "test"}, AddSubscriptionInput{Name: "second", URL: service.Snapshot().Profiles[0].URL})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || service.Snapshot().ActiveID != first.ID {
		t.Fatal("fixture did not retain first active subscription")
	}
	tx := &fakeResourceTransaction{validateStarted: make(chan struct{}), validateRelease: make(chan struct{})}
	close(tx.validateRelease)
	batch := &fakePreparedResources{id: second.ID, gen: second.Generation, tx: tx}
	resources := &fakeProviderResources{batch: batch}
	m.providerResources = resources
	m.supervisor = &activationSupervisor{}
	used, err := m.UseSubscription(context.Background(), Operation{ID: "managed-use", Source: "cli"}, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if used.ID != second.ID || service.Snapshot().ActiveID != second.ID || resources.batchCalls.Load() != 1 {
		t.Fatalf("used=%#v active=%q batch=%d", used, service.Snapshot().ActiveID, resources.batchCalls.Load())
	}
}

func TestEnableTun_ManagedResourcesUsesWholeActivation(t *testing.T) {
	m, _, controller, profile := rootProviderManager(t)
	tx := &fakeResourceTransaction{validateStarted: make(chan struct{}), validateRelease: make(chan struct{})}
	close(tx.validateRelease)
	batch := &fakePreparedResources{id: profile.ID, gen: profile.Generation, tx: tx}
	resources := &fakeProviderResources{batch: batch}
	m.providerResources = resources
	m.supervisor = &activationSupervisor{}
	controller.configs = map[string]any{"tun": map[string]any{"enable": false, "stack": "gVisor"}}
	status, err := m.EnableTun(context.Background(), Operation{ID: "managed-tun", Source: "cli"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !status.DesiredEnable || !tunDesiredEnable(m.settingsSnapshot().Tun) || resources.batchCalls.Load() != 1 {
		t.Fatalf("status=%#v batch=%d", status, resources.batchCalls.Load())
	}
}

func TestUpdateOnboarding_ManagedSettingsUsesWholeActivation(t *testing.T) {
	m, _, _, profile := rootProviderManager(t)
	service, err := onboarding.Open(onboarding.Options{StatePath: filepath.Join(t.TempDir(), "onboarding.json")})
	if err != nil {
		t.Fatal(err)
	}
	m.onboarding = service
	tx := &fakeResourceTransaction{validateStarted: make(chan struct{}), validateRelease: make(chan struct{})}
	close(tx.validateRelease)
	batch := &fakePreparedResources{id: profile.ID, gen: profile.Generation, tx: tx}
	resources := &fakeProviderResources{batch: batch}
	m.providerResources = resources
	m.supervisor = &activationSupervisor{}
	webAddr := "127.0.0.1:9292"
	got, err := m.UpdateOnboarding(context.Background(), Operation{ID: "managed-settings", Source: "cli"}, onboarding.Update{WebAddr: &webAddr})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status.WebAddr != webAddr || resources.batchCalls.Load() != 1 || batch.closed.Load() != 1 {
		t.Fatalf("status=%#v batch=%d closed=%d", got, resources.batchCalls.Load(), batch.closed.Load())
	}
}

func TestProviderScheduler_RefreshesManagedProviderAndJoinsOnCancel(t *testing.T) {
	m, _, controller, _ := rootProviderManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	plan := &fakePreparedProvider{commit: func(callCtx context.Context, reload func(context.Context) error) error {
		err := reload(callCtx)
		cancel()
		return err
	}}
	resources := &fakeProviderResources{plan: plan}
	m.providerResources = resources
	controller.updateRuleProvider = func(context.Context, string) error { return nil }
	err := m.RunProviderScheduler(ctx)
	if !errors.Is(err, context.Canceled) || resources.prepareCalls.Load() != 1 || plan.closed.Load() != 1 {
		t.Fatalf("err=%v prepare=%d closed=%d", err, resources.prepareCalls.Load(), plan.closed.Load())
	}
}
