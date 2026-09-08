//go:build unix_security && (linux || darwin)

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/securitytest"
	"github.com/mihari-proxy/mihari/internal/service"
)

type securityNativeInstall struct {
	layout     platform.ResolvedLayout
	manager    *securityNativeManager
	definition service.Definition
	scenario   securityCrashScenario
	source     *migrationFixture
}

func newSecurityNativeInstall(t *testing.T) *securityNativeInstall {
	t.Helper()
	securitytest.Parent(t)
	rotateSecurityBase(t)
	ctx, root := nativeInstallFixture(t)
	layout, err := platform.ResolveLayout(platform.LayoutInput{Data: filepath.Join(root, "p"), InstallRoot: filepath.Join(root, "i"), EUID: 0}, platform.SystemLayoutDefaults())
	if err != nil {
		t.Fatal(err)
	}
	_ = ctx
	return securityFixtureForLayout(t, layout)
}
func (f *securityNativeInstall) open(t *testing.T) *nativeInstallSession {
	t.Helper()
	session, err := openNativeInstallSession(context.Background(), f.layout, func(hook service.ActionHook) service.RecoveryAdapter {
		return f.manager.adapter(hook)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if session.dataLease != nil {
			if err := session.dataLease.Release(context.Background()); err != nil {
				t.Error(err)
			}
		}
		if session.tx.prepared != nil && session.tx.prepared.staging != nil {
			if err := session.tx.prepared.staging.Close(); err != nil {
				t.Error(err)
			}
		}
		if err := errors.Join(session.base.Close(), session.lease.Close()); err != nil {
			t.Error(err)
		}
	})
	session.buildDefinition = func(platform.ResolvedLayout) (service.Definition, error) { return f.definition, nil }
	return session
}
func (f *securityNativeInstall) prepare(t *testing.T, s *nativeInstallSession) InstallRequest {
	t.Helper()
	request := InstallRequest{Schema: InstallRequestSchema, Operation: InstallOperationInstall, Channel: InstallChannelMain, Layout: InstallLayoutPrivate, Data: f.layout.Data.Root}
	if f.layout.Mode == platform.SystemMode {
		request.Layout = InstallLayoutSystem
		request.Data = ""
	}
	if f.scenario.retained {
		request.Channel = InstallChannelDev
	}
	if f.scenario.pathBinary {
		request.PathBinary = filepath.Join(filepath.Dir(f.layout.InstallRoot), "path-mihari")
	}
	old, err := s.tx.Service.InspectDefinition(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != service.StatusNotInstalled {
		request.Operation = InstallOperationUpdate
	}
	if err := s.prepare(context.Background(), request, old, f.inputs(t), false); err != nil {
		t.Fatal(err)
	}
	s.tx.Validation = harnessValidationChild(s.tx.Artifacts)
	return request
}

func TestSecurityNativeCrashMatrix(t *testing.T) {
	for _, scenario := range securityCrashScenarios() {
		t.Run(scenario.name, func(t *testing.T) { securityNativeCrashScenario(t, scenario) })
	}
}
func securityNativeCrashScenario(t *testing.T, scenario securityCrashScenario) {
	ctx := context.Background()
	control := newSecurityScenario(t, scenario)
	session := control.open(t)
	request := control.prepare(t, session)
	if _, err := session.tx.ApplyLocked(ctx, session, request); err != nil {
		t.Fatal("native positive transaction", err)
	}
	assertSecurityInventory(t, control, session)
	plan := actionKinds(session.tx.journal)
	if len(plan) == 0 {
		t.Fatal("empty native action inventory")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	for index, kind := range plan {
		for _, point := range []string{"before-intent", "after-intent", "after-effect", "after-done"} {
			t.Run(fmt.Sprintf("%02d-%s-%s", index+1, kind, point), func(t *testing.T) {
				fixture := newSecurityScenario(t, scenario)
				current := fixture.open(t)
				req := fixture.prepare(t, current)
				initialLock, err := os.Stat(filepath.Join(fixture.layout.BaseDir, "install.lock"))
				if err != nil {
					t.Fatal(err)
				}
				var retainedIdentity os.FileInfo
				if scenario.retained {
					var err error
					retainedIdentity, err = os.Stat(fixture.layout.Data.Root)
					if err != nil {
						t.Fatal(err)
					}
				}
				current.tx.crash = &installCrashSpec{index: index + 1, point: point}
				if !catchInstallCrash(func() {
					_, err := current.tx.ApplyLocked(ctx, current, req)
					if err != nil {
						t.Fatalf("failed before requested native crash: %v", err)
					}
				}) {
					t.Fatal("declared crash point was not reached")
				}
				persisted, err := current.tx.Store.Load(ctx)
				if err != nil {
					t.Fatal(err)
				}
				authority := persisted.RecoveryAuthority
				// Close/reopen actual leases and root capabilities. Recovery reconstructs
				// all authority from durable bytes and observed native object identities.
				if err := current.Close(); err != nil {
					t.Fatal(err)
				}
				recovered := fixture.open(t)
				if present, err := recovered.loadState(ctx); err != nil || !present {
					t.Fatal("native state reopen", err)
				}
				recovered.tx.Validation = harnessValidationChild(recovered.tx.Artifacts)
				if securityRecoveryNeedsNewProcessProof(persisted) {
					assertSecurityRecoveryRefusal(t, fixture, recovered, persisted, initialLock, retainedIdentity)
					if err := recovered.Close(); err != nil {
						t.Fatal(err)
					}
					return
				}
				if err := recovered.tx.RecoverLocked(ctx, recovered); err != nil {
					t.Fatal("native first recovery", err)
				}
				first, err := recovered.tx.Store.Load(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if first.Phase != InstallPhaseComplete || first.RecoveryAuthority != authority {
					t.Fatal("recovery crossed durable activation authority")
				}
				before, err := os.Stat(filepath.Join(fixture.layout.BaseDir, "install.lock"))
				if err != nil || !os.SameFile(initialLock, before) {
					t.Fatal("first recovery replaced permanent lock", err)
				}
				if err := recovered.tx.RecoverLocked(ctx, recovered); err != nil {
					t.Fatal("native repeated recovery", err)
				}
				after, err := os.Stat(filepath.Join(fixture.layout.BaseDir, "install.lock"))
				if err != nil || !os.SameFile(before, after) {
					t.Fatal("recovery replaced permanent lock inode")
				}
				second, err := recovered.tx.Store.Load(ctx)
				if err != nil {
					t.Fatal(err)
				}
				firstBytes, err := EncodeJournal(first)
				if err != nil {
					t.Fatal(err)
				}
				secondBytes, err := EncodeJournal(second)
				if err != nil {
					t.Fatal(err)
				}
				if string(firstBytes) != string(secondBytes) {
					t.Fatal("recovery twice changed durable state")
				}
				if scenario.retained {
					now, err := os.Stat(fixture.layout.Data.Root)
					if err != nil || !os.SameFile(retainedIdentity, now) {
						t.Fatal("fault recovery replaced retained D", err)
					}
				}
				fixture.assertSource(t)
				fixture.assertRecoveredService(t, recovered, authority)
				if authority == InstallAuthorityTarget {
					if _, err := os.Stat(fixture.layout.Data.Root); err != nil {
						t.Fatal("target authority lost D", err)
					}
				}
				if err := recovered.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}

	t.Run("reverse-recovery", func(t *testing.T) { securityReverseCrashMatrix(t, scenario, plan) })
}

func rotateSecurityBase(t *testing.T) {
	t.Helper()
	path := platform.SystemLayoutDefaults().BaseDir
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	cap, err := platform.OpenTrustedRoot(context.Background(), path, platform.RootPolicy{Mode: 0711})
	if err != nil {
		t.Fatal(err)
	}
	if err := cap.Close(); err != nil {
		t.Fatal(err)
	}
	destination, err := os.MkdirTemp(filepath.Dir(path), "retained-")
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, current) {
		t.Fatal("phase B identity changed")
	}
	if err := os.Rename(path, filepath.Join(destination, "system")); err != nil {
		t.Fatal(err)
	}
}

// Each reverse action is discovered from a real pre-activation transaction,
// then independently interrupted and reopened from disk at every WAL boundary.
func securityReverseCrashMatrix(t *testing.T, scenario securityCrashScenario, forward []string) {
	ctx := context.Background()
	activation := 0
	for i, kind := range forward {
		if kind == JournalActionActivation {
			activation = i + 1
			break
		}
	}
	if activation == 0 {
		t.Fatal("native activation action absent")
	}
	seed := func(t *testing.T) (*securityNativeInstall, *nativeInstallSession, int) {
		f := newSecurityScenario(t, scenario)
		s := f.open(t)
		req := f.prepare(t, s)
		s.tx.crash = &installCrashSpec{index: activation, point: "before-intent"}
		if !catchInstallCrash(func() {
			if _, err := s.tx.ApplyLocked(ctx, s, req); err != nil {
				t.Fatal("pre-activation seed failed", err)
			}
		}) {
			t.Fatal("seed fault not reached")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		r := f.open(t)
		if present, err := r.loadState(ctx); err != nil || !present {
			t.Fatal("native reverse state", err)
		}
		r.tx.Validation = harnessValidationChild(r.tx.Artifacts)
		j, err := r.tx.Store.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return f, r, len(j.Actions)
	}
	_, baseline, count := seed(t)
	if err := baseline.tx.RecoverLocked(ctx, baseline); err != nil {
		t.Fatal(err)
	}
	j, err := baseline.tx.Store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reverse := append([]JournalAction(nil), j.Actions[count:]...)
	assertSecurityReverseInventory(t, scenario, reverse)
	if len(reverse) == 0 {
		t.Fatal("native reverse action inventory empty")
	}
	if err := baseline.Close(); err != nil {
		t.Fatal(err)
	}
	for i, action := range reverse {
		for _, point := range []string{"before-intent", "after-intent", "after-effect", "after-done"} {
			t.Run(fmt.Sprintf("%02d-%s-%s", i+1, action.Kind, point), func(t *testing.T) {
				f, r, _ := seed(t)
				initialLock, err := os.Stat(filepath.Join(f.layout.BaseDir, "install.lock"))
				if err != nil {
					t.Fatal(err)
				}
				var retainedIdentity os.FileInfo
				if scenario.retained {
					retainedIdentity, err = os.Stat(f.layout.Data.Root)
					if err != nil {
						t.Fatal(err)
					}
				}
				r.tx.crash = &installCrashSpec{index: i + 1, point: point}
				if !catchInstallCrash(func() {
					if err := r.tx.RecoverLocked(ctx, r); err != nil {
						t.Fatal("failed before reverse fault", err)
					}
				}) {
					t.Fatal("reverse fault not reached")
				}
				if err := r.Close(); err != nil {
					t.Fatal(err)
				}
				reopened := f.open(t)
				if present, err := reopened.loadState(ctx); err != nil || !present {
					t.Fatal(err)
				}
				reopened.tx.Validation = harnessValidationChild(reopened.tx.Artifacts)
				persisted, err := reopened.tx.Store.Load(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if securityRecoveryNeedsNewProcessProof(persisted) {
					assertSecurityRecoveryRefusal(t, f, reopened, persisted, initialLock, retainedIdentity)
					if err := reopened.Close(); err != nil {
						t.Fatal(err)
					}
					return
				}
				if err := reopened.tx.RecoverLocked(ctx, reopened); err != nil {
					t.Fatal("reverse resume", err)
				}
				first, err := reopened.tx.Store.Load(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if first.Phase != InstallPhaseComplete || first.RecoveryAuthority != InstallAuthoritySource {
					t.Fatal("reverse changed source authority")
				}
				a, err := EncodeJournal(first)
				if err != nil {
					t.Fatal(err)
				}
				if err := reopened.tx.RecoverLocked(ctx, reopened); err != nil {
					t.Fatal(err)
				}
				second, err := reopened.tx.Store.Load(ctx)
				if err != nil {
					t.Fatal(err)
				}
				b, err := EncodeJournal(second)
				if err != nil || string(a) != string(b) {
					t.Fatal("reverse retry changed journal", err)
				}
				currentLock, err := os.Stat(filepath.Join(f.layout.BaseDir, "install.lock"))
				if err != nil || !os.SameFile(initialLock, currentLock) {
					t.Fatal("reverse replaced permanent lock", err)
				}
				if scenario.retained {
					currentData, err := os.Stat(f.layout.Data.Root)
					if err != nil || !os.SameFile(retainedIdentity, currentData) {
						t.Fatal("reverse replaced retained D", err)
					}
				}
				f.assertSource(t)
				f.assertRecoveredService(t, reopened, InstallAuthoritySource)
				if err := reopened.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func securityRecoveryNeedsNewProcessProof(journal InstallJournal) bool {
	if runtime.GOOS != "darwin" || journal.Phase == InstallPhaseComplete {
		return false
	}
	want := ""
	switch journal.RecoveryAuthority {
	case InstallAuthorityTarget:
		want = JournalActionStart
	case InstallAuthoritySource:
		want = JournalActionRestoreStart
	}
	for _, action := range journal.Actions {
		if want != "" && action.Kind == want && (action.Status == JournalActionIntent || action.Status == JournalActionDone) {
			return true
		}
	}
	return false
}

func assertSecurityRecoveryRefusal(t *testing.T, fixture *securityNativeInstall, session *nativeInstallSession, before InstallJournal, initialLock, retainedIdentity os.FileInfo) {
	ctx := context.Background()
	if before.BootID == "" || before.BootID != session.tx.Artifacts.BootID {
		t.Fatal("expected refusal fixture is not in the recorded boot")
	}
	wantJournal, err := session.tx.Store.files.read(ctx, installJournalFileName, MaxInstallJournalBytes)
	if err != nil {
		t.Fatal(err)
	}
	wantService, err := securityServiceSnapshot(ctx, fixture.manager)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := securityInstallationFileSnapshots(t, fixture)
	for attempt := 0; attempt < 2; attempt++ {
		err := session.tx.RecoverLocked(ctx, session)
		var apiErr protocol.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != protocol.CodeInvalidState || apiErr.Message != "service process group identity is unknown" {
			t.Fatalf("ambiguous service generation error=%v", err)
		}
		afterJournal, err := session.tx.Store.files.read(ctx, installJournalFileName, MaxInstallJournalBytes)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(afterJournal, wantJournal) {
			t.Fatal("refused recovery changed durable journal bytes")
		}
		gotService, err := securityServiceSnapshot(ctx, fixture.manager)
		if err != nil || gotService != wantService {
			t.Fatal("refused recovery changed service or definition", err)
		}
		assertSecurityInstallationFileSnapshots(t, wantFiles)
	}
	currentLock, err := os.Stat(filepath.Join(fixture.layout.BaseDir, "install.lock"))
	if err != nil || !os.SameFile(initialLock, currentLock) {
		t.Fatal("refused recovery replaced permanent lock", err)
	}
	if retainedIdentity != nil {
		currentData, err := os.Stat(fixture.layout.Data.Root)
		if err != nil || !os.SameFile(retainedIdentity, currentData) {
			t.Fatal("refused recovery replaced retained D", err)
		}
	}
	fixture.assertSource(t)
}

type securityInstallationFileSnapshot struct {
	path    string
	present bool
	info    os.FileInfo
	body    []byte
}

func securityInstallationFileSnapshots(t *testing.T, fixture *securityNativeInstall) []securityInstallationFileSnapshot {
	paths := []string{filepath.Join(fixture.layout.InstallRoot, "mihari"), fixture.layout.ChannelPath, fixture.manager.launchd.Plist}
	snapshots := make([]securityInstallationFileSnapshot, 0, len(paths))
	for _, path := range paths {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			snapshots = append(snapshots, securityInstallationFileSnapshot{path: path})
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, securityInstallationFileSnapshot{path: path, present: true, info: info, body: body})
	}
	return snapshots
}

func assertSecurityInstallationFileSnapshots(t *testing.T, want []securityInstallationFileSnapshot) {
	for _, snapshot := range want {
		info, err := os.Lstat(snapshot.path)
		if !snapshot.present {
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("refused recovery created %s: %v", snapshot.path, err)
			}
			continue
		}
		if err != nil || !os.SameFile(snapshot.info, info) {
			t.Fatalf("refused recovery replaced %s: %v", snapshot.path, err)
		}
		body, err := os.ReadFile(snapshot.path)
		if err != nil || !bytes.Equal(body, snapshot.body) {
			t.Fatalf("refused recovery changed %s: %v", snapshot.path, err)
		}
	}
}

func securityServiceSnapshot(ctx context.Context, manager *securityNativeManager) (string, error) {
	file, err := manager.files.Read(ctx, manager.launchd.Plist)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Sprintf("%t/%t/%t/absent", manager.running, manager.loaded, manager.disabled), nil
	}
	if err != nil {
		return "", err
	}
	link := ""
	if file.Kind == "link" || file.Kind == "mask" {
		link, err = manager.files.ReadLink(ctx, manager.launchd.Plist)
		if err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%t/%t/%t/%s", manager.running, manager.loaded, manager.disabled, service.DefinitionFileState(file, link)), nil
}
