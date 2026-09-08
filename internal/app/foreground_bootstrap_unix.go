//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"os"
)

type foregroundInstallLease struct {
	lock    *platform.OwnedInstallLease
	root    *platform.TrustedRoot
	layout  platform.ResolvedLayout
	session *nativeInstallSession
}

func (l *foregroundInstallLease) Validate(ctx context.Context) error {
	return l.lock.Validate(ctx, l.layout, false)
}
func (l *foregroundInstallLease) Close() error { return l.session.Close() }

// RunUnixForeground is the callable production root/bootstrap lifecycle. The
// caller supplies source/service discovery and the ordinary daemon assembly;
// default layout dispatch is a separate entrypoint decision.
func RunUnixForeground(ctx context.Context, layout platform.ResolvedLayout, discover func(context.Context) (bool, error), run func(context.Context, string) error) error {
	if os.Geteuid() != 0 {
		return (ForegroundBootstrap{Run: run}).Start(ctx)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	boot, err := installBootIdentity()
	if err != nil {
		return err
	}
	x, err := newForegroundTransaction(ctx, layout, executable, boot, trustedValidationBinaryHash)
	if err != nil {
		return err
	}
	if err := ConfigureUnixValidation(ctx, x); err != nil {
		return err
	}
	var session *nativeInstallSession
	x.Acquire = func(ctx context.Context) (InstallLease, error) {
		var lock *platform.OwnedInstallLease
		var err error
		if x.Private {
			lock, err = platform.AcquirePrivateBootstrapLease(ctx, layout)
		} else {
			lock, err = platform.AcquireInstallLease(ctx, layout)
		}
		if err != nil {
			return nil, err
		}
		mode := uint32(0711)
		if x.Private {
			mode = 0700
		}
		root, err := platform.OpenTrustedRoot(ctx, layout.BaseDir, platform.RootPolicy{Owner: 0, Mode: mode})
		if err != nil {
			return nil, errors.Join(err, lock.Close())
		}
		store, err := NewInstallJournalStore(ctx, root)
		if err != nil {
			return nil, errors.Join(err, root.Close(), lock.Close())
		}
		x.Store = store
		session = &nativeInstallSession{lease: lock, base: root, layout: layout, tx: x}
		return &foregroundInstallLease{lock: lock, root: root, layout: layout, session: session}, nil
	}
	create := func(ctx context.Context) error {
		root, err := platform.OpenTrustedRoot(ctx, layout.Data.Root, platform.RootPolicy{Owner: 0, Mode: 0700, AllowCreate: true})
		if err != nil {
			return err
		}
		return root.Close()
	}
	initialize := func(ctx context.Context, id string) error {
		original := x.Artifacts
		session.state = nativeInstallState{Foreground: true, TransactionID: id, BootID: boot, Layout: layout, DataAction: InstallDataCreate, DataHash: sha256Hex(id), OldDefinition: service.Definition{Status: service.StatusNotInstalled}, TargetDefinition: service.Definition{Status: service.StatusNotInstalled}}
		root, err := platform.OpenTrustedRoot(ctx, layout.Data.Root, platform.RootPolicy{Owner: 0, Mode: 0700})
		exists := err == nil
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if exists {
			session.state.TargetObject, err = nativeDirectoryObject(ctx, root, boot, id)
			if err != nil {
				return errors.Join(err, root.Close())
			}
			names, readErr := root.ReadNames(ctx)
			closeErr := root.Close()
			if err := errors.Join(readErr, closeErr); err != nil {
				return err
			}
			for _, name := range names {
				if name != "install.lock" && name != "locks" {
					return installBusy("existing data requires recovery or migration")
				}
			}
		}
		if err := session.saveStateAt(ctx, "unit-bootstrap"); err != nil {
			return err
		}
		x.Artifacts.CandidateHash = original.CandidateHash
		return nil
	}
	prepare := func(ctx context.Context, id string) error {
		original := x.Artifacts
		if err := session.prepareData(ctx, InstallRequest{}, &nativeReleaseInputs{}); err != nil {
			return err
		}
		if session.state.TargetObject.Present {
			if err := session.prepareDataParts(ctx); err != nil {
				return err
			}
		}
		if err := session.saveState(ctx); err != nil {
			return err
		}
		x.Artifacts.CandidateHash = original.CandidateHash
		x.Artifacts.BackupHash = x.preparedAuthority.ServiceBackup.SHA256
		return nil
	}
	return (ForegroundBootstrap{Root: true, Transaction: x, DiscoverSource: discover, CreateData: create, InitializeJournal: initialize, PrepareJournal: prepare, Run: run}).Start(ctx)
}
