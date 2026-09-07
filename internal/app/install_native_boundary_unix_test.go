//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
)

type nativeBoundaryManager struct {
	files      service.DefinitionStore
	paths      service.SystemdPaths
	running    bool
	failStarts int
}

func (m *nativeBoundaryManager) Run(ctx context.Context, argv []string) (service.CommandResult, error) {
	if len(argv) < 3 {
		return service.CommandResult{}, errors.New("unexpected manager argv")
	}
	switch argv[2] {
	case "start":
		if m.failStarts > 0 {
			m.failStarts--
			return service.CommandResult{}, errors.New("fixture old service restart failed")
		}
		m.running = true
	case "stop":
		m.running = false
	case "show":
		load, fragment, enabled := "loaded", m.paths.UnitFile, "disabled"
		_, err := m.files.Read(ctx, m.paths.UnitFile)
		if errors.Is(err, os.ErrNotExist) {
			load, fragment = "not-found", ""
		} else if err != nil {
			return service.CommandResult{}, err
		}
		if link, err := m.files.ReadLink(ctx, m.paths.UnitFile); err == nil && link == "/dev/null" {
			load, fragment, enabled = "masked", "/dev/null", "masked"
		}
		active, pid, group := "inactive", 0, ""
		if m.running {
			active, pid, group = "active", 123, "/system.slice/mihari.service"
		}
		return service.CommandResult{Stdout: []byte(fmt.Sprintf("LoadState=%s\nActiveState=%s\nSubState=dead\nMainPID=%d\nControlGroup=%s\nFragmentPath=%s\nDropInPaths=\nUnitFileState=%s\n", load, active, pid, group, fragment, enabled))}, nil
	}
	return service.CommandResult{}, nil
}

type nativeBoundaryTree struct{}

func (nativeBoundaryTree) Empty(context.Context, string) (bool, error) { return true, nil }
func (nativeBoundaryTree) SignalGroup(context.Context, string, string) error {
	return errors.New("no fixture process may be signaled")
}
func (nativeBoundaryTree) Lookup(context.Context, service.ProcessIdentity) (bool, error) {
	return false, nil
}
func (nativeBoundaryTree) SignalIdentity(context.Context, service.ProcessIdentity, string) error {
	return errors.New("no fixture process may be signaled")
}
func (nativeBoundaryTree) Identify(_ context.Context, pid int) (service.ProcessIdentity, error) {
	return service.ProcessIdentity{PID: pid, BootID: "fixture-boot", StartUnix: 100}, nil
}

func nativeBoundarySession(t *testing.T) (context.Context, *nativeInstallSession, *nativeBoundaryManager, service.Definition) {
	t.Helper()
	ctx, root := nativeInstallFixture(t)
	base, err := platform.OpenTrustedRoot(ctx, root, platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := base.Close(); err != nil {
			t.Error(err)
		}
	})
	store, err := NewInstallJournalStore(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := platform.ResolveLayout(platform.LayoutInput{Data: filepath.Join(root, "data"), InstallRoot: filepath.Join(root, "install"), EUID: 0}, platform.SystemLayoutDefaults())
	if err != nil {
		t.Fatal(err)
	}
	layout.ChannelPath = filepath.Join(root, "mihari-channel")
	unitDir := filepath.Join(root, "system")
	if err := os.MkdirAll(filepath.Join(unitDir, "wants"), 0700); err != nil {
		t.Fatal(err)
	}
	paths := service.DefaultSystemdPaths()
	paths.UnitDir = unitDir
	paths.UnitFile = filepath.Join(unitDir, "mihari.service")
	paths.DropinDir = filepath.Join(unitDir, "mihari.service.d")
	paths.WantsLink = filepath.Join(unitDir, "wants", "mihari.service")
	manager := &nativeBoundaryManager{files: service.NewUnixDefinitionStore(), paths: paths}
	tx := &InstallTransaction{Store: store, Artifacts: InstallArtifacts{BootID: "fixture-boot"}, ParentIdentity: ProcessStartIdentity{PID: 7, BootID: "fixture-boot", StartUnix: 100}, EUID: 0}
	tx.Service = service.NewSystemdAdapterWithConfig(service.SystemdConfig{Runner: manager, Files: manager.files, Paths: paths, Tree: nativeBoundaryTree{}, Hook: tx.journaledHook})
	def, err := service.BuildUnixDefinition(layout, "linux")
	if err != nil {
		t.Fatal(err)
	}
	def.Files[0].Path = paths.UnitFile
	def.Links = []service.DefinitionLink{{Path: paths.WantsLink, Target: paths.UnitFile, Owner: 0, Mode: 0777}}
	session := &nativeInstallSession{base: base, layout: layout, tx: tx, buildDefinition: func(platform.ResolvedLayout) (service.Definition, error) { return def, nil }}
	return ctx, session, manager, def
}
func nativeBoundaryApply(t *testing.T, ctx context.Context, s *nativeInstallSession, req InstallRequest, old service.Definition, inputs *nativeReleaseInputs) {
	t.Helper()
	if err := s.prepare(ctx, req, old, inputs, false); err != nil {
		t.Fatal(err)
	}
	// Only the global OS lock and validation process are fixture boundaries. The
	// native session, WAL store, D/files, service adapter and definition store run.
	s.tx.AfterDataCommitted = nil
	s.tx.Validation = barrierValidation{ValidationChild: harnessValidationChild(s.tx.Artifacts), check: func() {
		link, err := service.NewUnixDefinitionStore().ReadLink(ctx, s.state.TargetDefinition.Files[0].Path)
		if !nativeValidationBarrierMatches(old.Status, link, err, s.tx.journal.RecoveryAuthority) {
			t.Error("native manager barrier absent during validation", link, err)
		}
	}}
	lease := &fakeInstallLease{held: true, global: true}
	if _, err := s.tx.ApplyLocked(ctx, lease, req); err != nil {
		t.Fatal(err)
	}
	if err := s.cleanupCompletedCandidates(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestNativeInstallBoundary_LifecycleRetainsDataAuthority(t *testing.T) {
	ctx, s, manager, _ := nativeBoundarySession(t)
	req := InstallRequest{Schema: InstallRequestSchema, Operation: InstallOperationInstall, Channel: InstallChannelMain, Layout: InstallLayoutPrivate, Data: s.layout.Data.Root}
	nativeBoundaryApply(t, ctx, s, req, service.Definition{Status: service.StatusNotInstalled}, &nativeReleaseInputs{binary: []byte("verified candidate"), resources: map[string][]byte{}})
	original, err := os.Stat(s.layout.Data.Root)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := s.tx.Service.InspectDefinition(ctx)
	if err != nil {
		t.Fatal(err)
	}
	updateReq := req
	updateReq.Operation = InstallOperationUpdate
	nativeBoundaryApply(t, ctx, s, updateReq, installed, &nativeReleaseInputs{binary: []byte("verified upgrade candidate"), resources: map[string][]byte{}})
	for _, operation := range []string{"stop", "start", "stop", "uninstall"} {
		old, err := s.tx.Service.InspectDefinition(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.prepareLifecycle(ctx, operation, old); err != nil {
			t.Fatal(operation, err)
		}
		if err := s.tx.lifecycleActions(ctx, operation); err != nil {
			t.Fatal(operation, err)
		}
		if err := s.cleanupCompletedCandidates(ctx); err != nil {
			t.Fatal(err)
		}
		present, err := s.loadState(ctx)
		if err != nil || !present {
			t.Fatal("native lifecycle reload", present, err)
		}
	}
	if manager.running {
		t.Fatal("uninstall left fake manager running")
	}
	allowed, err := s.hasRetainedData(ctx)
	if err != nil || !allowed {
		t.Fatal("preserved D was not authorized", allowed, err)
	}
	previousID := s.state.TransactionID
	nativeBoundaryApply(t, ctx, s, req, service.Definition{Status: service.StatusNotInstalled}, &nativeReleaseInputs{binary: []byte("verified next candidate"), resources: map[string][]byte{}})
	if s.state.TransactionID == previousID {
		t.Fatal("new install reused completion metadata identity")
	}
	after, err := os.Stat(s.layout.Data.Root)
	if err != nil || !os.SameFile(original, after) {
		t.Fatal("reinstall replaced retained D", err)
	}
	originalMarker, err := os.ReadFile(filepath.Join(s.layout.Data.Root, "locks", "install-data-id"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.layout.Data.Root, "locks", "install-data-id"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.verifyRetainedData(ctx); err == nil || ok {
		t.Fatal("tampered retained data marker accepted")
	}
	// Recreate an equal marker beneath a different root inode; it is not the D
	// authorized by this boot's completed transaction.
	dataPath := s.layout.Data.Root
	if err := os.Rename(dataPath, dataPath+"-held"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataPath, "locks"), 0700); err != nil {
		t.Fatal(err)
	}
	// DataMarkerHash was carried from the original installation, so use that
	// original marker value from the first transaction's retained history.
	if err := os.WriteFile(filepath.Join(dataPath, "locks", "install-data-id"), originalMarker, 0600); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.verifyRetainedData(ctx); err == nil || ok {
		t.Fatal("replacement data root accepted")
	}

}
func TestNativeInstallBoundary_AbsentPathMigrationStagesBothBinaries(t *testing.T) {
	ctx, s, _, _ := nativeBoundarySession(t)
	fx := newMigrationFixture(t)
	source, err := openReadOnlyMigrationRoot(ctx, fx.source.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	req := fx.options().Request
	req.Layout = InstallLayoutPrivate
	req.Data = s.layout.Data.Root
	req.InstallRoot = s.layout.InstallRoot
	req.PathBinary = filepath.Join(filepath.Dir(s.layout.InstallRoot), "mihari-path")
	req.Operation = InstallOperationInstall
	inputs := &nativeReleaseInputs{source: source, binary: fx.binary, trust: fx.trust, resources: map[string][]byte{}}
	if err := s.prepare(ctx, req, service.Definition{Status: service.StatusNotInstalled}, inputs, false); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s.tx.prepared != nil {
			if err := s.tx.prepared.staging.Close(); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, role := range []string{JournalRoleManagedBinary, JournalRolePathBinary} {
		f := s.state.Files[role]
		if f.Path == "" || f.OldHash != "absent" || f.CandidateIdentity == "" {
			t.Fatal("missing distinct native candidate", role, f)
		}
		raw, err := os.ReadFile(filepath.Join(f.Parent, f.Stage, "candidate"))
		if err != nil || string(raw) != string(fx.binary) {
			t.Fatal("candidate not staged", role, err)
		}
	}
	if s.state.Files[JournalRoleManagedBinary].Path == s.state.Files[JournalRolePathBinary].Path {
		t.Fatal("distinct paths collapsed")
	}
	for path, hash := range fx.sourceSnap {
		if fx.sourceHashes(t)[path] != hash {
			t.Fatal("source changed", path)
		}
	}
}
func TestNativeInstallBoundary_ServiceIdentityRecovery(t *testing.T) {
	ctx, s, manager, target := nativeBoundarySession(t)
	oldFile := target.Files[0]
	oldFile.Bytes = append([]byte("# original\n"), oldFile.Bytes...)
	if err := manager.files.Write(ctx, oldFile); err != nil {
		t.Fatal(err)
	}
	captured, err := manager.files.Read(ctx, oldFile.Path)
	if err != nil {
		t.Fatal(err)
	}
	old := target
	old.Files = []service.DefinitionFile{captured}
	old.Links = nil
	s.state = nativeInstallState{TransactionID: testTxnID, BootID: "fixture-boot", Layout: s.layout, DataAction: InstallDataRetain, OldDefinition: old, TargetDefinition: target}
	if err := s.prepareServiceFiles(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.saveState(ctx); err != nil {
		t.Fatal(err)
	}
	s.tx.Artifacts.CandidateHash = sha256Hex("verified fixture candidate")
	marker, err := s.tx.Store.CreateTransactionMarker(ctx, testTxnID)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := s.tx.buildJournal(InstallRequest{Operation: InstallOperationRecover, Layout: InstallLayoutPrivate}, testTxnID, marker, s.tx.Artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.tx.Store.Save(ctx, journal); err != nil {
		t.Fatal(err)
	}
	effects := s.tx.serviceEffects
	held := oldFile.Path + ".held"
	if err := os.Rename(oldFile.Path, held); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldFile.Path, captured.Bytes, 0644); err != nil {
		t.Fatal(err)
	}
	mask := service.DefinitionAction{Kind: service.DefinitionActionMask, TargetRole: JournalRoleDefinition, Path: oldFile.Path, Link: "/dev/null"}
	if _, err := effects.prepare(ctx, mask); err == nil {
		t.Fatal("fresh equal-bytes inode authorized service replacement")
	}
	if err := os.Rename(held, oldFile.Path); err != nil {
		t.Fatal(err)
	}
	action, err := effects.prepare(ctx, mask)
	if err != nil {
		t.Fatal(err)
	}
	if err := effects.apply(ctx, action); err != nil {
		t.Fatal(err)
	}
	reverse := action
	reverse.Kind = JournalActionRestoreDefinition
	reverse.OldState, reverse.NewState = action.NewState, action.OldState
	if err := effects.apply(ctx, reverse); err != nil {
		t.Fatal(err)
	}
	restored, err := manager.files.Read(ctx, oldFile.Path)
	if err != nil || string(restored.Bytes) != string(captured.Bytes) || restored.Identity == captured.Identity {
		t.Fatal("private restore did not publish its recorded candidate", err)
	}
	if _, err := effects.observe(ctx, reverse); err != nil {
		t.Fatal("legitimate restore not recognized", err)
	}
	if err := os.Rename(oldFile.Path, held); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldFile.Path, restored.Bytes, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := effects.observe(ctx, reverse); err == nil {
		t.Fatal("recovery accepted equal bytes at a different inode")
	}
	// Cross-boot proof comes from the hash-bound private unit metadata and known
	// candidate states, after the journal's private marker has been verified.
	restoredSession := &nativeInstallSession{base: s.base, layout: s.layout, tx: &InstallTransaction{Store: s.tx.Store, Service: s.tx.Service, Artifacts: InstallArtifacts{BootID: "next-boot"}}}
	if present, err := restoredSession.loadState(ctx); err != nil || !present {
		t.Fatal("private recovery metadata did not load", present, err)
	}
	if got, err := restoredSession.tx.serviceEffects.observe(ctx, reverse); err != nil || !strings.EqualFold(got, action.OldState) {
		t.Fatal("cross-boot private content proof rejected", err)
	}
}
