//go:build darwin

package platform

import (
	"context"
	"errors"
)

// OpenLaunchdRuntimeControl opens the fixed global B/launchd-runtime control
// without creating or repairing any directory or file.
func OpenLaunchdRuntimeControl(ctx context.Context) (_ *LaunchdRuntimeControl, err error) {
	base, err := OpenTrustedRoot(ctx, platformLayoutDefaults("").BaseDir, RootPolicy{Owner: 0, Mode: 0711})
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, base.Close()) }()
	return openUnixLaunchdRuntimeControlAt(ctx, base)
}

// InitializeLaunchdRuntimeControl atomically publishes the fixed global
// runtime directory. A live global installation lease is required.
func InitializeLaunchdRuntimeControl(ctx context.Context, lease *OwnedInstallLease, initial []byte) (_ *LaunchdRuntimeControl, publication InstallPublication, err error) {
	finish, err := beginInstallControlUnixLease(ctx, lease)
	if err != nil {
		return nil, publication, err
	}
	defer finish()
	base, validate, err := installControlUnixBaseFromHeldLease(ctx, lease)
	if err != nil {
		return nil, publication, err
	}
	defer func() { err = errors.Join(err, base.Close()) }()
	return initializeUnixLaunchdRuntimeControlAt(ctx, base, initial, validate)
}
