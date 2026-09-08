//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
	"path/filepath"
)

// Cleanup never discards a candidate needed by a pending recovery. The immutable
// private unit metadata remains with the completion journal as its authority.
func (s *nativeInstallSession) cleanupCompletedCandidates(ctx context.Context) error {
	if s.tx == nil || s.state.TransactionID == "" {
		return nil
	}
	current, err := s.tx.Store.files.inspect(ctx, installJournalFileName)
	if err != nil {
		return err
	}
	published := false
	if current.Present {
		journal, err := s.tx.Store.Load(ctx)
		if err != nil {
			return err
		}
		if journal.TransactionID == s.state.TransactionID {
			if journal.Phase != InstallPhaseComplete {
				return nil
			}
			published = journal.RecoveryAuthority == InstallAuthorityTarget
		}
	}
	effect := &nativeInstallEffects{transaction: s.tx, state: s.state}
	result := (&nativeServiceFiles{objects: s.state.ServiceFiles, recordedBoot: s.state.BootID, currentBoot: s.tx.Artifacts.BootID}).Cleanup(ctx)
	for _, file := range s.state.Files {
		result = errors.Join(result, effect.cleanupFileStage(ctx, file))
	}
	// An absent-D commit moved the whole candidate tree. Existing private P moved
	// the described parts and leaves only its inert, empty staging lock objects.
	if s.state.DataStage != "" {
		if published {
			result = errors.Join(result, effect.cleanupPublishedDataStage(ctx))
		} else {
			result = errors.Join(result, effect.cleanupUnpublishedDataStage(ctx))
		}
	}
	return result
}
func (e *nativeInstallEffects) cleanupFileStage(ctx context.Context, file nativeInstallFile) (err error) {
	parent, err := e.fileParent(ctx, file)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	stage, err := parent.OpenDir(ctx, file.Stage, platform.RootPolicy{Owner: 0, Mode: 0700})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stage.Close()) }()
	for _, item := range []struct{ name, hash, identity string }{{"candidate", file.NewHash, file.CandidateIdentity}, {"restore", file.OldHash, file.RestoreIdentity}} {
		rawHash, identity, err := nativeFileObservation(ctx, stage, item.name, file.Mode)
		if err != nil {
			return err
		}
		if rawHash == "absent" {
			continue
		}
		if rawHash != item.hash || !e.sameBootIdentity(item.identity, identity) {
			return unknownInstallState()
		}
		held, id, err := stage.OpenFile(ctx, item.name, file.Mode)
		if err != nil {
			return err
		}
		if err := held.Close(); err != nil {
			return err
		}
		if id.Key() != identity {
			return unknownInstallState()
		}
		if err := stage.RemoveFile(ctx, item.name, file.Mode, id); err != nil {
			return err
		}
	}
	return parent.RemoveEmptyDir(ctx, file.Stage)
}
func (e *nativeInstallEffects) cleanupPublishedDataStage(ctx context.Context) (err error) {
	if filepath.Dir(e.state.DataStage) != filepath.Dir(e.state.Layout.Data.Root) || filepath.Base(e.state.DataStage) != ".mihari-data-"+e.state.TransactionID {
		return unknownInstallState()
	}
	parent, err := platform.OpenTrustedParent(ctx, filepath.Dir(e.state.DataStage), 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	stage, err := parent.OpenDir(ctx, filepath.Base(e.state.DataStage), platform.RootPolicy{Owner: 0, Mode: 0700})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stage.Close()) }()
	_, id, _, _, err := stage.Snapshot(ctx)
	if err != nil {
		return err
	}
	if !e.sameBootIdentity(e.state.DataIdentity, id) {
		return unknownInstallState()
	}
	names, err := stage.ReadNames(ctx)
	if err != nil {
		return err
	}
	for _, name := range names {
		switch name {
		case "locks":
			if err := stage.RemoveEmptyDir(ctx, name); err != nil {
				return err
			}
		case "daemon.lock":
			hash, identity, err := nativeFileObservation(ctx, stage, name, 0600)
			if err != nil {
				return err
			}
			if hash != sha256Hex("") {
				return unknownInstallState()
			}
			file, id, err := stage.OpenFile(ctx, name, 0600)
			if err != nil {
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
			if id.Key() != identity {
				return unknownInstallState()
			}
			if err := stage.RemoveFile(ctx, name, 0600, id); err != nil {
				return err
			}
		default:
			return unknownInstallState()
		}
	}
	return parent.RemoveEmptyDir(ctx, filepath.Base(e.state.DataStage))
}

func (e *nativeInstallEffects) cleanupUnpublishedDataStage(ctx context.Context) error {
	return e.cleanupUnpublishedStage(ctx, false)
}

func (e *nativeInstallEffects) cleanupUnpublishedBootstrapStage(ctx context.Context) error {
	return e.cleanupUnpublishedStage(ctx, true)
}

func (e *nativeInstallEffects) cleanupUnpublishedStage(ctx context.Context, bootstrap bool) (err error) {
	if filepath.Dir(e.state.DataStage) != filepath.Dir(e.state.Layout.Data.Root) || filepath.Base(e.state.DataStage) != ".mihari-data-"+e.state.TransactionID {
		return unknownInstallState()
	}
	parent, err := platform.OpenTrustedParent(ctx, filepath.Dir(e.state.DataStage), 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	stage, err := parent.OpenDir(ctx, filepath.Base(e.state.DataStage), platform.RootPolicy{Owner: 0, Mode: 0700})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stage.Close()) }()
	_, id, _, _, err := stage.Snapshot(ctx)
	if err != nil {
		return err
	}
	if bootstrap {
		err = e.cleanupBootstrapDataContents(ctx, stage, id)
	} else {
		err = e.cleanupUnpublishedDataContents(ctx, stage, id)
	}
	if err != nil {
		return err
	}
	return parent.RemoveEmptyDir(ctx, filepath.Base(e.state.DataStage))
}

func (e *nativeInstallEffects) cleanupUnpublishedDataContents(ctx context.Context, stage *platform.TrustedRoot, id string) error {
	if id != e.state.DataIdentity {
		return unknownInstallState()
	}
	return removeNativeStageContents(ctx, stage)
}

// Foreground staging has only an empty daemon lock and the transaction marker.
// A different boot relies on the verified private transaction metadata and this
// exhaustive content check. Empty remnants are only removed as empty directories;
// unknown files never gain recursive deletion authority from a changed boot.
func (e *nativeInstallEffects) cleanupBootstrapDataContents(ctx context.Context, stage *platform.TrustedRoot, id string) (err error) {
	currentBoot := e.transaction.Artifacts.BootID
	if e.state.BootID == "" || currentBoot == "" || !e.sameBootIdentity(e.state.DataIdentity, id) {
		return unknownInstallState()
	}
	names, err := stage.ReadNames(ctx)
	if err != nil {
		return err
	}
	for _, name := range names {
		if name != "daemon.lock" && name != "locks" {
			return unknownInstallState()
		}
	}
	daemonHash, daemonID, err := nativeFileObservation(ctx, stage, "daemon.lock", 0600)
	if err != nil {
		return err
	}
	if daemonHash != "absent" && daemonHash != sha256Hex("") {
		return unknownInstallState()
	}
	_, actual, owner, _, err := stage.Snapshot(ctx)
	if err != nil {
		return err
	}
	if actual != id {
		return unknownInstallState()
	}
	locks, err := stage.OpenDir(ctx, "locks", platform.RootPolicy{Owner: owner, Mode: 0700})
	if errors.Is(err, os.ErrNotExist) && len(names) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, locks.Close()) }()
	lockNames, err := locks.ReadNames(ctx)
	if err != nil {
		return err
	}
	for _, name := range lockNames {
		if name != "install-data-id" {
			return unknownInstallState()
		}
	}
	markerHash, markerID, err := nativeFileObservation(ctx, locks, "install-data-id", 0600)
	if err != nil {
		return err
	}
	if markerHash != sha256Hex(e.state.TransactionID) && (markerHash != "absent" || daemonHash != "absent") {
		return unknownInstallState()
	}
	// This order preserves the stage marker until only empty directories remain.
	for _, file := range []struct {
		root                 *platform.TrustedRoot
		name, hash, identity string
	}{{stage, "daemon.lock", daemonHash, daemonID}, {locks, "install-data-id", markerHash, markerID}} {
		if file.hash == "absent" {
			continue
		}
		held, identity, err := file.root.OpenFile(ctx, file.name, 0600)
		if err != nil {
			return err
		}
		if err := held.Close(); err != nil {
			return err
		}
		if identity.Key() != file.identity {
			return unknownInstallState()
		}
		if err := file.root.RemoveFile(ctx, file.name, 0600, identity); err != nil {
			return err
		}
	}
	return stage.RemoveEmptyDir(ctx, "locks")
}

// This deletion capability is used only after proving a privately created
// candidate is not needed by a pending journal, and checking its retained inode.
func removeNativeStageContents(ctx context.Context, root *platform.TrustedRoot) error {
	names, err := root.ReadNames(ctx)
	if err != nil {
		return err
	}
	for _, name := range names {
		file, id, fileErr := root.OpenFile(ctx, name, 0755)
		if fileErr == nil {
			if err := file.Close(); err != nil {
				return err
			}
			if err := root.RemoveFile(ctx, name, 0755, id); err != nil {
				return err
			}
			continue
		}
		child, err := root.OpenDir(ctx, name, platform.RootPolicy{Owner: 0, Mode: 0700})
		if err != nil {
			return errors.Join(fileErr, err)
		}
		err = errors.Join(removeNativeStageContents(ctx, child), child.Close())
		if err != nil {
			return err
		}
		if err := root.RemoveEmptyDir(ctx, name); err != nil {
			return err
		}
	}
	return nil
}
