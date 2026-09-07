package runtime

import (
	"context"
	"reflect"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

func (m *Manager) recheckSettingsCandidate(candidate settingsCandidate) error {
	if !reflect.DeepEqual(m.settingsSnapshot(), candidate.before) {
		return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "configuration inputs changed during activation"}
	}
	return nil
}

func (c *settingsResourceChange) PrepareLocked(ctx context.Context, p *subscription.PreparedResources) error {
	if err := c.RecheckLocked(); err != nil {
		return err
	}
	return p.StageSettings(ctx, c.manager.settingsPath, c.candidate.before, c.candidate.after)
}
func (c *settingsResourceChange) RecheckLocked() error {
	return c.manager.recheckSettingsCandidate(c.candidate)
}
func (c *settingsResourceChange) PublishLocked() { c.manager.publishSettings(c.candidate) }

func (c *subscriptionUseChange) PrepareLocked(ctx context.Context, p *subscription.PreparedResources) error {
	id, generation := p.Identity()
	if id != c.id {
		return resourceActivationDegraded()
	}
	var err error
	c.durable, err = c.manager.subscriptions.StageUse(ctx, p, id, generation)
	return err
}
func (c *subscriptionUseChange) RecheckLocked() error { return c.durable.Recheck() }
func (c *subscriptionUseChange) PublishLocked()       { c.after = c.durable.Publish() }

func (c *subscriptionRefreshChange) PrepareLocked(ctx context.Context, p *subscription.PreparedResources) error {
	id, generation := p.Identity()
	if id != c.wantID || generation != c.wantGen {
		return resourceActivationDegraded()
	}
	var err error
	c.durable, err = c.manager.subscriptions.StageRefresh(ctx, p, c.prepared, id, generation)
	return err
}
func (c *subscriptionRefreshChange) RecheckLocked() error { return c.durable.Recheck() }
func (c *subscriptionRefreshChange) PublishLocked()       { c.receipt.After = c.durable.Publish() }

func (c *onboardingResourceChange) PrepareLocked(ctx context.Context, p *subscription.PreparedResources) error {
	if err := c.manager.recheckSettingsCandidate(c.candidate); err != nil {
		return err
	}
	var err error
	c.durable, err = c.manager.onboarding.PrepareActivation(c.beforeState, c.update.Complete)
	if err != nil {
		return err
	}
	if err = p.StageSettings(ctx, c.manager.settingsPath, c.candidate.before, c.candidate.after); err != nil {
		return err
	}
	return p.StageOnboarding(ctx, c.durable)
}
func (c *onboardingResourceChange) RecheckLocked() error {
	if err := c.manager.recheckSettingsCandidate(c.candidate); err != nil {
		return err
	}
	return c.durable.Recheck()
}
func (c *onboardingResourceChange) PublishLocked() {
	c.manager.publishSettings(c.candidate)
	c.durable.Publish()
	c.manager.onboardingRestartRequired = c.beforeRestart || c.candidate.changed
}
