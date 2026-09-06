package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"sync"

	"github.com/mihari-proxy/mihari/internal/subscription"
)

// ResourceActivation owns core execution while fixed home resources are staged.
// The supervisor must already have stopped and joined the managed child.
type ResourceActivation struct {
	owner         *TrustedExecution
	release       func()
	once          sync.Once
	mu            sync.Mutex
	closed        bool
	validated     *subscription.ResourceActivation
	associated    *subscription.ResourceActivation
	validatedHash [sha256.Size]byte
	published     bool
	previousHash  [sha256.Size]byte
	previousReady bool
}

// BeginResourceActivation excludes commands and core installation until Close.
func (t *TrustedExecution) BeginResourceActivation(ctx context.Context) (*ResourceActivation, error) {
	if t == nil || t.store == nil || t.files == nil {
		return nil, dataFailure("trusted activation unavailable")
	}
	release, err := t.store.coreStore().execution().acquire(ctx)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	previousHash, previousReady := t.committed, t.ready
	t.mu.Unlock()
	return &ResourceActivation{owner: t, release: release, previousHash: previousHash, previousReady: previousReady}, nil
}

// Close releases execution after publication or complete rollback has converged.
func (a *ResourceActivation) Close() {
	if a == nil {
		return
	}
	a.once.Do(func() {
		a.mu.Lock()
		a.closed = true
		a.mu.Unlock()
		a.release()
	})
}

// Validate checks the exact policy-generated config against the fixed resource home.
func (a *ResourceActivation) Validate(ctx context.Context, resources *subscription.ResourceActivation) error {
	if resources == nil {
		return dataFailure("resource activation unavailable")
	}
	path, identity, err := resources.StoreBinding()
	if err != nil {
		return err
	}
	if err = a.checkBinding(path, identity); err != nil {
		return err
	}
	a.mu.Lock()
	if a.closed || a.published || a.associated != nil && a.associated != resources {
		a.mu.Unlock()
		return dataFailure("trusted resource activation unavailable")
	}
	a.associated = resources
	a.mu.Unlock()
	b, hash, path, identity, err := resources.ValidationConfig(ctx)
	if err != nil {
		return err
	}
	if err = a.validateOwned(ctx, b, hash, path, identity); err != nil {
		return err
	}
	after, afterHash, afterPath, afterIdentity, err := resources.ValidationConfig(ctx)
	if err != nil {
		return err
	}
	if hash != afterHash || path != afterPath || identity != afterIdentity || !bytes.Equal(b, after) {
		return dataFailure("resource activation changed during validation")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.published {
		return dataFailure("trusted resource activation unavailable")
	}
	a.validated, a.validatedHash = resources, hash
	return nil
}

func (a *ResourceActivation) validateOwned(ctx context.Context, content []byte, expected [sha256.Size]byte, path, identity string) error {
	if a == nil || a.owner == nil || a.owner.store == nil || a.owner.files == nil || sha256.Sum256(content) != expected {
		return dataFailure("trusted resource activation unavailable")
	}
	if err := a.checkBinding(path, identity); err != nil {
		return err
	}
	capability, err := a.owner.files.prepare(ctx, content)
	if err != nil {
		return err
	}
	cleanup := func() error {
		return errors.Join(a.owner.files.remove(context.WithoutCancel(ctx), capability), capability.Close())
	}
	installed, err := OpenInstalledCore(ctx, a.owner.store)
	if err != nil {
		return errors.Join(err, cleanup())
	}
	_, executeErr := executeVerifiedOwned(ctx, installed, CoreValidate, capability, a.owner.executor)
	return errors.Join(executeErr, installed.Close(), cleanup())
}

// Publish performs the WAL-owned config rename and binds the resulting fixed
// file as the only configuration capability accepted by future core starts.
func (a *ResourceActivation) Publish(ctx context.Context, resources *subscription.ResourceActivation) (*ConfigCapability, error) {
	if a == nil || resources == nil {
		return nil, dataFailure("resource activation unavailable")
	}
	a.mu.Lock()
	if a.closed || a.published || a.validated != resources {
		a.mu.Unlock()
		return nil, dataFailure("resource activation was not validated")
	}
	hash := a.validatedHash
	a.mu.Unlock()
	b, currentHash, path, identity, err := resources.ValidationConfig(ctx)
	if err != nil {
		return nil, err
	}
	if currentHash != hash || sha256.Sum256(b) != hash {
		return nil, dataFailure("validated configuration changed before publication")
	}
	if err = a.checkBinding(path, identity); err != nil {
		return nil, err
	}
	if err = resources.PublishConfig(ctx, hash); err != nil {
		return nil, err
	}
	capability, err := a.owner.files.bind(ctx, hash)
	if err != nil {
		return nil, err
	}
	a.owner.mu.Lock()
	a.owner.committed, a.owner.ready = hash, true
	a.owner.mu.Unlock()
	a.mu.Lock()
	a.published = true
	a.mu.Unlock()
	return capability, nil
}

// Restore rolls the typed WAL back as one set and rebinds the exact previous
// config before execution ownership is released. Cancellation cannot detach it.
func (a *ResourceActivation) Restore(ctx context.Context, resources *subscription.ResourceActivation) (*ConfigCapability, error) {
	if a == nil || resources == nil {
		return nil, dataFailure("resource activation unavailable")
	}
	ctx = context.WithoutCancel(ctx)
	path, identity, err := resources.StoreBinding()
	if err != nil {
		return nil, err
	}
	if err = a.checkBinding(path, identity); err != nil {
		return nil, err
	}
	a.mu.Lock()
	associated := a.associated
	a.mu.Unlock()
	if associated != nil && associated != resources {
		return nil, dataFailure("resource activation instance changed")
	}
	if err = resources.Restore(ctx); err != nil {
		return nil, err
	}
	if !a.previousReady {
		if _, readErr := a.owner.files.read(ctx); readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return nil, readErr
		}
		a.owner.mu.Lock()
		a.owner.ready = false
		a.owner.committed = [sha256.Size]byte{}
		a.owner.mu.Unlock()
		return nil, nil
	}
	capability, err := a.owner.files.bind(ctx, a.previousHash)
	if err != nil {
		return nil, err
	}
	a.owner.mu.Lock()
	a.owner.committed, a.owner.ready = a.previousHash, true
	a.owner.mu.Unlock()
	return capability, nil
}

func (a *ResourceActivation) checkBinding(path, identity string) error {
	if a == nil || a.owner == nil || a.owner.store == nil {
		return dataFailure("trusted resource activation unavailable")
	}
	bound, ok := a.owner.store.coreStore().(interface{ bindingIdentity() string })
	if !ok || path != a.owner.store.coreStore().location() || identity == "" || identity != bound.bindingIdentity() {
		return dataFailure("resource activation belongs to a different data root")
	}
	return nil
}
