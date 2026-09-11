//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
)

type nativeInstallFile struct {
	Path, Parent, ParentIdentity, Stage                               string
	OldHash, NewHash, OldIdentity, CandidateIdentity, RestoreIdentity string
	Mode                                                              uint32
}
type nativeInstallDataPart struct{ Name, Kind, Identity, Hash string }
type nativeInstallState struct {
	ServiceFiles   map[string]nativeServiceObject
	DataMarkerHash string
	Foreground     bool
	DataParts      []nativeInstallDataPart

	TransactionID, BootID                         string
	Layout                                        platform.ResolvedLayout
	Source, SourceIdentity                        string
	DataAction, DataStage, DataIdentity, DataHash string
	OldDefinition, TargetDefinition               service.Definition
	Files                                         map[string]nativeInstallFile
	SourceObject, TargetObject, InstallObject     JournalObject
}

type nativeInstallEffects struct {
	state       nativeInstallState
	transaction *InstallTransaction
}

func (e *nativeInstallEffects) SourcePresent() bool {
	if e.state.Source == "" {
		return true
	}
	source, err := platform.OpenReadOnlySource(context.Background(), e.state.Source)
	if err != nil {
		return false
	}
	return source.Close() == nil
}
func (e *nativeInstallEffects) Object(string) []byte { return nil }
func (e *nativeInstallEffects) Observe(ctx context.Context, action JournalAction) (_ string, err error) {
	if strings.HasPrefix(action.CandidateRef, "data:") {
		return e.observeDataPart(ctx, action)
	}
	switch action.Kind {
	case JournalActionDataPublish, JournalActionDataIsolate, JournalActionRestoreData:
		return e.observeData(ctx)
	case JournalActionReload, JournalActionRestoreReload:
		return action.OldState, nil // Reload is deliberately replayable after a crash.
	case JournalActionActivation:
		return e.transaction.journal.RecoveryAuthority, nil
	case JournalActionValidationStart, JournalActionValidationStop, JournalActionRestoreValidation:
		// recoverValidation has already identified, joined and reaped the child.
		if e.transaction.validation != nil {
			return "running", nil
		}
		return "idle", nil
	}
	file, ok := e.state.Files[action.TargetRole]
	if !ok {
		return "", unknownInstallState()
	}
	parent, err := e.fileParent(ctx, file)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	hash, id, err := nativeFileObservation(ctx, parent, filepath.Base(file.Path), file.Mode)
	if err != nil {
		return "", err
	}
	if err := e.checkFileState(file, hash, id); err != nil {
		return "", err
	}
	return hash, nil
}
func (e *nativeInstallEffects) Apply(ctx context.Context, action JournalAction) (err error) {
	if strings.HasPrefix(action.CandidateRef, "data:") {
		return e.applyDataPart(ctx, action)
	}
	switch action.Kind {
	case JournalActionDataPublish:
		return e.publishData(ctx)
	case JournalActionDataIsolate, JournalActionRestoreData:
		return e.isolateData(ctx)
	case JournalActionReload, JournalActionRestoreReload:
		if e.transaction.serviceEffects == nil {
			return nil
		}
		return e.transaction.serviceEffects.apply(ctx, action)
	case JournalActionActivation, JournalActionValidationStart, JournalActionValidationStop, JournalActionRestoreValidation:
		return nil
	}
	file, ok := e.state.Files[action.TargetRole]
	if !ok {
		return unknownInstallState()
	}
	parent, err := e.fileParent(ctx, file)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	hash, id, err := nativeFileObservation(ctx, parent, filepath.Base(file.Path), file.Mode)
	if err != nil {
		return err
	}
	if err := e.checkFileState(file, hash, id); err != nil {
		return err
	}
	if hash == action.NewState {
		return nil
	}
	if hash != action.OldState {
		return unknownInstallState()
	}
	if action.NewState == "absent" {
		current, identity, err := parent.OpenFile(ctx, filepath.Base(file.Path), file.Mode)
		if err != nil {
			return err
		}
		if err := current.Close(); err != nil {
			return err
		}
		return parent.RemoveFile(ctx, filepath.Base(file.Path), file.Mode, identity)
	}
	stage, err := parent.OpenDir(ctx, file.Stage, platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stage.Close()) }()
	name, want := "candidate", file.CandidateIdentity
	if action.NewState == file.OldHash {
		name, want = "restore", file.RestoreIdentity
	} else if action.NewState != file.NewHash {
		return unknownInstallState()
	}
	candidate, candidateID, err := stage.OpenFile(ctx, name, file.Mode)
	if err != nil {
		return err
	}
	raw, readErr := readInstallFile(ctx, candidate, migrationBinaryMax)
	readErr = errors.Join(readErr, candidate.Close())
	if readErr != nil {
		return readErr
	}
	if sha256HexBytes(raw) != action.NewState || !e.sameBootIdentity(want, candidateID.Key()) {
		return unknownInstallState()
	}
	var expected *platform.FileIdentity
	if hash != "absent" {
		current, currentID, err := parent.OpenFile(ctx, filepath.Base(file.Path), file.Mode)
		if err != nil {
			return err
		}
		if err := current.Close(); err != nil {
			return err
		}
		if currentID.Key() != id {
			return unknownInstallState()
		}
		expected = &currentID
	}
	return stage.MoveFileTo(ctx, name, candidateID, parent, filepath.Base(file.Path), file.Mode, expected)
}
func (e *nativeInstallEffects) sameBootIdentity(recorded, actual string) bool {
	return e.state.BootID != e.transaction.Artifacts.BootID || recorded == actual
}
func (e *nativeInstallEffects) checkFileState(file nativeInstallFile, hash, id string) error {
	switch hash {
	case "absent":
		if file.OldHash == "absent" || file.NewHash == "absent" {
			return nil
		}
	case file.OldHash:
		if e.sameBootIdentity(file.OldIdentity, id) || e.sameBootIdentity(file.RestoreIdentity, id) {
			return nil
		}
	case file.NewHash:
		if e.sameBootIdentity(file.CandidateIdentity, id) {
			return nil
		}
	}
	return unknownInstallState()
}
func (e *nativeInstallEffects) fileParent(ctx context.Context, file nativeInstallFile) (*platform.TrustedRoot, error) {
	if filepath.Dir(file.Path) != file.Parent || !strings.HasPrefix(file.Stage, ".mihari-transaction-"+e.state.TransactionID+"-") || filepath.Base(file.Stage) != file.Stage {
		return nil, unknownInstallState()
	}
	parent, err := platform.OpenTrustedParent(ctx, file.Parent, 0)
	if err != nil {
		return nil, err
	}
	_, id, _, _, err := parent.Snapshot(ctx)
	if err != nil || !e.sameBootIdentity(file.ParentIdentity, id) {
		return nil, errors.Join(unknownInstallState(), err, parent.Close())
	}
	return parent, nil
}
func nativeFileObservation(ctx context.Context, parent *platform.TrustedRoot, name string, mode uint32) (string, string, error) {
	file, id, err := parent.OpenFile(ctx, name, mode)
	if errors.Is(err, os.ErrNotExist) {
		return "absent", "", nil
	}
	if err != nil {
		return "", "", err
	}
	raw, err := readInstallFile(ctx, file, migrationBinaryMax)
	err = errors.Join(err, file.Close())
	if err != nil {
		return "", "", err
	}
	return sha256HexBytes(raw), id.Key(), nil
}
func (e *nativeInstallEffects) observeData(ctx context.Context) (_ string, err error) {
	root, err := platform.OpenTrustedRoot(ctx, e.state.Layout.Data.Root, platform.RootPolicy{Owner: 0, Mode: 0700})
	if errors.Is(err, os.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	_, id, _, _, err := root.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	if !e.sameBootIdentity(e.state.DataIdentity, id) {
		return "", unknownInstallState()
	}
	locks, err := root.OpenDir(ctx, "locks", platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, locks.Close()) }()
	marker, _, err := nativeFileObservation(ctx, locks, "install-data-id", 0600)
	if err != nil {
		return "", err
	}
	if marker != sha256Hex(e.state.TransactionID) {
		return "", unknownInstallState()
	}
	return e.state.DataHash, nil
}
func (e *nativeInstallEffects) publishData(ctx context.Context) (err error) {
	if e.state.DataAction != InstallDataCreate {
		return unknownInstallState()
	}
	actual, err := e.observeData(ctx)
	if err != nil {
		return err
	}
	if actual == e.state.DataHash {
		return nil
	}
	if actual != "absent" {
		return unknownInstallState()
	}
	parentPath := filepath.Dir(e.state.Layout.Data.Root)
	if filepath.Dir(e.state.DataStage) != parentPath {
		return unknownInstallState()
	}
	parent, err := platform.OpenTrustedParent(ctx, parentPath, 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	stage, err := parent.OpenDir(ctx, filepath.Base(e.state.DataStage), platform.RootPolicy{Owner: 0, Mode: 0700})
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
	locks, err := stage.OpenDir(ctx, "locks", platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		return err
	}
	marker, _, readErr := nativeFileObservation(ctx, locks, "install-data-id", 0600)
	if err := errors.Join(readErr, locks.Close()); err != nil {
		return err
	}
	if marker != sha256Hex(e.state.TransactionID) {
		return unknownInstallState()
	}
	_, err = parent.MoveDirTo(ctx, filepath.Base(e.state.DataStage), stage, parent, filepath.Base(e.state.Layout.Data.Root))
	return err
}
func (e *nativeInstallEffects) isolateData(ctx context.Context) (err error) {
	if e.state.DataAction == InstallDataRetain {
		return nil
	}
	actual, err := e.observeData(ctx)
	if err != nil {
		return err
	}
	if actual == "absent" {
		return nil
	}
	if actual != e.state.DataHash {
		return unknownInstallState()
	}
	parent, err := platform.OpenTrustedParent(ctx, filepath.Dir(e.state.Layout.Data.Root), 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	data, err := parent.OpenDir(ctx, filepath.Base(e.state.Layout.Data.Root), platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, data.Close()) }()
	_, err = parent.MoveDirTo(ctx, filepath.Base(e.state.Layout.Data.Root), data, parent, ".mihari-isolated-"+e.state.TransactionID)
	return err
}
