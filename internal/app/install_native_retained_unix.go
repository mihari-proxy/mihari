//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
	"strings"
)

func (s *nativeInstallSession) hasRetainedData(ctx context.Context) (allowed bool, err error) {
	if s.tx.journal.RecoveryAuthority == InstallAuthorityTarget && s.tx.journal.Phase == InstallPhaseComplete && s.state.Layout.Data.Root == s.layout.Data.Root {
		return s.verifyRetainedData(ctx)
	}
	if s.layout.Mode != platform.PrivateMode {
		return false, nil
	}
	root, err := platform.OpenTrustedRoot(ctx, s.layout.Data.Root, platform.RootPolicy{Owner: 0, Mode: 0700})
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	// A private foreground bootstrap has its own P journal. Borrow the already
	// held P/global lease; merely inspecting it must never create journal paths.
	file, _, err := root.OpenFile(ctx, installJournalFileName, 0600)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	store, err := NewInstallJournalStore(ctx, root)
	if err != nil {
		return false, err
	}
	journal, err := store.Load(ctx)
	if err != nil {
		return false, err
	}
	if journal.DataRoot != s.layout.Data.Root || journal.RecoveryAuthority != InstallAuthorityTarget || (journal.Phase != InstallPhaseComplete && journal.Phase != InstallPhaseActivationCommitted) {
		return false, installBusy("private bootstrap recovery required")
	}
	previous := &nativeInstallSession{lease: s.lease, base: root, layout: s.layout, tx: &InstallTransaction{Store: store, Artifacts: InstallArtifacts{BootID: s.tx.Artifacts.BootID}}}
	present, err := previous.loadState(ctx)
	if err != nil {
		return false, err
	}
	if !present || !previous.state.Foreground {
		return false, unknownInstallState()
	}
	return previous.verifyRetainedData(ctx)
}
func (s *nativeInstallSession) observeRetainedData(ctx context.Context) (object JournalObject, marker string, err error) {
	root, err := platform.OpenTrustedRoot(ctx, s.layout.Data.Root, platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		return object, "", err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	object, err = nativeDirectoryObject(ctx, root, s.tx.Artifacts.BootID, s.state.TransactionID)
	if err != nil {
		return object, "", err
	}
	locks, err := root.OpenDir(ctx, "locks", platform.RootPolicy{Owner: 0, Mode: 0700})
	if errors.Is(err, os.ErrNotExist) {
		return object, "absent", nil
	}
	if err != nil {
		return object, "", err
	}
	marker, _, readErr := nativeFileObservation(ctx, locks, "install-data-id", 0600)
	return object, marker, errors.Join(readErr, locks.Close())
}
func (s *nativeInstallSession) retainedDataExpected() (JournalObject, string) {
	object, marker := s.state.TargetObject, s.state.DataMarkerHash
	if s.state.DataAction == InstallDataCreate {
		if len(s.state.DataParts) == 0 {
			identity, mount, _ := strings.Cut(s.state.DataIdentity, "@")
			object = JournalObject{Present: true, Identity: identity, MountID: mount, BootID: s.state.BootID}
		}
		marker = sha256Hex(s.state.TransactionID)
	}
	return object, marker
}
func (s *nativeInstallSession) verifyRetainedData(ctx context.Context) (bool, error) {
	actual, marker, err := s.observeRetainedData(ctx)
	if err != nil {
		return false, err
	}
	expected, expectedMarker := s.retainedDataExpected()
	if !retainedDataMatches(expected, expectedMarker, actual, marker, s.tx.Artifacts.BootID) {
		return false, unknownInstallState()
	}
	return true, nil
}
