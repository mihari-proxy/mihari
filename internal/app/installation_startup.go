package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
)

// InstallationStartupStore exposes a read and durability confirmation, never installation mutation.
type InstallationStartupStore interface {
	ReadState(context.Context) ([]byte, string, error)
	ConfirmState(context.Context, string) error
}

// ConfirmInstallationStartup proves a complete installation before business owners start.
// The caller holds the platform startup gate and supplies a verifier that binds
// the current executable and selected data/endpoint scope to the manifest.
func ConfirmInstallationStartup(ctx context.Context, store InstallationStartupStore, verify func(context.Context, InstallationManifest) error) (InstallationManifest, error) {
	if err := ctx.Err(); err != nil {
		return InstallationManifest{}, err
	}
	if store == nil || verify == nil {
		return InstallationManifest{}, ErrInstallationObservationUnknown
	}
	raw, digest, err := store.ReadState(ctx)
	if err != nil {
		return InstallationManifest{}, err
	}
	if digest != fmt.Sprintf("%x", sha256.Sum256(raw)) {
		return InstallationManifest{}, ErrInstallationObservationUnknown
	}
	state, err := DecodeInstallationState(bytes.NewReader(raw))
	if err != nil {
		return InstallationManifest{}, err
	}
	if state.State != InstallationStateComplete || !state.Target.Installed {
		return InstallationManifest{}, ErrInstallationObservationUnknown
	}
	if err := store.ConfirmState(ctx, digest); err != nil {
		return InstallationManifest{}, err
	}
	if err := verify(ctx, state.Target); err != nil {
		return InstallationManifest{}, err
	}
	latest, version, err := store.ReadState(ctx)
	if err != nil {
		return InstallationManifest{}, err
	}
	if version != digest || !bytes.Equal(raw, latest) {
		return InstallationManifest{}, ErrInstallationObservationUnknown
	}
	// Decode anew so the first verifier cannot accidentally change the target
	// through one of the manifest's pointer fields.
	state, err = DecodeInstallationState(bytes.NewReader(latest))
	if err != nil {
		return InstallationManifest{}, err
	}
	if err := verify(ctx, state.Target); err != nil {
		return InstallationManifest{}, err
	}
	if err := ctx.Err(); err != nil {
		return InstallationManifest{}, err
	}
	state, err = DecodeInstallationState(bytes.NewReader(latest))
	return state.Target, err
}
