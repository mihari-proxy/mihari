package runtime

import (
	"context"
	"errors"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/subscription"
)

// resourceActivationOwner is read and written only under Manager maintenance.
// It spans unlocked core validation while conflicting mutations fail fast.
type resourceActivationOwner struct{}

type resourceActivationPlan interface {
	Begin(context.Context) (resourceActivationTransaction, error)
	Identity() (string, uint64)
}

type resourceActivationTransaction interface {
	Activate(context.Context) error
	Validate(context.Context) error
	Recheck(context.Context) error
	Publish(context.Context) error
	Finish(context.Context) error
	Restore(context.Context) error
	Close()
}

// activatePreparedResources is the fixed-H transaction seam consumed by the
// root entrypoints added in the next checkpoint. Preparation and Build happen
// before this call and therefore outside Manager mutation ownership.
func (m *Manager) activatePreparedResources(ctx context.Context, operation Operation, generation uint64, prepared *subscription.PreparedResources) error {
	if m.trustedCore == nil || prepared == nil {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "trusted resource activation unavailable"}
	}
	return m.activateResourcePlan(ctx, operation, generation, trustedResourcePlan{trusted: m.trustedCore, prepared: prepared})
}

type trustedResourcePlan struct {
	trusted  *core.TrustedExecution
	prepared *subscription.PreparedResources
}

func (p trustedResourcePlan) Begin(ctx context.Context) (resourceActivationTransaction, error) {
	execution, err := p.trusted.BeginResourceActivation(ctx)
	if err != nil {
		return nil, err
	}
	return &trustedResourceTransaction{execution: execution, prepared: p.prepared}, nil
}
func (p trustedResourcePlan) Identity() (string, uint64) {
	return p.prepared.Identity()
}

type trustedResourceTransaction struct {
	execution  *core.ResourceActivation
	prepared   *subscription.PreparedResources
	activation *subscription.ResourceActivation
}

func (t *trustedResourceTransaction) Activate(ctx context.Context) error {
	activation, err := t.prepared.Activate(ctx)
	if err == nil {
		t.activation = activation
	}
	return err
}
func (t *trustedResourceTransaction) Validate(ctx context.Context) error {
	return t.execution.Validate(ctx, t.activation)
}
func (t *trustedResourceTransaction) Recheck(ctx context.Context) error {
	return t.activation.Recheck(ctx)
}
func (t *trustedResourceTransaction) Publish(ctx context.Context) error {
	capability, err := t.execution.Publish(ctx, t.activation)
	if capability != nil {
		err = errors.Join(err, capability.Close())
	}
	return err
}
func (t *trustedResourceTransaction) Finish(ctx context.Context) error {
	return t.activation.Finish(ctx)
}
func (t *trustedResourceTransaction) Restore(ctx context.Context) error {
	capability, err := t.execution.Restore(ctx, t.activation)
	if capability != nil {
		err = errors.Join(err, capability.Close())
	}
	return err
}
func (t *trustedResourceTransaction) Close() { t.execution.Close() }

func (m *Manager) activateResourcePlan(ctx context.Context, operation Operation, generation uint64, plan resourceActivationPlan) error {
	maintenance, ok := m.supervisor.(interface {
		Maintain(context.Context, func() error) error
	})
	if !ok || plan == nil {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "trusted core maintenance unavailable"}
	}
	return maintenance.Maintain(ctx, func() error {
		tx, err := plan.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Close()

		owner := &resourceActivationOwner{}
		if err = m.lockMutation(ctx); err != nil {
			return err
		}
		locked := true
		if err = m.checkIfRevision(operation.IfRevision); err == nil {
			err = m.checkResourceActivation(generation, plan)
		}
		if err == nil {
			err = tx.Activate(ctx)
		}
		if err != nil {
			m.releaseMutation()
			return err
		}
		m.resourceActivation = owner
		m.releaseMutation()
		locked = false

		if err = tx.Validate(ctx); err != nil {
			return m.rollbackResourceActivation(ctx, owner, tx, err, locked)
		}
		if err = m.lockResourceActivation(ctx, owner); err != nil {
			return m.rollbackResourceActivation(ctx, owner, tx, err, false)
		}
		locked = true
		if err = m.checkResourceActivation(generation, plan); err == nil {
			err = tx.Recheck(ctx)
		}
		if err == nil {
			err = tx.Publish(ctx)
		}
		if err == nil {
			err = tx.Finish(ctx)
		}
		if err != nil {
			return m.rollbackResourceActivation(ctx, owner, tx, err, locked)
		}
		m.settingsMu.Lock()
		m.configGeneration++
		m.settingsMu.Unlock()
		m.resourceActivation = nil
		m.releaseMutation()
		return nil
	})
}

func (m *Manager) lockResourceActivation(ctx context.Context, owner *resourceActivationOwner) error {
	if err := m.lockMaintenance(ctx); err != nil {
		return err
	}
	if m.resourceActivation != owner {
		m.releaseMutation()
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "resource activation ownership changed"}
	}
	return nil
}

func (m *Manager) preflightResourceActivation(ctx context.Context) error {
	if err := m.lockMaintenance(ctx); err != nil {
		return err
	}
	active := m.resourceActivation != nil
	m.releaseMutation()
	if active {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "resource activation is in progress"}
	}
	return nil
}

func (m *Manager) checkResourceActivation(generation uint64, plan resourceActivationPlan) error {
	if generation != m.currentConfigGeneration() {
		return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "configuration inputs changed during activation"}
	}
	id, resourceGeneration := plan.Identity()
	if id == "" {
		return nil
	}
	if m.subscriptions == nil {
		return protocol.APIError{Code: protocol.CodeInvalidState, Message: "subscription manager is unavailable"}
	}
	catalog := m.subscriptions.Snapshot()
	if catalog.ActiveID != id {
		return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "active subscription changed during activation"}
	}
	for _, profile := range catalog.Profiles {
		if profile.ID == id && profile.Generation == resourceGeneration {
			return nil
		}
	}
	return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "subscription generation changed during activation"}
}

func (m *Manager) rollbackResourceActivation(ctx context.Context, owner *resourceActivationOwner, tx resourceActivationTransaction, cause error, locked bool) error {
	ctx = context.WithoutCancel(ctx)
	if !locked {
		if err := m.lockResourceActivation(ctx, owner); err != nil {
			m.mutationDegraded.Store(true)
			return resourceActivationDegraded()
		}
	}
	restoreErr := tx.Restore(ctx)
	if restoreErr == nil {
		m.resourceActivation = nil
		m.releaseMutation()
		return cause
	}
	m.mutationDegraded.Store(true)
	m.releaseMutation()
	return resourceActivationDegraded()
}

func resourceActivationDegraded() error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: "resource activation recovery could not be confirmed", Details: map[string]any{"degraded": true}}
}
