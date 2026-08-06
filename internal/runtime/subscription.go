package runtime

import (
	"context"
	"errors"
	"os"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

type AddSubscriptionInput struct {
	Name string
	URL  string
}

type SetSubscriptionInput struct {
	Name         *string
	URL          *string
	Interval     *string
	AutoRefresh  *bool
	GlobalPeriod *string
}

type configCandidate struct {
	path    string
	content []byte
}

type configReloader interface {
	Reload(context.Context, string, bool) error
}

func (m *Manager) Subscriptions() subscription.PublicCatalog {
	if m.subscriptions == nil {
		return subscription.PublicCatalog{Profiles: []subscription.PublicProfile{}}
	}
	return m.subscriptions.Snapshot().Public()
}

func (m *Manager) AddSubscription(ctx context.Context, operation Operation, input AddSubscriptionInput) (subscription.PublicProfile, error) {
	result, err := m.doOperation(ctx, "sub-add:"+operation.ID, func() (any, error) {
		if m.subscriptions == nil {
			return nil, subscriptionsUnavailable()
		}
		if err := m.lock(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		var added subscription.Profile
		_, err := m.coordinator.Do(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			var addErr error
			added, addErr = m.subscriptions.Add(input.Name, input.URL)
			if addErr != nil {
				return snapshot, addErr
			}
			m.syncSubscriptionState(&snapshot, m.subscriptions.Snapshot())
			return snapshot, nil
		})
		if err != nil {
			return nil, err
		}
		catalog := m.subscriptions.Snapshot().Public()
		for _, profile := range catalog.Profiles {
			if profile.ID == added.ID {
				return profile, nil
			}
		}
		return nil, subscriptionsUnavailable()
	})
	if err != nil {
		return subscription.PublicProfile{}, err
	}
	profile := result.(subscription.PublicProfile)
	// Pull once immediately so the profile does not sit in Missing until a manual refresh.
	// Registration is already durable: fetch failures keep the profile and surface last_error.
	refreshed, refreshErr := m.RefreshSubscription(ctx, Operation{
		ID:     operation.ID + "-fetch",
		Source: operation.Source,
	}, profile.ID)
	if refreshErr != nil {
		if current, findErr := findPublicProfile(m.subscriptions.Snapshot().Public(), profile.ID); findErr == nil {
			return current, nil
		}
		return profile, nil
	}
	return refreshed, nil
}

func (m *Manager) RefreshSubscription(ctx context.Context, operation Operation, id string) (subscription.PublicProfile, error) {
	result, err := m.doOperation(ctx, "sub-refresh:"+operation.ID, func() (any, error) {
		if m.subscriptions == nil {
			return nil, subscriptionsUnavailable()
		}
		prepared, err := m.subscriptions.PrepareRefresh(ctx, id)
		if err != nil {
			return nil, err
		}
		candidate, err := m.prepareConfig(ctx, prepared.Document())
		if err != nil {
			return nil, err
		}
		defer os.Remove(candidate.path)
		if err := m.lock(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		_, err = m.coordinator.Do(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			receipt, commitErr := m.subscriptions.CommitRefresh(prepared)
			if commitErr != nil {
				return snapshot, commitErr
			}
			if receipt.After.ActiveID == id {
				if applyErr := m.commitRuntimeConfig(ctx, candidate.content); applyErr != nil {
					if rollbackErr := m.subscriptions.Rollback(receipt); rollbackErr != nil {
						return snapshot, degradedConfigError()
					}
					return snapshot, applyErr
				}
				markConfigApplied(&snapshot)
			}
			m.syncSubscriptionState(&snapshot, receipt.After)
			return snapshot, nil
		})
		if err != nil {
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

func (m *Manager) UseSubscription(ctx context.Context, operation Operation, id string) (subscription.PublicProfile, error) {
	result, err := m.doOperation(ctx, "sub-use:"+operation.ID, func() (any, error) {
		if m.subscriptions == nil {
			return nil, subscriptionsUnavailable()
		}
		catalog := m.subscriptions.Snapshot()
		index := catalog.Index(id)
		if index < 0 || !catalog.Profiles[index].Enabled || catalog.Profiles[index].Generation == 0 {
			return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "subscription is disabled or has no valid cache"}
		}
		capturedVersion := catalog.Profiles[index].Version
		_, document, err := m.subscriptions.ReadCache(id)
		if err != nil {
			return nil, err
		}
		candidate, err := m.prepareConfig(ctx, document)
		if err != nil {
			return nil, err
		}
		defer os.Remove(candidate.path)
		if err := m.lock(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		_, err = m.coordinator.Do(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			current := m.subscriptions.Snapshot()
			currentIndex := current.Index(id)
			if currentIndex < 0 || current.Profiles[currentIndex].Version != capturedVersion {
				return snapshot, protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "subscription changed while activation was in progress"}
			}
			before, after, mutateErr := m.subscriptions.Mutate(func(next *subscription.Catalog) error {
				next.ActiveID = id
				return nil
			})
			if mutateErr != nil {
				return snapshot, mutateErr
			}
			if applyErr := m.commitRuntimeConfig(ctx, candidate.content); applyErr != nil {
				if restoreErr := m.subscriptions.Restore(before); restoreErr != nil {
					return snapshot, degradedConfigError()
				}
				return snapshot, applyErr
			}
			markConfigApplied(&snapshot)
			m.syncSubscriptionState(&snapshot, after)
			return snapshot, nil
		})
		if err != nil {
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

func (m *Manager) RemoveSubscription(ctx context.Context, operation Operation, id string) error {
	_, err := m.doOperation(ctx, "sub-remove:"+operation.ID, func() (any, error) {
		if m.subscriptions == nil {
			return nil, subscriptionsUnavailable()
		}
		if err := m.lock(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		_, err := m.coordinator.Do(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			before, after, mutateErr := m.subscriptions.Mutate(func(next *subscription.Catalog) error {
				index := next.Index(id)
				if index < 0 {
					return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "subscription not found"}
				}
				next.Profiles = append(next.Profiles[:index], next.Profiles[index+1:]...)
				return nil
			})
			if mutateErr != nil {
				return snapshot, mutateErr
			}
			if before.ActiveID == id {
				candidate, prepareErr := m.prepareCatalogConfig(ctx, after)
				if prepareErr != nil {
					_ = m.subscriptions.Restore(before)
					return snapshot, prepareErr
				}
				defer os.Remove(candidate.path)
				if applyErr := m.commitRuntimeConfig(ctx, candidate.content); applyErr != nil {
					if restoreErr := m.subscriptions.Restore(before); restoreErr != nil {
						return snapshot, degradedConfigError()
					}
					return snapshot, applyErr
				}
				markConfigApplied(&snapshot)
			}
			m.syncSubscriptionState(&snapshot, after)
			return snapshot, nil
		})
		if err != nil {
			m.markConfigDegraded(ctx, err)
			return nil, err
		}
		_ = os.Remove(m.subscriptions.CachePath(id))
		return struct{}{}, nil
	})
	return err
}

func (m *Manager) SetSubscriptionEnabled(ctx context.Context, operation Operation, id string, enabled bool) (subscription.PublicProfile, error) {
	return m.mutateSubscription(ctx, "sub-enabled:", operation, id, func(catalog *subscription.Catalog, profile *subscription.Profile) error {
		profile.Enabled = enabled
		profile.Version++
		if !enabled && catalog.ActiveID == profile.ID {
			catalog.ActiveID = ""
		}
		return nil
	})
}

func (m *Manager) SetSubscription(ctx context.Context, operation Operation, id string, input SetSubscriptionInput) (subscription.PublicProfile, error) {
	return m.mutateSubscription(ctx, "sub-set:", operation, id, func(catalog *subscription.Catalog, profile *subscription.Profile) error {
		if input.Name != nil {
			profile.Name = *input.Name
		}
		if input.Interval != nil {
			profile.Interval = *input.Interval
		}
		if input.AutoRefresh != nil {
			profile.AutoRefresh = *input.AutoRefresh
		}
		if input.GlobalPeriod != nil {
			catalog.GlobalInterval = *input.GlobalPeriod
		}
		if input.URL != nil && *input.URL != profile.URL {
			profile.URL = *input.URL
			profile.Generation = 0
			profile.UpdatedAt = subscription.Profile{}.UpdatedAt
			profile.ETag = ""
			profile.LastModified = ""
			if catalog.ActiveID == profile.ID {
				catalog.ActiveID = ""
			}
		}
		profile.Version++
		return nil
	})
}

func (m *Manager) mutateSubscription(ctx context.Context, prefix string, operation Operation, id string, mutate func(*subscription.Catalog, *subscription.Profile) error) (subscription.PublicProfile, error) {
	result, err := m.doOperation(ctx, prefix+operation.ID, func() (any, error) {
		if m.subscriptions == nil {
			return nil, subscriptionsUnavailable()
		}
		if err := m.lock(ctx); err != nil {
			return nil, err
		}
		defer m.unlock()
		_, err := m.coordinator.Do(ctx, state.CommandMeta{ID: operation.ID, Source: operation.Source, IfRevision: operation.IfRevision}, func(snapshot state.Snapshot) (state.Snapshot, error) {
			before, after, mutateErr := m.subscriptions.Mutate(func(next *subscription.Catalog) error {
				index := next.Index(id)
				if index < 0 {
					return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "subscription not found"}
				}
				return mutate(next, &next.Profiles[index])
			})
			if mutateErr != nil {
				return snapshot, mutateErr
			}
			if before.ActiveID != after.ActiveID {
				candidate, prepareErr := m.prepareCatalogConfig(ctx, after)
				if prepareErr != nil {
					_ = m.subscriptions.Restore(before)
					return snapshot, prepareErr
				}
				defer os.Remove(candidate.path)
				if applyErr := m.commitRuntimeConfig(ctx, candidate.content); applyErr != nil {
					if restoreErr := m.subscriptions.Restore(before); restoreErr != nil {
						return snapshot, degradedConfigError()
					}
					return snapshot, applyErr
				}
				markConfigApplied(&snapshot)
			}
			m.syncSubscriptionState(&snapshot, after)
			return snapshot, nil
		})
		if err != nil {
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

func (m *Manager) prepareCatalogConfig(ctx context.Context, catalog subscription.Catalog) (configCandidate, error) {
	if catalog.ActiveID == "" {
		content, err := core.BootstrapConfig(m.settings)
		if err != nil {
			return configCandidate{}, err
		}
		return m.prepareContent(ctx, content)
	}
	_, document, err := m.subscriptions.ReadCache(catalog.ActiveID)
	if err != nil {
		return configCandidate{}, err
	}
	return m.prepareConfig(ctx, document)
}

func (m *Manager) prepareConfig(ctx context.Context, document subscription.Document) (configCandidate, error) {
	content, err := subscription.Generate(document, nil, m.settings)
	if err != nil {
		return configCandidate{}, err
	}
	return m.prepareContent(ctx, content)
}

func (m *Manager) prepareContent(ctx context.Context, content []byte) (configCandidate, error) {
	if err := os.MkdirAll(m.stagingDir, 0o700); err != nil {
		return configCandidate{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "create subscription staging directory"}
	}
	file, err := os.CreateTemp(m.stagingDir, "config-*.yaml")
	if err != nil {
		return configCandidate{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "create generated configuration candidate"}
	}
	path := file.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	if chmodErr := file.Chmod(0o600); chmodErr != nil {
		_ = file.Close()
		err = chmodErr
		return configCandidate{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "secure generated configuration candidate"}
	}
	if _, writeErr := file.Write(content); writeErr != nil {
		_ = file.Close()
		err = writeErr
		return configCandidate{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "write generated configuration candidate"}
	}
	if syncErr := file.Sync(); syncErr != nil {
		_ = file.Close()
		err = syncErr
		return configCandidate{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "sync generated configuration candidate"}
	}
	if closeErr := file.Close(); closeErr != nil {
		err = closeErr
		return configCandidate{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "close generated configuration candidate"}
	}
	if m.validateConfig != nil {
		if validateErr := m.validateConfig(ctx, path); validateErr != nil {
			_ = os.Remove(path)
			return configCandidate{}, validateErr
		}
	}
	return configCandidate{path: path, content: content}, nil
}

func (m *Manager) commitRuntimeConfig(ctx context.Context, content []byte) error {
	previous, err := os.ReadFile(m.runtimeConfig)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "read previous runtime configuration"}
	}
	hadPrevious := err == nil
	if err := config.AtomicWrite(m.runtimeConfig, content, 0o600); err != nil {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "install generated runtime configuration"}
	}
	reloader, ok := m.controller.(configReloader)
	if !ok {
		_ = restoreRuntimeConfig(m.runtimeConfig, previous, hadPrevious)
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "mihomo reload is unavailable"}
	}
	if err := reloader.Reload(ctx, m.runtimeConfig, true); err == nil {
		return nil
	}
	restoreErr := restoreRuntimeConfig(m.runtimeConfig, previous, hadPrevious)
	reloadErr := reloader.Reload(ctx, m.runtimeConfig, true)
	if restoreErr != nil || reloadErr != nil {
		return protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "mihomo reload failed and rollback could not be confirmed", Details: map[string]any{"degraded": true}}
	}
	return protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "mihomo rejected generated configuration; previous configuration restored"}
}

func restoreRuntimeConfig(path string, previous []byte, existed bool) error {
	if existed {
		return config.AtomicWrite(path, previous, 0o600)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (m *Manager) syncSubscriptionState(snapshot *state.Snapshot, catalog subscription.Catalog) {
	public := catalog.Public()
	snapshot.ActiveSubscription = public.ActiveID
	snapshot.Subscriptions = make([]state.SubscriptionState, 0, len(public.Profiles))
	for _, profile := range public.Profiles {
		snapshot.Subscriptions = append(snapshot.Subscriptions, state.SubscriptionState{
			ID: profile.ID, Name: profile.Name, Enabled: profile.Enabled, AutoRefresh: profile.AutoRefresh,
			Interval: profile.Interval, Cached: profile.Cached, Generation: profile.Generation,
			UpdatedAt: profile.UpdatedAt, LastError: profile.LastError,
		})
	}
}

func findPublicProfile(catalog subscription.PublicCatalog, id string) (subscription.PublicProfile, error) {
	for _, profile := range catalog.Profiles {
		if profile.ID == id {
			return profile, nil
		}
	}
	return subscription.PublicProfile{}, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "subscription not found"}
}

func subscriptionsUnavailable() error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: "subscription manager is unavailable"}
}

func markConfigApplied(snapshot *state.Snapshot) {
	nextRevision := snapshot.Revision + 1
	snapshot.Config = state.ConfigState{Status: "ok", DesiredRevision: nextRevision, ObservedRevision: nextRevision}
}

func (m *Manager) markConfigDegraded(ctx context.Context, err error) {
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Details == nil || apiError.Details["degraded"] != true {
		return
	}
	_, _ = m.coordinator.Do(ctx, state.CommandMeta{Source: "runtime"}, func(snapshot state.Snapshot) (state.Snapshot, error) {
		snapshot.Health = "degraded"
		snapshot.Config = state.ConfigState{
			Status: "degraded", DesiredRevision: snapshot.Revision + 1,
			ObservedRevision: snapshot.Revision, LastError: "generated configuration rollback could not be confirmed",
		}
		return snapshot, nil
	})
}

func degradedConfigError() error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: "subscription state rollback failed", Details: map[string]any{"degraded": true}}
}
