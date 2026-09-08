//go:build unix_security && (linux || darwin)

package app

import (
	"context"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
)

type launchdRecoveryFixture struct {
	service.DefinitionStore
	file                             service.DefinitionFile
	group                            string
	occupied, disabled               bool
	groupChecks, fileWrites, signals int
}

func (f *launchdRecoveryFixture) Read(context.Context, string) (service.DefinitionFile, error) {
	return f.file, nil
}
func (f *launchdRecoveryFixture) Write(context.Context, service.DefinitionFile) error {
	f.fileWrites++
	return errors.New("unexpected definition publication")
}
func (f *launchdRecoveryFixture) Run(_ context.Context, argv []string) (service.CommandResult, error) {
	switch argv[1] {
	case "print":
		return service.CommandResult{ExitCode: 1, Stderr: []byte("Could not find service \"system/mihari\".\n")}, nil
	case "print-disabled":
		value := "false"
		if f.disabled {
			value = "true"
		}
		return service.CommandResult{Stdout: []byte("{\n \"mihari\" => " + value + "\n}\n")}, nil
	case "disable":
		f.disabled = true
		return service.CommandResult{}, nil
	case "enable":
		f.disabled = false
		return service.CommandResult{}, nil
	default:
		return service.CommandResult{}, errors.New("unexpected manager effect")
	}
}
func (f *launchdRecoveryFixture) BootIdentity(context.Context) (string, error) {
	return testBootID, nil
}
func (f *launchdRecoveryFixture) Empty(_ context.Context, group string) (bool, error) {
	f.groupChecks++
	if group != f.group {
		return false, errors.New("recovery used a different group")
	}
	if f.occupied {
		return false, errors.New("fixture group still has an owned descendant")
	}
	return true, nil
}
func (f *launchdRecoveryFixture) SignalGroup(context.Context, string, string) error {
	f.signals++
	return errors.New("unexpected group signal")
}
func (f *launchdRecoveryFixture) Lookup(context.Context, service.ProcessIdentity) (bool, error) {
	return false, nil
}
func (f *launchdRecoveryFixture) Identify(context.Context, int) (service.ProcessIdentity, error) {
	return service.ProcessIdentity{}, nil
}
func (f *launchdRecoveryFixture) SignalIdentity(context.Context, service.ProcessIdentity, string) error {
	f.signals++
	return errors.New("unexpected process signal")
}

func TestNativeLaunchdRecovery_RetainsUnloadedGroupFromBackup(t *testing.T) {
	ctx := context.Background()
	h := newInstallHarness(t, InstallDataRetain)
	layout := platform.ResolvedLayout{Mode: platform.SystemMode, Data: platform.Paths{Root: h.art.DataRoot}, InstallRoot: h.art.Install, ControlEndpoint: h.art.Endpoint, CredentialPath: h.art.Credential}
	definition, err := service.BuildUnixDefinition(layout, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	id := service.ProcessIdentity{PID: 77, BootID: testBootID, StartUnix: 1700000000, StartUsec: 42, Group: "darwin-pgid-v1:" + testBootID + ":77:1700000000:42"}
	fixture := &launchdRecoveryFixture{file: definition.Files[0], group: id.Group, occupied: true}
	newSession := func() *nativeInstallSession {
		tx := &InstallTransaction{Store: h.tx.Store, Artifacts: InstallArtifacts{BootID: testBootID}}
		tx.Service = service.NewSecurityLaunchdAdapter(fixture, fixture, fixture, tx.journaledHook, service.DefaultLaunchdPaths())
		return &nativeInstallSession{layout: layout, tx: tx}
	}
	prepared := newSession()
	prepared.state = nativeInstallState{TransactionID: testTxnID, BootID: testBootID, Layout: layout, DataAction: InstallDataRetain, OldDefinition: service.Definition{Status: service.StatusStopped, Enabled: true, Process: id}, TargetDefinition: definition}
	if err := prepared.saveState(ctx); err != nil {
		t.Fatal(err)
	}
	marker, err := prepared.tx.Store.CreateTransactionMarker(ctx, testTxnID)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := prepared.tx.buildJournal(h.req, testTxnID, marker, prepared.tx.preparedArtifacts(h.req))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.tx.Store.Save(ctx, journal); err != nil {
		t.Fatal(err)
	}
	recovered := newSession()
	if present, err := recovered.loadState(ctx); err != nil || !present {
		t.Fatal("private backup failed to reload", err)
	}
	if recovered.state.OldDefinition.Process != id {
		t.Fatal("group identity did not round trip in existing backup")
	}
	lease := &fakeInstallLease{global: true, held: true}
	if err := recovered.tx.RecoverLocked(ctx, lease); err == nil {
		t.Fatal("unloaded job with surviving group authorized recovery")
	}
	if fixture.groupChecks == 0 || fixture.fileWrites != 0 || fixture.signals != 0 || recovered.tx.journal.Phase == InstallPhaseComplete {
		t.Fatal("unresolved group did not retain the recovery barrier")
	}
	fixture.occupied = false
	if err := recovered.tx.RecoverLocked(ctx, lease); err != nil {
		t.Fatal("empty recorded group could not finish recovery", err)
	}
	if recovered.tx.journal.Phase != InstallPhaseComplete || recovered.state.OldDefinition.Process != id {
		t.Fatal("recovery lost its recorded authority")
	}
}

func TestNativeInstallState_RejectsBackupBootMismatch(t *testing.T) {
	ctx := context.Background()
	h := newInstallHarness(t, InstallDataRetain)
	layout := platform.ResolvedLayout{Mode: platform.SystemMode, Data: platform.Paths{Root: h.art.DataRoot}, InstallRoot: h.art.Install, ControlEndpoint: h.art.Endpoint, CredentialPath: h.art.Credential}
	s := &nativeInstallSession{layout: layout, tx: h.tx, state: nativeInstallState{Foreground: true, TransactionID: testTxnID, BootID: "different-backup-boot", Layout: layout, DataAction: InstallDataRetain}}
	if err := s.saveState(ctx); err != nil {
		t.Fatal(err)
	}
	marker, err := s.tx.Store.CreateTransactionMarker(ctx, testTxnID)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := s.tx.buildJournal(h.req, testTxnID, marker, s.tx.preparedArtifacts(h.req))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.tx.Store.Save(ctx, journal); err != nil {
		t.Fatal(err)
	}
	restored := &nativeInstallSession{layout: layout, tx: &InstallTransaction{Store: s.tx.Store, Artifacts: InstallArtifacts{BootID: testBootID}}}
	if present, err := restored.loadState(ctx); err == nil || present {
		t.Fatal("hash-bound backup with inconsistent boot was accepted")
	}
}
