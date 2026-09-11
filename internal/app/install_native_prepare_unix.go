//go:build linux || darwin

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/mihari-proxy/mihari/internal/geoip"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
)

func (s *nativeInstallSession) prepare(ctx context.Context, req InstallRequest, old service.Definition, inputs *nativeReleaseInputs, start bool) (err error) {
	if err := rejectNativeMigrationOverlap(ctx, inputs.source, s.layout.Data.Root); err != nil {
		return err
	}
	retained := false
	if old.Status == service.StatusNotInstalled {
		retained, err = s.hasRetainedData(ctx)
		if err != nil {
			return err
		}
	}
	id := (&InstallTransaction{}).newTransactionID()
	buildDefinition := s.buildDefinition
	if buildDefinition == nil {
		buildDefinition = func(layout platform.ResolvedLayout) (service.Definition, error) {
			return service.BuildUnixDefinition(layout, runtime.GOOS)
		}
	}
	target, err := buildDefinition(s.layout)
	if err != nil {
		return err
	}
	target.Enabled, target.Running = targetServicePolicy(req.Operation, old.Enabled, old.Running, start)
	if runtime.GOOS == "linux" {
		paths := service.DefaultSystemdPaths()
		if len(target.Links) == 0 {
			target.Links = []service.DefinitionLink{{Path: paths.WantsLink, Target: paths.UnitFile, Owner: 0, Mode: 0777}}
		}
	}
	s.state = nativeInstallState{TransactionID: id, BootID: s.tx.Artifacts.BootID, Layout: s.layout, OldDefinition: old, TargetDefinition: target, Files: map[string]nativeInstallFile{}}
	if inputs.source != nil {
		s.state.Source = inputs.source.Path()
	}
	install, err := platform.OpenTrustedRoot(ctx, s.layout.InstallRoot, platform.RootPolicy{Owner: 0, Mode: 0755, AllowCreate: true})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, install.Close()) }()
	s.state.InstallObject, err = nativeDirectoryObject(ctx, install, s.state.BootID, id)
	if err != nil {
		return err
	}
	// Resolve D before creating any business entry. An existing live D is retained
	// only when a prior private journal or the current definition identifies it.
	data, openErr := platform.OpenTrustedRoot(ctx, s.layout.Data.Root, platform.RootPolicy{Owner: 0, Mode: 0700})
	if openErr != nil && !errors.Is(openErr, os.ErrNotExist) {
		return openErr
	}
	if data != nil {
		defer func() { err = errors.Join(err, data.Close()) }()
		names, e := data.ReadNames(ctx)
		if e != nil {
			return e
		}
		empty := true
		for _, name := range names {
			if name != "install.lock" && name != "locks" {
				empty = false
			}
		}
		if old.Status == service.StatusNotInstalled && !empty && !retained {
			return migrateState("unknown target data")
		}
		s.state.TargetObject, err = nativeDirectoryObject(ctx, data, s.state.BootID, id)
		if err != nil {
			return err
		}
		if inputs.source != nil || len(inputs.resources) > 0 && old.Status == service.StatusNotInstalled && !retained {
			if !empty {
				return migrateState("target data already exists")
			}
			s.state.DataAction = InstallDataCreate
			if err := s.prepareData(ctx, req, inputs); err != nil {
				return err
			}
			if err := s.prepareDataParts(ctx); err != nil {
				return err
			}
		} else {
			s.state.DataAction = InstallDataRetain
			_, s.state.DataMarkerHash, err = s.observeRetainedData(ctx)
			if err != nil {
				return err
			}
		}
	} else {
		s.state.DataAction = InstallDataCreate
		if err := s.prepareData(ctx, req, inputs); err != nil {
			return err
		}
	}

	art := InstallArtifacts{BootID: s.state.BootID, DataAction: s.state.DataAction, Source: s.state.Source, Target: s.layout.Data.Root, DataRoot: s.layout.Data.Root, Install: s.layout.InstallRoot, Endpoint: s.layout.ControlEndpoint, Credential: s.layout.CredentialPath, CandidateHash: sha256HexBytes(inputs.binary), OldRunning: old.Running, OldEnabled: old.Enabled, TargetRunning: target.Running, TargetEnabled: target.Enabled, ManagedNew: inputs.binary, ChannelNew: []byte(req.Channel + "\n"), DataNew: []byte(id)}
	if s.state.DataAction == InstallDataCreate {
		s.state.DataHash = fileHash(art.DataNew)
	}
	channelMode := uint32(0600)
	if s.layout.Mode == platform.SystemMode {
		channelMode = 0644
	}
	for _, item := range []struct {
		role, path string
		mode       uint32
		candidate  []byte
		old        *[]byte
	}{{JournalRoleManagedBinary, filepath.Join(s.layout.InstallRoot, "mihari"), 0755, inputs.binary, &art.ManagedOld}, {JournalRoleChannel, s.layout.ChannelPath, channelMode, art.ChannelNew, &art.ChannelOld}} {
		file, oldBytes, err := prepareNativeInstallFile(ctx, id, item.role, item.path, item.mode, item.candidate)
		if err != nil {
			return err
		}
		s.state.Files[item.role] = file
		*item.old = oldBytes
	}
	if req.PathBinary != "" && filepath.Clean(req.PathBinary) != filepath.Join(s.layout.InstallRoot, "mihari") {
		file, oldBytes, err := prepareNativeInstallFile(ctx, id, JournalRolePathBinary, req.PathBinary, 0755, inputs.binary)
		if err != nil {
			return err
		}
		s.state.Files[JournalRolePathBinary] = file
		art.PathOld, art.PathNew = oldBytes, inputs.binary
	}
	if inputs.source != nil {
		dev, ino, mount := inputs.source.Identity()
		observed := []string{}
		if s.tx.prepared != nil {
			for rel, entry := range s.tx.prepared.obs {
				observed = append(observed, rel+"="+entry.hash)
			}
		}
		sort.Strings(observed)
		raw, err := json.Marshal(observed)
		if err != nil {
			return err
		}
		s.state.SourceObject = JournalObject{Present: true, Dev: dev, Ino: ino, MountID: mount, BootID: s.state.BootID, Marker: id, Identity: dev + ":" + ino, SHA256: sha256HexBytes(raw)}
	}
	if err := s.prepareServiceFiles(ctx); err != nil {
		return err
	}
	if err := s.saveState(ctx); err != nil {
		return err
	}
	art.BackupHash = s.tx.preparedAuthority.ServiceBackup.SHA256
	s.tx.Artifacts = art
	if s.tx.prepared != nil {
		s.tx.prepared.art = art
	}
	s.tx.StartAfterInstall = start
	return nil
}

func rejectNativeMigrationOverlap(ctx context.Context, source migrationCapability, target string) (err error) {
	if source == nil {
		return nil
	}
	cap, ok := source.(*readOnlyMigrationCap)
	if !ok {
		return migrateInvalid("migration source capability is unavailable")
	}
	root, err := platform.OpenTrustedRoot(ctx, target, platform.RootPolicy{Owner: 0, Mode: 0700})
	if errors.Is(err, os.ErrNotExist) {
		// Walk only existing trusted ancestors. A nonexistent target cannot
		// contain the source, but its parent may be inside a source alias.
		parent := filepath.Dir(target)
		for {
			root, err = platform.OpenTrustedParent(ctx, parent, 0)
			if !errors.Is(err, os.ErrNotExist) || parent == filepath.Dir(parent) {
				break
			}
			parent = filepath.Dir(parent)
		}
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, root.Close()) }()
		overlaps, err := cap.source.ContainsTrustedRoot(ctx, root)
		if err != nil {
			return err
		}
		if overlaps {
			return migrateInvalid("source and target must not nest")
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	overlaps, err := cap.source.OverlapsTrustedRoot(ctx, root)
	if err != nil {
		return err
	}
	if overlaps {
		return migrateInvalid("source and target must not nest")
	}
	return nil
}
func targetServicePolicy(operation string, enabled, running, start bool) (bool, bool) {
	switch operation {
	case InstallOperationInstall:
		return true, start
	case InstallOperationReinstall:
		return true, true
	default:
		return enabled, running
	}
}
func (s *nativeInstallSession) prepareData(ctx context.Context, req InstallRequest, inputs *nativeReleaseInputs) (err error) {
	parent, err := platform.OpenTrustedParent(ctx, filepath.Dir(s.layout.Data.Root), 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	name := ".mihari-data-" + s.state.TransactionID
	stage, err := parent.OpenDir(ctx, name, platform.RootPolicy{Owner: 0, Mode: 0700, AllowCreate: true})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stage.Close()) }()
	s.state.DataStage = filepath.Join(filepath.Dir(s.layout.Data.Root), name)
	_, s.state.DataIdentity, _, _, err = stage.Snapshot(ctx)
	if err != nil {
		return err
	}
	cap, err := openTrustedMigrationRoot(ctx, s.state.DataStage, 0, true)
	if err != nil {
		return err
	}
	ownedByPrepared := false
	defer func() {
		if !ownedByPrepared {
			err = errors.Join(err, cap.Close())
		}
	}()
	if inputs.source != nil {
		migrationReq := businessMigrationRequest(req)
		migrationReq.Source = inputs.source.Path()
		prepared, err := prepareMigration(ctx, migrationOptions{Source: inputs.source, Staging: cap, Request: migrationReq, Trust: inputs.trust, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Store: s.tx.Store, FixedSource: inputs.source.Path()})
		if err != nil {
			return err
		}
		prepared.cleanupFn = func() {} // The transaction owns staging and may have published it.
		s.tx.prepared = prepared
		ownedByPrepared = true
	} else {
		for rel, raw := range inputs.resources {
			if rel == "bin/mihomo" {
				continue
			}
			if err := cap.Mkdir(ctx, filepath.ToSlash(filepath.Dir(rel))); err != nil {
				return err
			}
			if err := cap.WriteFile(ctx, rel, raw); err != nil {
				return err
			}
			if strings.HasPrefix(rel, "geoip/") {
				if err := geoip.ValidateMMDBFile(filepath.Join(s.state.DataStage, filepath.FromSlash(rel))); err != nil {
					return err
				}
			}
		}
	}
	if inputs.core != nil {
		bin, err := stage.OpenDir(ctx, "bin", platform.RootPolicy{Owner: 0, Mode: 0700, AllowCreate: true})
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, bin.Close()) }()
		var expected *platform.FileIdentity
		current, id, e := bin.OpenFile(ctx, "mihomo", 0700)
		if e == nil {
			if err := current.Close(); err != nil {
				return err
			}
			expected = &id
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if err := bin.WriteFile(ctx, "mihomo", inputs.core, 0700, expected); err != nil {
			return err
		}
		if err := bin.WriteFile(ctx, "mihomo.provenance.json", inputs.receipt, 0600, nil); err != nil {
			return err
		}
	}
	locks, err := stage.OpenDir(ctx, "locks", platform.RootPolicy{Owner: 0, Mode: 0700, AllowCreate: true})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, locks.Close()) }()
	if err := locks.WriteFile(ctx, "install-data-id", []byte(s.state.TransactionID), 0600, nil); err != nil {
		return err
	}
	// The data lock inode is created before whole-tree publication and is never
	// replaced while a daemon can hold it.
	if err := stage.WriteFile(ctx, "daemon.lock", nil, 0600, nil); err != nil {
		return err
	}
	return stage.Sync(ctx)
}

func (s *nativeInstallSession) prepareDataParts(ctx context.Context) (err error) {
	stage, err := platform.OpenTrustedRoot(ctx, s.state.DataStage, platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stage.Close()) }()
	names, err := stage.ReadNames(ctx)
	if err != nil {
		return err
	}
	sort.Strings(names)
	liveLocks, locksErr := platform.OpenTrustedRoot(ctx, filepath.Join(s.layout.Data.Root, "locks"), platform.RootPolicy{Owner: 0, Mode: 0700})
	existingLocks := locksErr == nil
	if locksErr != nil && !errors.Is(locksErr, os.ErrNotExist) {
		return locksErr
	}
	if liveLocks != nil {
		if err := liveLocks.Close(); err != nil {
			return err
		}
	}
	for _, name := range names {
		if name == "daemon.lock" || (name == "locks" && existingLocks) {
			continue
		}
		part := nativeInstallDataPart{Name: name, Kind: "file"}
		child, e := stage.OpenDir(ctx, name, platform.RootPolicy{Owner: 0, Mode: 0700})
		if e == nil {
			part.Kind = "dir"
			_, part.Identity, _, _, e = child.Snapshot(ctx)
			e = errors.Join(e, child.Close())
			if e != nil {
				return e
			}
			part.Hash, e = hashNativeDataTree(ctx, filepath.Join(s.state.DataStage, name))
			if e != nil {
				return e
			}
		} else {
			part.Hash, part.Identity, e = nativeFileObservation(ctx, stage, name, 0600)
			if e != nil {
				return e
			}
		}
		s.state.DataParts = append(s.state.DataParts, part)
	}
	if !existingLocks {
		return nil
	}
	locks, err := stage.OpenDir(ctx, "locks", platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, locks.Close()) }()
	hash, id, err := nativeFileObservation(ctx, locks, "install-data-id", 0600)
	if err != nil {
		return err
	}
	s.state.DataParts = append(s.state.DataParts, nativeInstallDataPart{Name: "locks/install-data-id", Kind: "file", Hash: hash, Identity: id})
	return nil
}
