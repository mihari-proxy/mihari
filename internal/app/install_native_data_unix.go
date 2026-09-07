//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mihari-proxy/mihari/internal/platform"
)

// PublishDataLocked uses one directory rename when D is absent. A private P
// already owns install.lock and keeps its inode: each new top-level object has
// an independent durable intent and an independently identifiable candidate.
func (e *nativeInstallEffects) PublishDataLocked(ctx context.Context, x *InstallTransaction) error {
	if len(e.state.DataParts) == 0 {
		return x.fileStep(ctx, JournalActionDataPublish, JournalRoleData, "absent", e.state.DataHash)
	}
	if x.prepared != nil {
		if err := x.prepared.recheckAndPublish(ctx); err != nil {
			return err
		}
	}
	if x.AfterDataCommitted != nil {
		if err := x.AfterDataCommitted(ctx); err != nil {
			return err
		}
	}
	for _, part := range e.state.DataParts {
		action := JournalAction{Kind: JournalActionDataPublish, TargetRole: JournalRoleData, OldState: "absent", NewState: part.Hash, CandidateRef: "data:" + sha256Hex(part.Name)}
		if err := x.step(ctx, action, func(ctx context.Context) error { return e.applyDataPart(ctx, action) }); err != nil {
			return err
		}
	}
	return nil
}
func (e *nativeInstallEffects) dataPart(action JournalAction) (nativeInstallDataPart, error) {
	for _, part := range e.state.DataParts {
		if action.CandidateRef == "data:"+sha256Hex(part.Name) {
			return part, nil
		}
	}
	return nativeInstallDataPart{}, unknownInstallState()
}
func (e *nativeInstallEffects) openDataPartParent(ctx context.Context, path, name string) (*platform.TrustedRoot, string, error) {
	root, err := platform.OpenTrustedRoot(ctx, path, platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		return nil, "", err
	}
	if name == "locks/install-data-id" {
		locks, err := root.OpenDir(ctx, "locks", platform.RootPolicy{Owner: 0, Mode: 0700})
		closeErr := root.Close()
		if err != nil {
			return nil, "", errors.Join(err, closeErr)
		}
		if closeErr != nil {
			return nil, "", errors.Join(closeErr, locks.Close())
		}
		return locks, "install-data-id", nil
	}
	if filepath.Base(name) != name {
		return nil, "", errors.Join(unknownInstallState(), root.Close())
	}
	return root, name, nil
}
func (e *nativeInstallEffects) observeDataPart(ctx context.Context, action JournalAction) (_ string, err error) {
	part, err := e.dataPart(action)
	if err != nil {
		return "", err
	}
	parent, name, err := e.openDataPartParent(ctx, e.state.Layout.Data.Root, part.Name)
	if errors.Is(err, os.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	if part.Kind == "dir" {
		child, err := parent.OpenDir(ctx, name, platform.RootPolicy{Owner: 0, Mode: 0700})
		if errors.Is(err, os.ErrNotExist) {
			return "absent", nil
		}
		if err != nil {
			return "", err
		}
		defer func() { err = errors.Join(err, child.Close()) }()
		_, id, _, _, err := child.Snapshot(ctx)
		if err != nil {
			return "", err
		}
		if !e.sameBootIdentity(part.Identity, id) {
			return "", unknownInstallState()
		}
		actualHash, err := hashNativeDataTree(ctx, filepath.Join(e.state.Layout.Data.Root, part.Name))
		if err != nil {
			return "", err
		}
		if actualHash != part.Hash {
			return "", unknownInstallState()
		}
		return part.Hash, nil
	}
	hash, id, err := nativeFileObservation(ctx, parent, name, 0600)
	if err != nil {
		return "", err
	}
	if hash == "absent" {
		return hash, nil
	}
	if hash != part.Hash || !e.sameBootIdentity(part.Identity, id) {
		return "", unknownInstallState()
	}
	return hash, nil
}
func (e *nativeInstallEffects) applyDataPart(ctx context.Context, action JournalAction) (err error) {
	part, err := e.dataPart(action)
	if err != nil {
		return err
	}
	actual, err := e.observeDataPart(ctx, action)
	if err != nil {
		return err
	}
	if actual == action.NewState {
		return nil
	}
	if actual != action.OldState {
		return unknownInstallState()
	}
	sourcePath, targetPath := e.state.DataStage, e.state.Layout.Data.Root
	if action.NewState == "absent" {
		sourcePath = e.state.Layout.Data.Root
		targetPath = filepath.Join(filepath.Dir(e.state.DataStage), ".mihari-isolated-"+e.state.TransactionID)
		isolated, err := platform.OpenTrustedRoot(ctx, targetPath, platform.RootPolicy{Owner: 0, Mode: 0700, AllowCreate: true})
		if err != nil {
			return err
		}
		if part.Name == "locks/install-data-id" {
			locks, err := isolated.OpenDir(ctx, "locks", platform.RootPolicy{Owner: 0, Mode: 0700, AllowCreate: true})
			if err != nil {
				return errors.Join(err, isolated.Close())
			}
			if err := locks.Close(); err != nil {
				return errors.Join(err, isolated.Close())
			}
		}
		if err := isolated.Close(); err != nil {
			return err
		}
	} else if action.NewState != part.Hash {
		return unknownInstallState()
	}
	source, name, err := e.openDataPartParent(ctx, sourcePath, part.Name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, source.Close()) }()
	target, dest, err := e.openDataPartParent(ctx, targetPath, part.Name)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, target.Close()) }()
	if part.Kind == "dir" {
		child, err := source.OpenDir(ctx, name, platform.RootPolicy{Owner: 0, Mode: 0700})
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, child.Close()) }()
		_, id, _, _, err := child.Snapshot(ctx)
		if err != nil {
			return err
		}
		if !e.sameBootIdentity(part.Identity, id) {
			return unknownInstallState()
		}
		hash, err := hashNativeDataTree(ctx, filepath.Join(sourcePath, part.Name))
		if err != nil {
			return err
		}
		if hash != part.Hash {
			return unknownInstallState()
		}
		_, err = source.MoveDirTo(ctx, name, child, target, dest)
		return err
	}
	file, id, err := source.OpenFile(ctx, name, 0600)
	if err != nil {
		return err
	}
	raw, err := readInstallFile(ctx, file, migrationBinaryMax)
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if sha256HexBytes(raw) != part.Hash || !e.sameBootIdentity(part.Identity, id.Key()) {
		return unknownInstallState()
	}
	return source.MoveFileTo(ctx, name, id, target, dest, 0600, nil)
}

func hashNativeDataTree(ctx context.Context, path string) (_ string, err error) {
	root, err := openReadOnlyMigrationRoot(ctx, path)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	var observed []string
	var total int64
	var walk func(string, int) error
	walk = func(rel string, depth int) error {
		if depth > migrationMaxDepth {
			return migrateData("staged data exceeds depth")
		}
		entries, err := root.List(ctx, rel)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			child := entry.Name
			if rel != "." {
				child = rel + "/" + entry.Name
			}
			switch entry.Kind {
			case "dir":
				observed = append(observed, child+"/")
				if err := walk(child, depth+1); err != nil {
					return err
				}
			case "file":
				observed = append(observed, child+"="+entry.Hash)
				total += entry.Size
			default:
				return unknownInstallState()
			}
			if len(observed) > migrationMaxFiles || total > migrationMaxBytes {
				return migrateData("staged data exceeds limits")
			}
		}
		return nil
	}
	if err := walk(".", 0); err != nil {
		return "", err
	}
	sort.Strings(observed)
	return sha256Hex(strings.Join(observed, "\n")), nil
}
