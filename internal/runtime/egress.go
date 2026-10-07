package runtime

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sort"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/state"
	"go.yaml.in/yaml/v3"
)

func egressSelection(name string) protocol.EgressSelection {
	if name == "" {
		return protocol.EgressSelection{Mode: "automatic"}
	}
	return protocol.EgressSelection{Mode: "manual", InterfaceName: name}
}

// EgressStatus returns saved selection and a fresh local adapter snapshot.
func (m *Manager) EgressStatus(ctx context.Context) (protocol.EgressStatus, error) {
	if err := m.lockMaintenance(ctx); err != nil {
		return protocol.EgressStatus{}, err
	}
	defer m.unlock()
	return m.egressStatus(ctx)
}

func (m *Manager) egressStatus(ctx context.Context) (protocol.EgressStatus, error) {
	saved := m.settingsSnapshot().EgressInterface
	status := protocol.EgressStatus{Schema: "mihari/v1", Revision: m.store.Load().Revision, Selection: egressSelection(saved), State: "unknown", Interfaces: []protocol.EgressInterface{}}
	adapters, err := m.listInterfaces(ctx)
	if err != nil {
		return status, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "enumerate outbound interfaces"}, err)
	}
	var live map[string]any
	if m.routingCoreStopped() {
		status.State = "saved"
	} else if m.controller != nil {
		live, err = m.controller.Configs(ctx)
		if err == nil && !m.mutationDegraded.Load() {
			want := saved
			known := saved != ""
			if saved == "" {
				if content, readErr := m.egressPreviousConfig(ctx); readErr == nil {
					var document map[string]any
					if parseErr := yaml.Unmarshal(content, &document); parseErr == nil {
						want, _ = document["interface-name"].(string)
						known = true
					}
				}
			}
			if actual, ok := live["interface-name"].(string); ok && known && actual == want {
				status.State = "applied"
			}
		}
	}
	own := m.egressOwnTun(ctx, live)
	found := false
	for _, adapter := range adapters {
		item := protocol.EgressInterface{Name: adapter.Name, Kind: adapter.Kind, Availability: adapter.Availability, Addresses: append([]string{}, adapter.Addresses...), Device: adapter.Description, Selectable: adapter.Name != own}
		if item.Kind == "" {
			item.Kind = "unknown"
		}
		if item.Availability == "" {
			item.Availability = "unknown"
		}
		if !item.Selectable {
			item.Reason = "Mihari's own TUN interface"
		}
		status.Interfaces = append(status.Interfaces, item)
		found = found || adapter.Name == saved
	}
	if saved != "" && !found {
		item := protocol.EgressInterface{Name: saved, Kind: "unknown", Availability: "not_found", Addresses: []string{}, Selectable: saved != own}
		if !item.Selectable {
			item.Reason = "Mihari's own TUN interface"
		}
		status.Interfaces = append(status.Interfaces, item)
	}
	sort.Slice(status.Interfaces, func(i, j int) bool { return status.Interfaces[i].Name < status.Interfaces[j].Name })
	return status, nil
}

func (m *Manager) egressPreviousConfig(ctx context.Context) ([]byte, error) {
	if m.trustedCore != nil {
		return m.trustedCore.PreviousConfig(ctx)
	}
	return os.ReadFile(m.runtimeConfig)
}

func (m *Manager) egressOwnTun(ctx context.Context, live map[string]any) string {
	if tun, ok := live["tun"].(map[string]any); ok {
		if name, ok := tun["device"].(string); ok && name != "" {
			m.egressTunName = name
			return name
		}
	}
	if content, err := m.egressPreviousConfig(ctx); err == nil {
		var document map[string]any
		if err := yaml.Unmarshal(content, &document); err == nil {
			if tun, ok := document["tun"].(map[string]any); ok {
				name, _ := tun["device"].(string)
				if name != "" {
					return name
				}
			}
		}
	}
	if m.subscriptions != nil {
		catalog := m.loggingCatalog()
		if catalog.ActiveID != "" {
			if _, document, err := m.subscriptions.ReadCache(catalog.ActiveID); err == nil {
				if data, err := yaml.Marshal(document["tun"]); err == nil {
					var tun struct{ Device string }
					if yaml.Unmarshal(data, &tun) == nil && tun.Device != "" {
						return tun.Device
					}
				}
			}
		}
	}
	return m.egressTunName
}

func validateEgressCandidate(status protocol.EgressStatus, selection protocol.EgressSelection) error {
	if !protocol.ValidEgressSelection(selection.Mode, selection.InterfaceName) {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "invalid outbound interface selection"}
	}
	if selection.Mode == "automatic" {
		return nil
	}
	for _, item := range status.Interfaces {
		if item.Name == selection.InterfaceName {
			if item.Selectable {
				return nil
			}
			return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "Mihari's own TUN cannot be used as outbound interface"}
		}
	}
	return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "outbound interface is not in the current or saved interface list"}
}

// UpdateEgress applies an instance-wide selection through the configuration transaction.
func (m *Manager) UpdateEgress(ctx context.Context, op Operation, selection protocol.EgressSelection) (protocol.EgressStatus, error) {
	if op.ID == "" {
		return protocol.EgressStatus{}, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "operation ID is required"}
	}
	result, err := m.doOperation(ctx, "egress:"+op.ID, func(ctx context.Context) (any, error) {
		candidate, generated, err := m.prepareEgress(ctx, op, selection)
		if err != nil {
			return nil, err
		}
		defer m.unlock()
		defer func() { collectWarning(ctx, "egress", "candidate.cleanup.failed", generated.cleanup()) }()
		if !candidate.changed {
			return m.egressStatus(ctx)
		}
		if !m.routingCoreStopped() {
			candidate, err = m.commitEgress(ctx, candidate, generated)
		} else {
			_, err = m.saveSettingsCandidate(ctx, candidate)
		}
		if err != nil {
			return nil, err
		}
		m.publishSettings(candidate)
		if _, err := m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{ID: op.ID, Source: op.Source}, func(s state.Snapshot) (state.Snapshot, error) {
			if !m.routingCoreStopped() {
				markConfigApplied(&s)
			}
			return s, nil
		}); err != nil {
			return nil, err
		}
		// Saving is already complete. A later enumeration/read error must not
		// turn that committed write into a purported failed transaction.
		status, readErr := m.egressStatus(ctx)
		if readErr != nil {
			collectWarning(ctx, "egress", "status.refresh.failed", readErr)
			status.Schema, status.Revision, status.Selection = "mihari/v1", m.store.Load().Revision, egressSelection(candidate.after.EgressInterface)
		}
		return status, nil
	})
	if err != nil {
		return protocol.EgressStatus{}, err
	}
	return result.(protocol.EgressStatus), nil
}

// prepareEgress returns mutation ownership only on success. Slow validation is
// outside that ownership; generation, source, epoch and selection are rechecked.
func (m *Manager) prepareEgress(ctx context.Context, op Operation, selection protocol.EgressSelection) (settingsCandidate, configCandidate, error) {
	if err := m.lockMutation(ctx); err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	owned, success := true, false
	defer func() {
		if owned && !success {
			m.unlock()
		}
	}()
	if err := m.checkIfRevision(op.IfRevision); err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	status, err := m.egressStatus(ctx)
	if err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	if err := validateEgressCandidate(status, selection); err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	candidate, err := m.prepareSettings(func(s *config.Settings) error { s.EgressInterface = selection.InterfaceName; return nil })
	if err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	if !candidate.changed || m.routingCoreStopped() {
		success = true
		return candidate, configCandidate{}, nil
	}
	if m.store.Load().Core.Status != "running" {
		return settingsCandidate{}, configCandidate{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "core is not ready for outbound interface changes"}
	}
	catalog, epoch := m.loggingCatalog(), m.coreEpoch
	hash, err := m.loggingCacheHash(catalog.ActiveID)
	if err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	m.unlock()
	owned = false
	generated, err := m.prepareCatalogConfigWithSettings(ctx, catalog, candidate.after, candidate.generation)
	if err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	defer func() {
		if !success {
			collectWarning(ctx, "egress", "candidate.cleanup.failed", generated.cleanup())
		}
	}()
	if err = m.lockMutation(ctx); err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	owned = true
	newHash, err := m.loggingCacheHash(catalog.ActiveID)
	if err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	if hash != newHash || candidate.generation != m.currentConfigGeneration() || epoch != m.coreEpoch || !reflect.DeepEqual(catalog, m.loggingCatalog()) || m.store.Load().Core.Status != "running" {
		return settingsCandidate{}, configCandidate{}, routingConflict()
	}
	if err = m.checkIfRevision(op.IfRevision); err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	status, err = m.egressStatus(ctx)
	if err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	if err = validateEgressCandidate(status, selection); err != nil {
		return settingsCandidate{}, configCandidate{}, err
	}
	success = true
	return candidate, generated, nil
}

func (m *Manager) commitEgress(ctx context.Context, candidate settingsCandidate, generated configCandidate) (settingsCandidate, error) {
	if m.loggingUnsaved {
		return candidate, loggingCoreError("save the observed core logging level before reloading configuration", nil)
	}
	previous, err := m.egressPreviousConfig(ctx)
	if err != nil {
		return candidate, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "capture previous outbound configuration"}, err)
	}
	before := candidate.before
	var oldMode, oldExit string
	if before.Routing != nil {
		oldMode, err = m.observeRoutingMode(ctx)
		if err != nil {
			return candidate, err
		}
		group, e := m.observeGlobal(ctx)
		if e != nil {
			return candidate, e
		}
		oldExit = group.Now
	}
	compensate := func(cause error) (settingsCandidate, error) {
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		restoreErr := m.restoreRoutingConfig(recovery, previous)
		if restoreErr == nil {
			restoreErr = m.confirmEgressConfig(recovery, previous)
		}
		if oldMode != "" {
			restoreErr = errors.Join(restoreErr, m.applyRoutingLive(recovery, oldMode, oldExit))
		}
		if !reflect.DeepEqual(before, m.settingsSnapshot()) {
			_, e := m.restoreSettings(recovery, before)
			restoreErr = errors.Join(restoreErr, e)
		}
		if restoreErr != nil {
			m.mutationDegraded.Store(true)
			m.stopCoreOnUnlock.Store(m.trustedCore != nil)
			cause = diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "outbound interface recovery could not be confirmed; restart required", Details: map[string]any{"degraded": true}}, errors.Join(cause, restoreErr))
			m.markConfigDegraded(recovery, cause)
		}
		return candidate, cause
	}
	if err = m.commitRuntimeConfig(ctx, generated); err != nil {
		return compensate(err)
	}
	if err = m.confirmEgressConfig(ctx, generated.content); err != nil {
		return compensate(err)
	}
	if err = m.controller.CloseAllConnections(ctx); err != nil {
		return compensate(diagnostics.Wrap(protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "close active connections after outbound change"}, err))
	}
	// Reload may restore a routing selection. Preserve that committed result.
	candidate, err = m.prepareSettings(func(s *config.Settings) error { s.EgressInterface = candidate.after.EgressInterface; return nil })
	if err != nil {
		return compensate(err)
	}
	if _, err = m.saveSettingsCandidate(ctx, candidate); err != nil {
		return compensate(err)
	}
	return candidate, nil
}

func (m *Manager) confirmEgressConfig(ctx context.Context, content []byte) error {
	var expected map[string]any
	if err := yaml.Unmarshal(content, &expected); err != nil {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "parse outbound configuration for confirmation"}, err)
	}
	live, err := m.controller.Configs(ctx)
	want, _ := expected["interface-name"].(string)
	actual, observed := live["interface-name"].(string)
	if err != nil || !observed || actual != want {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "outbound interface apply could not be confirmed"}, err)
	}
	return nil
}
