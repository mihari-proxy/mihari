//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
)

// BindInstallLayout keeps the already held global lease while recovery resolves
// its private P from a verified private backup. It never reacquires B or releases
// an earlier P; all added locks remain owned by this same outer lease.
func (l *OwnedInstallLease) BindInstallLayout(ctx context.Context, layout ResolvedLayout) error {
	return l.bindInstallLayout(ctx, layout, platformLayoutDefaults(""), nativeLeaseRoot)
}

func (l *OwnedInstallLease) bindInstallLayout(ctx context.Context, layout ResolvedLayout, defaults LayoutDefaults, open leaseRootOpener) (err error) {
	if l == nil || l.state == nil {
		return os.ErrClosed
	}
	s := l.state
	done, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer done()
	if !s.global || s.owner != 0 {
		return os.ErrPermission
	}
	if err := s.validate(s.layout); err != nil {
		return err
	}
	if err := validateLeaseLayout(layout, 0, defaults); err != nil {
		return err
	}
	previous := s.layout
	rootCount, lockCount := len(s.roots), len(s.locks)
	defer func() {
		if err == nil {
			return
		}
		for i := len(s.locks) - 1; i >= lockCount; i-- {
			err = errors.Join(err, s.locks[i].close())
		}
		for i := len(s.roots) - 1; i >= rootCount; i-- {
			err = errors.Join(err, s.roots[i].Close())
		}
		s.locks, s.roots, s.layout = s.locks[:lockCount], s.roots[:rootCount], previous
	}()
	if layout.Mode == PrivateMode {
		already := false
		for _, root := range s.roots {
			path, _, _, _, err := root.Snapshot(ctx)
			if err != nil {
				return err
			}
			if path == layout.BaseDir {
				already = true
			}
		}
		if !already {
			private, err := open(ctx, layout.BaseDir, RootPolicy{Owner: 0, Mode: 0700}, false)
			if err != nil {
				return err
			}
			for _, root := range s.roots {
				if rootsOverlap(root, private) {
					return errors.Join(os.ErrInvalid, private.Close())
				}
			}
			lock, err := acquireRootLock(ctx, private, "install.lock")
			if err != nil {
				return errors.Join(err, private.Close())
			}
			s.roots = append(s.roots, private)
			s.locks = append(s.locks, lock)
		}
	}
	s.layout = layout
	return s.validate(layout)
}
