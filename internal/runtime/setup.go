package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

// setupPreparationChanged is only returned before a step has committed.
// It is deliberately distinct from public revision conflicts and rollback errors.
type setupPreparationChanged struct{ resource string }

func (e setupPreparationChanged) Error() string {
	return e.resource + " inputs changed during setup preparation"
}
func (e setupPreparationChanged) Unwrap() error {
	return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: e.Error()}
}

func preparationChanged(op Operation, resource, message string) error {
	if op.ExecutionMode == protocol.ExecutionModeSetup {
		return setupPreparationChanged{resource: resource}
	}
	return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: message}
}

func retrySetupPreparation(ctx context.Context, op Operation, work func() (any, error)) (any, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := work()
		var changed setupPreparationChanged
		if op.ExecutionMode != protocol.ExecutionModeSetup || !errors.As(err, &changed) {
			return result, err
		}
		if attempt == 2 {
			return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeRevisionConflict, Message: fmt.Sprintf("%s is still changing; your input and earlier setup steps are kept. Try again shortly.", changed.resource)}, err)
		}
	}
}

// commitSubscriptionCandidate reuses the fetched document while regenerating
// only the configuration invalidated by a real settings change.
func (m *Manager) commitSubscriptionCandidate(ctx context.Context, op Operation, prepare func() (subscription.Document, error), check func() error, commit func(state.Snapshot, configCandidate) (state.Snapshot, error)) error {
	degradedMarked := false
	_, err := retrySetupPreparation(ctx, op, func() (any, error) {
		document, err := prepare()
		if err != nil {
			return nil, err
		}
		candidate, err := m.prepareConfig(ctx, document)
		if err != nil {
			return nil, err
		}
		defer func() { collectWarning(ctx, "subscription", "candidate.cleanup.failed", candidate.cleanup()) }()
		if check != nil {
			if err := check(); err != nil {
				return nil, err
			}
		}
		if err := m.lockSetupMutation(ctx, op); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err := m.checkIfRevision(op.IfRevision); err != nil {
			return nil, err
		}
		if op.ExecutionMode == protocol.ExecutionModeSetup && candidate.generation != m.currentConfigGeneration() {
			return nil, setupPreparationChanged{resource: "Subscription configuration"}
		}
		_, err = m.updateStateLocked(ctx, state.CommandMeta{ID: op.ID, Source: op.Source, IfRevision: op.IfRevision}, func(s state.Snapshot) (state.Snapshot, error) { return commit(s, candidate) })
		var api protocol.APIError
		if op.ExecutionMode == protocol.ExecutionModeSetup && errors.As(err, &api) && api.Details["degraded"] == true {
			m.mutationDegraded.Store(true)
			m.stopCoreOnUnlock.Store(true)
			m.markConfigDegraded(ctx, err)
			degradedMarked = true
		}
		return nil, err
	})
	if !degradedMarked {
		m.markConfigDegraded(ctx, err)
	}
	return err
}

func coreVersionChannel(version string) string {
	if strings.HasPrefix(version, "alpha-") {
		return "alpha"
	}
	return "stable"
}

// lockSetupMutation waits for an already owned core update rather than forcing
// a second update or treating ordinary startup work as a revision conflict.
func (m *Manager) lockSetupMutation(ctx context.Context, op Operation) error {
	for {
		if op.ExecutionMode == protocol.ExecutionModeSetup && !m.ownsCoreUpdate(ctx) {
			if r := m.coreUpdate.Load(); r != nil && r.done != nil {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-r.done:
					continue
				}
			}
		}
		err := m.lockMutation(ctx)
		if err == nil || op.ExecutionMode != protocol.ExecutionModeSetup {
			return err
		}
		if r := m.coreUpdate.Load(); r == nil || r.done == nil {
			return err
		}
	}
}

// reuseSetupCore verifies that the same local object still satisfies the fixed
// channel target after acquiring ownership; lifecycle observations are irrelevant.
func (m *Manager) reuseSetupCore(ctx context.Context, op Operation, channel, bundle string) (core.InstallResult, bool, error) {
	result, err := retrySetupPreparation(ctx, op, func() (any, error) {
		before, err := os.Stat(m.installRequest.BinaryPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		version, err := m.installer.DetectVersion(ctx, m.installRequest.BinaryPath)
		if err != nil || version == "" || coreVersionChannel(version) != channel {
			return nil, nil
		}
		identity := m.coreConfigIdentity()
		document, err := m.setupCoreDocument(identity)
		if err != nil {
			return nil, err
		}
		configuration, err := m.prepareConfig(ctx, document)
		if err != nil {
			return nil, err
		}
		defer func() { collectWarning(ctx, "core", "candidate.cleanup.failed", configuration.cleanup()) }()
		if err := m.lockSetupMutation(ctx, op); err != nil {
			return nil, err
		}
		defer m.unlock()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := m.checkIfRevision(op.IfRevision); err != nil {
			return nil, err
		}
		if configuration.generation != m.currentConfigGeneration() || identity != m.coreConfigIdentity() {
			return nil, setupPreparationChanged{resource: "Local core configuration"}
		}
		after, err := os.Stat(m.installRequest.BinaryPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil, setupPreparationChanged{resource: "Local core"}
		}
		if err != nil {
			return nil, err
		}
		if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			return nil, setupPreparationChanged{resource: "Local core"}
		}
		candidate, err := m.prepareSettings(func(s *config.Settings) error { s.CoreChannel, s.CoreChannelBundle = channel, bundle; return nil })
		if err != nil {
			return nil, err
		}
		if _, err := m.saveSettingsCandidate(ctx, candidate); err != nil {
			return nil, err
		}
		m.publishSettings(candidate)
		current := m.store.Load().Core
		alphaSHA := ""
		if channel == "alpha" {
			alphaSHA = strings.TrimPrefix(version, "alpha-")
		}
		if candidate.changed || current.Version != version || current.Channel != channel || current.AlphaSHA != alphaSHA {
			_, err = m.updateStateLocked(context.WithoutCancel(ctx), state.CommandMeta{ID: op.ID, Source: op.Source, IfRevision: op.IfRevision}, func(s state.Snapshot) (state.Snapshot, error) {
				s.Core.Version, s.Core.Channel = version, channel
				s.Core.AlphaSHA = ""
				if channel == "alpha" {
					s.Core.AlphaSHA = strings.TrimPrefix(version, "alpha-")
				}
				return s, nil
			})
			if err != nil {
				return nil, err
			}
		}
		return core.InstallResult{Version: version}, nil
	})
	if err != nil {
		return core.InstallResult{}, false, err
	}
	if result == nil {
		return core.InstallResult{}, false, nil
	}
	return result.(core.InstallResult), true, nil
}

type coreConfigIdentity struct {
	active string
	config state.ConfigState
}

func (m *Manager) coreConfigIdentity() coreConfigIdentity {
	snapshot := m.store.Load()
	return coreConfigIdentity{active: snapshot.ActiveSubscription, config: snapshot.Config}
}

func (m *Manager) revalidateSetupCore(ctx context.Context, candidate PreparedCore) (uint64, coreConfigIdentity, error) {
	settings, generation := m.configInputs()
	identity := m.coreConfigIdentity()
	document, err := m.setupCoreDocument(identity)
	if err != nil {
		return 0, identity, err
	}
	content, err := subscription.Generate(document, nil, settings)
	if err != nil {
		return 0, identity, err
	}
	if m.trustedCore != nil {
		err = m.trustedCore.ValidateCandidateConfig(ctx, candidate, content)
	} else if validator, ok := candidate.(interface {
		Revalidate(context.Context, []byte) error
	}); ok {
		err = validator.Revalidate(ctx, content)
	} else {
		err = protocol.APIError{Code: protocol.CodeInvalidState, Message: "core inputs changed; candidate revalidation is unavailable"}
	}
	return generation, identity, err
}

func (m *Manager) setupCoreDocument(identity coreConfigIdentity) (subscription.Document, error) {
	if m.subscriptions != nil && identity.active != "" {
		_, doc, err := m.subscriptions.ReadCache(identity.active)
		return doc, err
	}
	return subscription.Document{"proxies": []any{}, "proxy-groups": []any{}, "rules": []any{"MATCH,DIRECT"}}, nil
}
