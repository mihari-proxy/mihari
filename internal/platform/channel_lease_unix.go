//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
)

// AcquireChannelLease serializes fixed channel metadata with installations.
// Private maintenance owns only P/install.lock, including when invoked as root.
// It grants no data/endpoint or named-service lifecycle authority.
func AcquireChannelLease(ctx context.Context, layout ResolvedLayout) (*OwnedInstallLease, error) {
	return acquireChannelLease(ctx, layout, uint32(os.Geteuid()), platformLayoutDefaults(""), nativeLeaseRoot)
}
func acquireChannelLease(ctx context.Context, layout ResolvedLayout, owner uint32, defaults LayoutDefaults, open leaseRootOpener) (_ *OwnedInstallLease, err error) {
	if err = validateLeaseLayout(layout, owner, defaults); err != nil {
		return nil, err
	}
	mode := uint32(0700)
	if layout.Mode == SystemMode {
		if owner != 0 {
			return nil, os.ErrPermission
		}
		mode = 0711
	}
	s := &leaseState{layout: layout, owner: owner}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.close())
		}
	}()
	root, err := open(ctx, layout.BaseDir, RootPolicy{Owner: owner, Mode: mode, AllowCreate: true}, false)
	if err != nil {
		return nil, err
	}
	s.roots = append(s.roots, root)
	if layout.Mode == PrivateMode && owner == 0 {
		// Only inspect existing B; private channel maintenance must never create
		// or lock it. Retain the identity proof until this lease is released.
		system, openErr := open(ctx, defaults.BaseDir, RootPolicy{Owner: 0, Mode: 0755}, true)
		if openErr != nil && !errors.Is(openErr, os.ErrNotExist) {
			return nil, openErr
		}
		if openErr == nil {
			s.roots = append(s.roots, system)
			if rootsOverlap(root, system) {
				return nil, os.ErrInvalid
			}
		}
	}
	if err = s.addLock(ctx, root, "install.lock"); err != nil {
		return nil, err
	}
	if err = s.validate(layout); err != nil {
		return nil, err
	}
	return &OwnedInstallLease{state: s}, nil
}
