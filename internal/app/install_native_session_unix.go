//go:build linux || darwin

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
)

type nativeInstallSession struct {
	buildDefinition func(platform.ResolvedLayout) (service.Definition, error)
	lease           *platform.OwnedInstallLease
	base            *platform.TrustedRoot
	layout          platform.ResolvedLayout
	tx              *InstallTransaction
	state           nativeInstallState
	dataLease       *nativeInstallDataLease
}

func (s *nativeInstallSession) Validate(ctx context.Context) error {
	return s.lease.Validate(ctx, s.layout, true)
}
func (s *nativeInstallSession) Close() error {
	var err error
	if s.dataLease != nil {
		err = s.dataLease.Release(context.Background())
	}
	if s.tx != nil && s.tx.prepared != nil && s.tx.prepared.staging != nil {
		err = errors.Join(err, s.tx.prepared.staging.Close())
	}
	return errors.Join(err, s.cleanupCompletedCandidates(context.Background()), s.base.Close(), s.lease.Close())
}
func openNativeInstallSession(ctx context.Context, layout platform.ResolvedLayout, adapter func(service.ActionHook) service.RecoveryAdapter) (_ *nativeInstallSession, err error) {
	lease, err := platform.AcquireInstallLease(ctx, layout)
	if err != nil {
		return nil, err
	}
	base, err := platform.OpenTrustedRoot(ctx, platform.SystemLayoutDefaults().BaseDir, platform.RootPolicy{Owner: 0, Mode: 0711})
	if err != nil {
		return nil, errors.Join(err, lease.Close())
	}
	s := &nativeInstallSession{lease: lease, base: base, layout: layout}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.Close())
		}
	}()
	store, err := NewInstallJournalStore(ctx, base)
	if err != nil {
		return nil, err
	}
	boot, err := installBootIdentity()
	if err != nil {
		return nil, err
	}
	s.tx = &InstallTransaction{Store: store, Private: layout.Mode == platform.PrivateMode, Artifacts: InstallArtifacts{BootID: boot}}
	s.tx.Service = adapter(s.tx.journaledHook)
	if err := ConfigureUnixValidation(ctx, s.tx); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *nativeInstallSession) loadState(ctx context.Context) (bool, error) {
	observed, err := s.tx.Store.files.inspect(ctx, installJournalFileName)
	if err != nil {
		return false, err
	}
	if !observed.Present {
		return false, nil
	}
	journal, err := s.tx.Store.Load(ctx)
	if err != nil {
		return false, err
	}
	ref := "transactions/" + journal.TransactionID + "/unit"
	if journal.ServiceBackup.Ref != ref {
		return false, unknownInstallState()
	}
	object, err := s.tx.Store.files.inspect(ctx, ref)
	if err != nil {
		return false, err
	}
	if !object.Present || object.SHA256 != journal.ServiceBackup.SHA256 || (journal.BootID == s.tx.Artifacts.BootID && object.Identity != journal.ServiceBackup.Identity) {
		return false, unknownInstallState()
	}
	raw, err := s.tx.Store.files.read(ctx, ref, MaxInstallJournalBytes)
	if err != nil {
		return false, err
	}
	if sha256HexBytes(raw) != journal.ServiceBackup.SHA256 {
		return false, unknownInstallState()
	}
	var state nativeInstallState
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return false, invalidInstallJournal()
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return false, invalidInstallJournal()
	}
	if state.TransactionID != journal.TransactionID || state.Layout.Data.Root != journal.DataRoot || state.Layout.InstallRoot != journal.InstallPath || state.Layout.ControlEndpoint != journal.EndpointPath || state.Layout.CredentialPath != journal.CredentialPath || state.DataAction != journal.DataAction {
		return false, unknownInstallState()
	}
	// A private transaction still holds the global lease, but cannot borrow an
	// unrelated private data lease selected by its metadata.
	if state.Layout != s.layout {
		if err := s.lease.BindInstallLayout(ctx, state.Layout); err != nil {
			return false, err
		}
		s.layout = state.Layout
	}
	s.state = state
	s.bindState(journal.ServiceBackup)
	return true, nil
}
func (s *nativeInstallSession) bindState(backup ServiceBackup) {
	currentBoot := s.tx.Artifacts.BootID
	s.tx.Artifacts = InstallArtifacts{BootID: currentBoot, DataAction: s.state.DataAction, Source: s.state.Source, Target: s.state.Layout.Data.Root, DataRoot: s.state.Layout.Data.Root, Install: s.state.Layout.InstallRoot, Endpoint: s.state.Layout.ControlEndpoint, Credential: s.state.Layout.CredentialPath, OldRunning: s.state.OldDefinition.Running, OldEnabled: s.state.OldDefinition.Enabled, TargetRunning: s.state.TargetDefinition.Running, TargetEnabled: s.state.TargetDefinition.Enabled}
	s.tx.preparedAuthority = &installPreparedAuthority{Source: s.state.SourceObject, Target: s.state.TargetObject, Install: s.state.InstallObject, ServiceBackup: backup, OldDefinition: s.state.OldDefinition, TargetDefinition: s.state.TargetDefinition}
	if !s.state.Foreground {
		s.tx.serviceEffects = &installServiceEffects{files: &nativeServiceFiles{objects: s.state.ServiceFiles, recordedBoot: s.state.BootID, currentBoot: currentBoot}, adapter: s.tx.Service.(service.RecoveryAdapter), old: s.state.OldDefinition, target: s.state.TargetDefinition}
	} else {
		s.tx.serviceEffects = nil
	}
	s.tx.Effects = &nativeInstallEffects{state: s.state, transaction: s.tx}
	s.tx.AfterDataCommitted = s.acquireDataLease
	if !s.state.Foreground {
		s.tx.BeforeRollback = s.prepareRollback
	}
}
func (s *nativeInstallSession) saveState(ctx context.Context) error {
	marker, err := s.tx.Store.CreateTransactionMarker(ctx, s.state.TransactionID)
	if err != nil {
		return err
	}
	_ = marker
	raw, err := json.Marshal(s.state)
	if err != nil {
		return err
	}
	if len(raw) > MaxInstallJournalBytes {
		return invalidInstallJournal()
	}
	ref := "transactions/" + s.state.TransactionID + "/unit"
	dur, err := s.tx.Store.files.write(ctx, ref, raw, JournalObject{})
	if err != nil {
		return err
	}
	if !dur.Durable {
		return installBusy("install backup is not durable")
	}
	s.bindState(ServiceBackup{Ref: ref, SHA256: sha256HexBytes(raw), Identity: dur.Object.Identity})
	s.tx.NewID = func() string { return s.state.TransactionID }
	return nil
}
func prepareNativeInstallFile(ctx context.Context, id, role, path string, mode uint32, candidate []byte) (_ nativeInstallFile, old []byte, err error) {
	parent, err := platform.OpenTrustedParent(ctx, filepath.Dir(path), 0)
	if err != nil {
		return nativeInstallFile{}, nil, err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	_, parentID, _, _, err := parent.Snapshot(ctx)
	if err != nil {
		return nativeInstallFile{}, nil, err
	}
	state := nativeInstallFile{Path: path, Parent: filepath.Dir(path), ParentIdentity: parentID, Stage: ".mihari-transaction-" + id + "-" + role, Mode: mode, OldHash: "absent", NewHash: fileHash(candidate)}
	file, oldID, err := parent.OpenFile(ctx, filepath.Base(path), mode)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return state, nil, err
	}
	if err == nil {
		old, err = readInstallFile(ctx, file, migrationBinaryMax)
		err = errors.Join(err, file.Close())
		if err != nil {
			return state, nil, err
		}
		state.OldHash, state.OldIdentity = sha256HexBytes(old), oldID.Key()
	}
	stage, err := parent.OpenDir(ctx, state.Stage, platform.RootPolicy{Owner: 0, Mode: 0700, AllowCreate: true})
	if err != nil {
		return state, nil, err
	}
	defer func() { err = errors.Join(err, stage.Close()) }()
	defer func() {
		if err != nil {
			err = errors.Join(err, removeNativeStageContents(context.WithoutCancel(ctx), stage), parent.RemoveEmptyDir(context.WithoutCancel(ctx), state.Stage))
		}
	}()
	for _, item := range []struct {
		name     string
		raw      []byte
		identity *string
	}{{"candidate", candidate, &state.CandidateIdentity}, {"restore", old, &state.RestoreIdentity}} {
		if item.raw == nil {
			continue
		}
		if err := stage.WriteFile(ctx, item.name, item.raw, mode, nil); err != nil {
			return state, nil, err
		}
		f, identity, err := stage.OpenFile(ctx, item.name, mode)
		if err != nil {
			return state, nil, err
		}
		if err := f.Close(); err != nil {
			return state, nil, err
		}
		*item.identity = identity.Key()
	}
	return state, old, nil
}
func nativeDirectoryObject(ctx context.Context, root *platform.TrustedRoot, boot, marker string) (JournalObject, error) {
	_, id, owner, mode, err := root.Snapshot(ctx)
	if err != nil {
		return JournalObject{}, err
	}
	identity, mount, ok := strings.Cut(id, "@")
	if !ok {
		return JournalObject{}, unknownInstallState()
	}
	dev, ino, ok := strings.Cut(identity, ":")
	if !ok {
		return JournalObject{}, unknownInstallState()
	}
	// Directories have no byte stream: record the digest of the actual retained
	// fstat observation, rather than a candidate hash or a guessed path hash.
	observation, err := json.Marshal(struct {
		Identity    string
		Owner, Mode uint32
	}{id, owner, mode})
	if err != nil {
		return JournalObject{}, err
	}
	return JournalObject{Present: true, SHA256: sha256HexBytes(observation), Dev: dev, Ino: ino, MountID: mount, BootID: boot, Marker: marker, Identity: identity}, nil
}

type nativeInstallDataLease struct {
	lock   *platform.OwnedDaemonLease
	layout platform.ResolvedLayout
}

func (l *nativeInstallDataLease) Held() bool { return l != nil && l.lock != nil }
func (l *nativeInstallDataLease) Release(context.Context) error {
	if !l.Held() {
		return nil
	}
	lock := l.lock
	l.lock = nil
	return lock.Close()
}
func (l *nativeInstallDataLease) WaitReleased(ctx context.Context) error {
	if l.Held() {
		return installBusy("installer data lease is still held")
	}
	lock, err := platform.AcquireDaemonLease(ctx, l.layout)
	if err != nil {
		return err
	}
	return lock.Close()
}

func (s *nativeInstallSession) acquireDataLease(ctx context.Context) error {
	if s.dataLease != nil && s.dataLease.Held() {
		return nil
	}
	lock, err := platform.AcquireDaemonLease(ctx, s.layout)
	if err != nil {
		return err
	}
	s.dataLease = &nativeInstallDataLease{lock: lock, layout: s.layout}
	s.tx.DataLease = s.dataLease
	return nil
}

func (s *nativeInstallSession) prepareRollback(ctx context.Context) error {
	// Quiesce any surviving manager tree before recovering data or binaries.
	if err := s.tx.Service.DisableAutostartAndStop(ctx); err != nil {
		return err
	}
	if err := s.tx.Service.WaitOwnedTreeExit(ctx); err != nil {
		return err
	}
	if len(s.state.Files) == 0 && s.state.DataStage == "" {
		return nil
	}
	root, err := platform.OpenTrustedRoot(ctx, s.layout.Data.Root, platform.RootPolicy{Owner: 0, Mode: 0700})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := root.Close(); err != nil {
		return err
	}
	return s.acquireDataLease(ctx)
}
