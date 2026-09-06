package runtime

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"go.yaml.in/yaml/v3"
)

type resourceGraph any

type preparedProviderPlan interface {
	Recheck(context.Context) error
	Commit(context.Context, func(context.Context) error) error
	Close(context.Context) error
}

type providerResourceRuntime interface {
	Recover(context.Context) error
	Snapshot(context.Context, subscription.PolicyInput) (resourceGraph, error)
	PrepareProvider(context.Context, subscription.PolicyInput, string, resourceGraph, string, string) (preparedProviderPlan, error)
	Prepare(context.Context, subscription.PolicyInput, string, resourceGraph) (preparedResourcePlan, error)
	PrepareOffline(context.Context, subscription.PolicyInput, resourceGraph) (preparedResourcePlan, error)
}

type preparedResourcePlan interface {
	resourceActivationPlan
	Close(context.Context) error
}

type subscriptionResourceRuntime struct {
	preparer *subscription.ResourcePreparer
	trusted  *core.TrustedExecution
}

func newProviderResourceRuntime(preparer *subscription.ResourcePreparer, trusted *core.TrustedExecution) providerResourceRuntime {
	if preparer == nil {
		return nil
	}
	return subscriptionResourceRuntime{preparer: preparer, trusted: trusted}
}

func (r subscriptionResourceRuntime) Recover(ctx context.Context) error {
	return r.preparer.Recover(ctx)
}
func (r subscriptionResourceRuntime) Snapshot(ctx context.Context, input subscription.PolicyInput) (resourceGraph, error) {
	return r.preparer.SnapshotResources(ctx, input)
}
func (r subscriptionResourceRuntime) PrepareProvider(ctx context.Context, input subscription.PolicyInput, mode string, graph resourceGraph, kind, name string) (preparedProviderPlan, error) {
	typed, ok := graph.(*subscription.ResourceGraph)
	if !ok || typed == nil {
		return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "managed resource graph unavailable"}
	}
	return r.preparer.PrepareProvider(ctx, input, mode, typed, kind, name)
}

func (r subscriptionResourceRuntime) Prepare(ctx context.Context, input subscription.PolicyInput, mode string, graph resourceGraph) (preparedResourcePlan, error) {
	if r.trusted == nil {
		return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "trusted resource activation unavailable"}
	}
	typed, ok := graph.(*subscription.ResourceGraph)
	if graph != nil && (!ok || typed == nil) {
		return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "managed resource graph unavailable"}
	}
	prepared, err := r.preparer.Prepare(ctx, input, mode, typed)
	if err != nil {
		return nil, err
	}
	return &trustedPreparedResourcePlan{trusted: r.trusted, prepared: prepared}, nil
}

func (r subscriptionResourceRuntime) PrepareOffline(ctx context.Context, input subscription.PolicyInput, graph resourceGraph) (preparedResourcePlan, error) {
	if r.trusted == nil {
		return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "trusted resource activation unavailable"}
	}
	typed, ok := graph.(*subscription.ResourceGraph)
	if !ok || typed == nil {
		return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "managed resource graph unavailable"}
	}
	prepared, err := r.preparer.PrepareOffline(ctx, input, typed)
	if err != nil {
		return nil, err
	}
	return &trustedPreparedResourcePlan{trusted: r.trusted, prepared: prepared}, nil
}

type trustedPreparedResourcePlan struct {
	trusted  *core.TrustedExecution
	prepared *subscription.PreparedResources
}

func (p *trustedPreparedResourcePlan) Begin(ctx context.Context) (resourceActivationTransaction, error) {
	return trustedResourcePlan{trusted: p.trusted, prepared: p.prepared}.Begin(ctx)
}
func (p *trustedPreparedResourcePlan) Identity() (string, uint64)      { return p.prepared.Identity() }
func (p *trustedPreparedResourcePlan) Close(ctx context.Context) error { return p.prepared.Close(ctx) }

func (m *Manager) activateManagedPlan(ctx context.Context, operation Operation, generation uint64, plan preparedResourcePlan, change resourceStateChange) error {
	if trusted, ok := plan.(*trustedPreparedResourcePlan); ok {
		return m.activatePreparedResources(ctx, operation, generation, trusted.prepared, change)
	}
	return m.activateResourcePlanWithState(ctx, operation, generation, plan, change)
}

type providerRefreshSnapshot struct {
	input            subscription.PolicyInput
	profileVersion   uint64
	cacheHash        [sha256.Size]byte
	configGeneration uint64
	proxyMode        string
}

type subscriptionRefreshChange struct {
	manager  *Manager
	prepared subscription.PreparedRefresh
	wantID   string
	wantGen  uint64
	receipt  subscription.Receipt
	applied  bool
}

type subscriptionUseChange struct {
	manager *Manager
	id      string
	before  subscription.Catalog
	after   subscription.Catalog
	applied bool
}

type settingsResourceChange struct {
	manager   *Manager
	candidate settingsCandidate
	applied   bool
}

func (c *settingsResourceChange) ApplyLocked() error {
	if _, err := c.manager.saveSettingsCandidate(c.candidate); err != nil {
		return err
	}
	c.manager.publishSettings(c.candidate)
	c.applied = true
	return nil
}
func (c *settingsResourceChange) RestoreLocked() error {
	if !c.applied {
		return nil
	}
	_, err := c.manager.restoreSettings(c.candidate.before)
	if err == nil {
		c.applied = false
	}
	return err
}
func (*settingsResourceChange) UpdateSnapshot(*state.Snapshot) {}

func (c *subscriptionUseChange) ApplyLocked() error {
	before, after, err := c.manager.subscriptions.Mutate(func(catalog *subscription.Catalog) error {
		index := catalog.Index(c.id)
		if index < 0 || !catalog.Profiles[index].Enabled || catalog.Profiles[index].Generation == 0 {
			return protocol.APIError{Code: protocol.CodeInvalidState, Message: "subscription is disabled or has no valid cache"}
		}
		catalog.ActiveID = c.id
		return nil
	})
	if err != nil {
		return err
	}
	c.before, c.after, c.applied = before, after, true
	return nil
}
func (c *subscriptionUseChange) RestoreLocked() error {
	if !c.applied {
		return nil
	}
	err := c.manager.subscriptions.Restore(c.before)
	if err == nil {
		c.applied = false
	}
	return err
}
func (c *subscriptionUseChange) UpdateSnapshot(snapshot *state.Snapshot) {
	c.manager.syncSubscriptionState(snapshot, c.after)
}

func (c *subscriptionRefreshChange) ApplyLocked() error {
	receipt, err := c.manager.subscriptions.CommitRefresh(c.prepared)
	if err != nil {
		return err
	}
	index := receipt.After.Index(c.wantID)
	if receipt.After.ActiveID != c.wantID || index < 0 || receipt.After.Profiles[index].Generation != c.wantGen {
		if rollbackErr := c.manager.subscriptions.Rollback(receipt); rollbackErr != nil {
			return resourceActivationDegraded()
		}
		return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "subscription generation changed during activation"}
	}
	c.receipt, c.applied = receipt, true
	return nil
}
func (c *subscriptionRefreshChange) RestoreLocked() error {
	if !c.applied {
		return nil
	}
	err := c.manager.subscriptions.Rollback(c.receipt)
	if err == nil {
		c.applied = false
	}
	return err
}
func (c *subscriptionRefreshChange) UpdateSnapshot(snapshot *state.Snapshot) {
	c.manager.syncSubscriptionState(snapshot, c.receipt.After)
}

func (m *Manager) refreshSubscriptionManaged(ctx context.Context, operation Operation, id string) (subscription.PublicProfile, error) {
	result, err := m.doOperation(ctx, "sub-refresh:"+operation.ID, func() (any, error) {
		if m.subscriptions == nil || m.rootConfigInput == nil {
			return nil, subscriptionsUnavailable()
		}
		settings, configGeneration := m.configInputs()
		catalog := m.subscriptions.Snapshot()
		index := catalog.Index(id)
		if index < 0 {
			return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "subscription not found"}
		}
		profile := catalog.Profiles[index]
		if catalog.ActiveID != "" && catalog.ActiveID != id {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "only the active subscription can refresh managed resources"}
		}
		var graph resourceGraph
		var err error
		if profile.Generation > 0 {
			_, document, readErr := m.subscriptions.ReadCache(id)
			if readErr != nil {
				return nil, readErr
			}
			currentInput, inputErr := m.rootPolicyInput(ctx, document, settings, id, profile.Generation)
			if inputErr != nil {
				return nil, inputErr
			}
			graph, err = m.providerResources.Snapshot(ctx, currentInput)
			if err != nil {
				return nil, err
			}
		}
		prepared, err := m.subscriptions.PrepareRefresh(ctx, id)
		if err != nil {
			return nil, err
		}
		nextGeneration := profile.Generation
		if prepared.AdvancesGeneration() {
			nextGeneration++
		}
		input, err := m.rootPolicyInput(ctx, prepared.Document(), settings, id, nextGeneration)
		if err != nil {
			return nil, err
		}
		plan, err := m.providerResources.Prepare(ctx, input, profile.ProxyMode, graph)
		if err != nil {
			return nil, err
		}
		defer func() { _ = plan.Close(context.WithoutCancel(ctx)) }()
		change := &subscriptionRefreshChange{manager: m, prepared: prepared, wantID: id, wantGen: nextGeneration}
		if err = m.activateManagedPlan(ctx, operation, configGeneration, plan, change); err != nil {
			m.markConfigDegraded(ctx, err)
			return nil, err
		}
		m.refreshSubscriptionLogSecrets()
		return findPublicProfile(m.subscriptions.Snapshot().Public(), id)
	})
	if err != nil {
		return subscription.PublicProfile{}, err
	}
	return result.(subscription.PublicProfile), nil
}

func (m *Manager) useSubscriptionManaged(ctx context.Context, operation Operation, id string) (subscription.PublicProfile, error) {
	result, err := m.doOperation(ctx, "sub-use:"+operation.ID, func() (any, error) {
		if m.subscriptions == nil || m.rootConfigInput == nil {
			return nil, subscriptionsUnavailable()
		}
		settings, configGeneration := m.configInputs()
		catalog := m.subscriptions.Snapshot()
		index := catalog.Index(id)
		if index < 0 || !catalog.Profiles[index].Enabled || catalog.Profiles[index].Generation == 0 {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "subscription is disabled or has no valid cache"}
		}
		profile := catalog.Profiles[index]
		_, document, err := m.subscriptions.ReadCache(id)
		if err != nil {
			return nil, err
		}
		input, err := m.rootPolicyInput(ctx, document, settings, id, profile.Generation)
		if err != nil {
			return nil, err
		}
		graph, err := m.providerResources.Snapshot(ctx, input)
		if err != nil {
			return nil, err
		}
		plan, err := m.providerResources.PrepareOffline(ctx, input, graph)
		if err != nil {
			return nil, err
		}
		defer func() { _ = plan.Close(context.WithoutCancel(ctx)) }()
		change := &subscriptionUseChange{manager: m, id: id}
		if err = m.activateManagedPlan(ctx, operation, configGeneration, plan, change); err != nil {
			m.markConfigDegraded(ctx, err)
			return nil, err
		}
		return findPublicProfile(m.subscriptions.Snapshot().Public(), id)
	})
	if err != nil {
		return subscription.PublicProfile{}, err
	}
	return result.(subscription.PublicProfile), nil
}

func (m *Manager) prepareManagedCurrent(ctx context.Context, settings config.Settings) (preparedResourcePlan, uint64, error) {
	_, generation := m.configInputs()
	catalog := m.subscriptions.Snapshot()
	if catalog.ActiveID == "" {
		document := subscription.Document{"proxies": []any{}, "proxy-groups": []any{}, "rules": []any{"MATCH,DIRECT"}}
		input, err := m.rootPolicyInput(ctx, document, settings, "00000000000000000000000000000000", 1)
		if err != nil {
			return nil, generation, err
		}
		plan, err := m.providerResources.Prepare(ctx, input, subscription.ProxyModeDirect, nil)
		return plan, generation, err
	}
	index := catalog.Index(catalog.ActiveID)
	if index < 0 || catalog.Profiles[index].Generation == 0 {
		return nil, generation, protocol.APIError{Code: protocol.CodeDataFailure, Message: "active subscription cache is unavailable"}
	}
	profile := catalog.Profiles[index]
	_, document, err := m.subscriptions.ReadCache(profile.ID)
	if err != nil {
		return nil, generation, err
	}
	input, err := m.rootPolicyInput(ctx, document, settings, profile.ID, profile.Generation)
	if err != nil {
		return nil, generation, err
	}
	graph, err := m.providerResources.Snapshot(ctx, input)
	if err != nil {
		return nil, generation, err
	}
	plan, err := m.providerResources.PrepareOffline(ctx, input, graph)
	return plan, generation, err
}

func (m *Manager) mutateTunManaged(ctx context.Context, op Operation, enable, force bool) (protocol.TunStatus, error) {
	if err := ctx.Err(); err != nil {
		return protocol.TunStatus{}, err
	}
	conflict := m.detectTunConflict(ctx)
	if enable && !force && conflict != nil && len(conflict.OtherTunInterfaces) > 0 {
		return protocol.TunStatus{}, protocol.APIError{Code: protocol.CodeTunConflict, Message: "other TUN adapters detected; routing conflict or loop risk", Details: map[string]any{"other_tun_interfaces": conflict.OtherTunInterfaces, "other_mihomo_processes": conflict.OtherMihomoProcesses}}
	}
	candidate, err := m.prepareSettings(func(settings *config.Settings) error {
		settings.Tun = buildManagedTun(enable, settings.Tun)
		return nil
	})
	if err != nil {
		return protocol.TunStatus{}, err
	}
	plan, generation, err := m.prepareManagedCurrent(ctx, candidate.after)
	if err != nil {
		return protocol.TunStatus{}, err
	}
	defer func() { _ = plan.Close(context.WithoutCancel(ctx)) }()
	change := &settingsResourceChange{manager: m, candidate: candidate}
	if err = m.activateManagedPlan(ctx, op, generation, plan, change); err != nil {
		m.setTunLastError(tunErrorMessage(err))
		m.markConfigDegraded(ctx, err)
		return protocol.TunStatus{}, err
	}
	m.setTunLastError("")
	return buildTunStatusFromObservation(candidate.after, m.store.Load().Revision, conflict, nil, ""), nil
}

// RefreshProvider refreshes one daemon-managed local rule provider.
func (m *Manager) RefreshProvider(ctx context.Context, operation Operation, name string) error {
	return m.refreshProvider(ctx, operation, "rule", name)
}

func (m *Manager) refreshProvider(ctx context.Context, operation Operation, kind, name string) error {
	_, err := m.doOperation(ctx, kind+"-provider:"+operation.ID, func() (any, error) {
		if m.controller == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo controller is unavailable"}
		}
		if m.providerResources == nil {
			if kind != "rule" {
				return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "managed proxy provider refresh is unavailable"}
			}
			if err := m.lockMutation(ctx); err != nil {
				return nil, err
			}
			defer m.unlock()
			_, err := m.updateStateLocked(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(current state.Snapshot) (state.Snapshot, error) {
				return current, m.controller.UpdateRuleProvider(ctx, name)
			})
			return struct{}{}, err
		}
		if m.subscriptions == nil || m.rootConfigInput == nil {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "managed provider refresh is unavailable"}
		}
		snapshot, err := m.captureProviderRefresh(ctx)
		if err != nil {
			return nil, err
		}
		graph, err := m.providerResources.Snapshot(ctx, snapshot.input)
		if err != nil {
			return nil, err
		}
		prepared, err := m.providerResources.PrepareProvider(ctx, snapshot.input, snapshot.proxyMode, graph, kind, name)
		if err != nil {
			return nil, err
		}
		defer func() { _ = prepared.Close(context.WithoutCancel(ctx)) }()
		if err = m.lockMutation(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err = m.checkIfRevision(operation.IfRevision); err == nil {
			err = m.recheckProviderRefresh(ctx, snapshot)
		}
		if err == nil {
			err = prepared.Recheck(ctx)
		}
		if err != nil {
			return nil, err
		}
		_, err = m.updateStateLocked(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(current state.Snapshot) (state.Snapshot, error) {
			if commitErr := prepared.Commit(ctx, func(reloadCtx context.Context) error {
				if kind == "rule" {
					return m.controller.UpdateRuleProvider(reloadCtx, name)
				}
				updater, ok := m.controller.(interface {
					UpdateProxyProvider(context.Context, string) error
				})
				if !ok {
					return protocol.APIError{Code: protocol.CodeInvalidState, Message: "proxy provider reload is unavailable"}
				}
				return updater.UpdateProxyProvider(reloadCtx, name)
			}); commitErr != nil {
				return current, commitErr
			}
			return current, nil
		})
		if err != nil {
			m.markConfigDegraded(ctx, err)
			return nil, err
		}
		return struct{}{}, nil
	})
	return err
}

func (m *Manager) captureProviderRefresh(ctx context.Context) (providerRefreshSnapshot, error) {
	settings, configGeneration := m.configInputs()
	catalog := m.subscriptions.Snapshot()
	index := catalog.Index(catalog.ActiveID)
	if index < 0 || catalog.Profiles[index].Generation == 0 {
		return providerRefreshSnapshot{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "active subscription cache is unavailable"}
	}
	profile := catalog.Profiles[index]
	raw, document, err := m.subscriptions.ReadCache(profile.ID)
	if err != nil {
		return providerRefreshSnapshot{}, err
	}
	input, err := m.rootPolicyInput(ctx, document, settings, profile.ID, profile.Generation)
	if err != nil {
		return providerRefreshSnapshot{}, err
	}
	return providerRefreshSnapshot{input: input, profileVersion: profile.Version, cacheHash: sha256.Sum256(raw), configGeneration: configGeneration, proxyMode: profile.ProxyMode}, nil
}

func (m *Manager) rootPolicyInput(ctx context.Context, document subscription.Document, settings config.Settings, id string, generation uint64) (subscription.PolicyInput, error) {
	input, err := m.rootConfigInput(ctx, document, settings)
	if err != nil {
		return subscription.PolicyInput{}, err
	}
	input.YAML, err = yaml.Marshal(document)
	if err != nil {
		return subscription.PolicyInput{}, err
	}
	input.SubscriptionID = id
	input.Generation = generation
	input.Settings = settings
	input.Resources = nil
	return input, nil
}

func (m *Manager) recheckProviderRefresh(ctx context.Context, captured providerRefreshSnapshot) error {
	if captured.configGeneration != m.currentConfigGeneration() {
		return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "configuration inputs changed during provider refresh"}
	}
	catalog := m.subscriptions.Snapshot()
	index := catalog.Index(captured.input.SubscriptionID)
	if catalog.ActiveID != captured.input.SubscriptionID || index < 0 || catalog.Profiles[index].Generation != captured.input.Generation || catalog.Profiles[index].Version != captured.profileVersion {
		return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "subscription changed during provider refresh"}
	}
	raw, _, err := m.subscriptions.ReadCache(captured.input.SubscriptionID)
	if err != nil || sha256.Sum256(raw) != captured.cacheHash {
		return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "subscription cache changed during provider refresh"}
	}
	return nil
}

// RunProviderScheduler owns managed provider refreshes until ctx is canceled.
func (m *Manager) RunProviderScheduler(ctx context.Context) error {
	if m.providerResources == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	next := make(map[string]time.Time)
	failures := make(map[string]int)
	var sequence uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		specs, err := m.scheduledProviders(ctx)
		if err != nil {
			m.reportBackground("provider-scheduler", err)
		}
		now := time.Now().UTC()
		wait := time.Minute
		for _, spec := range specs {
			if spec.URL == "" {
				continue
			}
			key := spec.Kind + ":" + spec.Name
			due := next[key]
			if !due.IsZero() && now.Before(due) {
				if remaining := due.Sub(now); remaining < wait {
					wait = remaining
				}
				continue
			}
			sequence++
			op := Operation{ID: fmt.Sprintf("scheduler-provider-%d", sequence), Source: "scheduler"}
			if err = m.refreshProvider(ctx, op, spec.Kind, spec.Name); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				failures[key]++
				delay := subscription.RetryDelay(failures[key], time.Minute, 30*time.Minute)
				next[key] = now.Add(delay)
				m.reportBackground("provider-scheduler", err)
				continue
			}
			delete(failures, key)
			next[key] = now.Add(spec.Interval)
		}
		if wait <= 0 {
			wait = time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (m *Manager) scheduledProviders(ctx context.Context) ([]subscription.ProviderSpec, error) {
	captured, err := m.captureProviderRefresh(ctx)
	if err != nil {
		return nil, err
	}
	requirements, err := subscription.NewRootConfigPolicy().Inspect(ctx, captured.input)
	if err != nil {
		return nil, err
	}
	return requirements.Providers, nil
}
