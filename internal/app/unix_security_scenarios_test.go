//go:build unix_security && (linux || darwin)

package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
)

type securityCrashScenario struct {
	name                                                                 string
	system, retained, running, enabled, migration, resources, pathBinary bool
}

func securityCrashScenarios() []securityCrashScenario {
	return []securityCrashScenario{
		{name: "fresh-system", system: true},
		{name: "fresh-private"},
		{name: "migration-path", migration: true, pathBinary: true},
		{name: "aio-resources-path", resources: true, pathBinary: true},
		{name: "update-running-enabled", retained: true, running: true, enabled: true},
		{name: "update-running-disabled", retained: true, running: true},
		{name: "update-stopped-enabled", retained: true, enabled: true},
		{name: "update-stopped-disabled", retained: true},
	}
}
func newSecurityScenario(t *testing.T, scenario securityCrashScenario) *securityNativeInstall {
	t.Helper()
	f := newSecurityNativeInstall(t)
	if scenario.system {
		layout, err := platform.ResolveLayout(platform.LayoutInput{InstallRoot: f.layout.InstallRoot, EUID: 0}, platform.SystemLayoutDefaults())
		if err != nil {
			t.Fatal(err)
		}
		f = securityFixtureForLayout(t, layout)
	}
	if scenario.retained {
		s := f.open(t)
		req := f.prepare(t, s)
		if _, err := s.tx.ApplyLocked(context.Background(), s, req); err != nil {
			t.Fatal("retained scenario seed", err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		f.manager.running = scenario.running
		f.manager.loaded = scenario.running
		f.manager.disabled = !scenario.enabled
		if runtime.GOOS == "darwin" && !scenario.running {
			boot, err := installBootIdentity()
			if err != nil {
				t.Fatal(err)
			}
			authority := service.Definition{Status: service.StatusStopped, Process: securityNativeProcessIdentity(boot, 123, 100, 0)}
			f.manager.stopAuthority = &authority
			f.manager.stopBoot = boot
		}
		// An update must actually publish a changed, still-parseable definition
		// on launchd too (it does not temporarily replace a plist with a mask).
		f.definition.Files[0].Bytes = append(append([]byte(nil), f.definition.Files[0].Bytes...), '\n')
		if runtime.GOOS == "linux" && !scenario.enabled {
			if err := f.manager.files.Remove(context.Background(), f.manager.paths.WantsLink); err != nil {
				t.Fatal(err)
			}
		}
	}
	if scenario.retained && scenario.running && scenario.enabled && runtime.GOOS == "linux" {
		if err := os.MkdirAll(f.manager.paths.DropinDir, 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(f.manager.paths.DropinDir, "legacy.conf")
		if err := f.manager.files.Write(context.Background(), service.DefinitionFile{Path: path, Kind: "dropin", Bytes: []byte("[Service]\n"), Owner: 0, Mode: 0644}); err != nil {
			t.Fatal(err)
		}
		f.manager.dropins = []string{path}
	}
	f.scenario = scenario
	if scenario.migration {
		f.source = newMigrationFixture(t)
	}
	return f
}
func (f *securityNativeInstall) inputs(t *testing.T) *nativeReleaseInputs {
	t.Helper()
	input := &nativeReleaseInputs{binary: []byte("native-candidate-" + f.scenario.name), resources: map[string][]byte{}}
	if f.scenario.name == "fresh-private" {
		input.resources["web/panels/zashboard/native/index.html"] = []byte("initial fixture content")
	}
	if f.scenario.resources {
		input.core = []byte("verified-input-core-fixture")
		input.receipt = []byte("verified-input-provenance-fixture")
		input.resources["geoip/GeoLite2-Country.mmdb"] = readMigrationMMDB(t, "country.mmdb")
		input.resources["web/panels/zashboard/native/index.html"] = []byte("isolated bundled panel")
	}
	if f.source != nil {
		source, err := openReadOnlyMigrationRoot(context.Background(), f.source.source.Path())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := source.Close(); err != nil {
				t.Error(err)
			}
		})
		input.source = source
		input.binary = f.source.binary
		input.trust = f.source.trust
	}
	return input
}
func (f *securityNativeInstall) assertSource(t *testing.T) {
	t.Helper()
	if f.source != nil {
		assertMigrationSourceUnchanged(t, f.source)
	}
}
func assertMigrationSourceUnchanged(t *testing.T, fx *migrationFixture) {
	t.Helper()
	got := fx.sourceHashes(t)
	if len(got) != len(fx.sourceSnap) {
		t.Fatal("native recovery changed source inventory")
	}
	for name, hash := range fx.sourceSnap {
		if got[name] != hash {
			t.Fatal("native recovery changed source", name)
		}
	}
}
func requireSecurityAction(t *testing.T, actions []JournalAction, kind, role string) {
	t.Helper()
	for _, a := range actions {
		if a.Kind == kind && a.TargetRole == role {
			return
		}
	}
	t.Fatalf("required native action absent: %s/%s", kind, role)
}
func assertSecurityInventory(t *testing.T, f *securityNativeInstall, s *nativeInstallSession) {
	t.Helper()
	for _, pair := range [][2]string{{JournalActionManagedBinary, JournalRoleManagedBinary}, {JournalActionChannel, JournalRoleChannel}, {JournalActionDefinition, JournalRoleDefinition}, {JournalActionValidationStart, JournalRoleValidation}, {JournalActionValidationStop, JournalRoleValidation}, {JournalActionActivation, JournalRoleActivation}} {
		requireSecurityAction(t, s.tx.journal.Actions, pair[0], pair[1])
	}
	if len(f.manager.dropins) > 0 {
		requireSecurityAction(t, s.tx.journal.Actions, JournalActionDropin, JournalRoleDefinition)
	}
	if f.scenario.pathBinary {
		requireSecurityAction(t, s.tx.journal.Actions, JournalActionPathBinary, JournalRolePathBinary)
	}
	if !f.scenario.retained {
		requireSecurityAction(t, s.tx.journal.Actions, JournalActionDataPublish, JournalRoleData)
	}
	if f.scenario.retained {
		if s.tx.journal.DataAction != InstallDataRetain || s.tx.journal.OldRunning != f.scenario.running || s.tx.journal.OldEnabled != f.scenario.enabled {
			t.Fatal("scenario did not observe required retained service state")
		}
		if f.scenario.running {
			requireSecurityAction(t, s.tx.journal.Actions, JournalActionStop, JournalRoleDefinition)
		}
		if runtime.GOOS == "darwin" {
			if f.scenario.enabled || f.scenario.running {
				requireSecurityAction(t, s.tx.journal.Actions, JournalActionDisabled, JournalRoleDefinition)
			}
		} else {
			requireSecurityAction(t, s.tx.journal.Actions, JournalActionMask, JournalRoleDefinition)
		}
		if f.scenario.running {
			requireSecurityAction(t, s.tx.journal.Actions, JournalActionStart, JournalRoleDefinition)
		}
	}
	if f.scenario.migration || f.scenario.resources {
		wanted := []string{"bin", "geoip", "web"}
		if f.scenario.migration {
			wanted = append(wanted, "runtime", "subscriptions", "mihari.yaml")
		}
		for _, name := range wanted {
			found := false
			for _, a := range s.tx.journal.Actions {
				if a.Kind == JournalActionDataPublish && a.TargetRole == JournalRoleData && a.CandidateRef == "data:"+sha256Hex(name) {
					found = true
				}
			}
			if !found {
				t.Fatal("native data-part publication absent", name)
			}
		}
	}
	f.assertSource(t)
}
func assertSecurityReverseInventory(t *testing.T, scenario securityCrashScenario, actions []JournalAction) {
	t.Helper()
	requireSecurityAction(t, actions, JournalActionRestoreManagedBinary, JournalRoleManagedBinary)
	requireSecurityAction(t, actions, JournalActionRestoreChannel, JournalRoleChannel)
	if !scenario.retained {
		requireSecurityAction(t, actions, JournalActionDataIsolate, JournalRoleData)
	}
	if scenario.running {
		requireSecurityAction(t, actions, JournalActionRestoreStart, JournalRoleDefinition)
	}
	if scenario.pathBinary {
		requireSecurityAction(t, actions, JournalActionRestorePathBinary, JournalRolePathBinary)
	}
	if scenario.retained && (scenario.enabled || scenario.running) && runtime.GOOS == "darwin" {
		requireSecurityAction(t, actions, JournalActionRestoreDisabled, JournalRoleDefinition)
	}
	// Service definition publication is after activation; source rollback restores
	// its recorded old definition through the real adapter's own journal hooks.
}

func (f *securityNativeInstall) assertRecoveredService(t *testing.T, session *nativeInstallSession, authority string) {
	t.Helper()
	got, err := session.tx.Service.InspectDefinition(context.Background())
	if err != nil {
		t.Fatal("recovered native service inspection", err)
	}
	if f.scenario.retained {
		if got.Running != f.scenario.running || got.Enabled != f.scenario.enabled {
			t.Fatal("recovery changed preserved running/enabled policy")
		}
	} else if authority == InstallAuthoritySource {
		if got.Status != service.StatusNotInstalled {
			t.Fatal("source recovery retained new service")
		}
	} else if got.Status == service.StatusNotInstalled || got.Running || !got.Enabled {
		t.Fatal("target recovery lost new stopped/enabled service")
	}
}
