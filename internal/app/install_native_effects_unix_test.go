//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
	"path/filepath"
	"testing"
)

// These fixtures require an isolated root CI job and a root-owned, non-writable
// TMPDIR ancestry. They never invoke the host service manager or a real core.
func nativeInstallFixture(t *testing.T) (context.Context, string) {
	t.Helper()
	if os.Getenv("MIHARI_NATIVE_INSTALL_TEST") != "1" || os.Geteuid() != 0 {
		t.Skip("isolated native root fixture is not enabled")
	}
	root, err := os.MkdirTemp(os.TempDir(), "ni-")
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err == nil {
		original, statErr := os.Lstat(root)
		if statErr != nil {
			t.Fatal(statErr)
		}
		t.Cleanup(func() {
			current, statErr := os.Lstat(root)
			if statErr != nil || !os.SameFile(original, current) {
				t.Error("native fixture cleanup identity changed", statErr)
				return
			}
			if err := os.RemoveAll(root); err != nil {
				t.Error(err)
			}
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cap, err := platform.OpenTrustedRoot(ctx, root, platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		t.Fatal(err)
	}
	if err := cap.Close(); err != nil {
		t.Fatal(err)
	}
	return ctx, root
}
func TestNativeInstallEffects_FilePublicationAndActualBackup(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	path := filepath.Join(root, "mihari")
	if err := os.WriteFile(path, []byte("old binary"), 0755); err != nil {
		t.Fatal(err)
	}
	file, old, err := prepareNativeInstallFile(ctx, testTxnID, JournalRoleManagedBinary, path, 0755, []byte("new binary"))
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != "old binary" || file.OldIdentity == "" || file.RestoreIdentity == "" {
		t.Fatal("missing actual backup identities")
	}
	x := &InstallTransaction{Artifacts: InstallArtifacts{BootID: "fixture-boot"}}
	effect := &nativeInstallEffects{transaction: x, state: nativeInstallState{TransactionID: testTxnID, BootID: "fixture-boot", Files: map[string]nativeInstallFile{JournalRoleManagedBinary: file}}}
	action := JournalAction{Kind: JournalActionManagedBinary, TargetRole: JournalRoleManagedBinary, OldState: file.OldHash, NewState: file.NewHash}
	if err := effect.Apply(ctx, action); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "new binary" {
		t.Fatalf("published binary=%q err=%v", raw, err)
	}
	action.Kind = JournalActionRestoreManagedBinary
	action.OldState, action.NewState = action.NewState, action.OldState
	if err := effect.Apply(ctx, action); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil || string(raw) != "old binary" {
		t.Fatalf("restored binary=%q err=%v", raw, err)
	}
	if err := os.WriteFile(path, []byte("unrecognized replacement"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := effect.Observe(ctx, action); err == nil {
		t.Fatal("accepted unknown binary state")
	}
}
func TestNativeInstallEffects_PrivatePublicationRetainsLockIdentity(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	data := filepath.Join(root, "private")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(data, "daemon.lock")
	if err := os.WriteFile(lockPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := platform.ResolveLayout(platform.LayoutInput{Data: data, InstallRoot: filepath.Join(root, "install"), EUID: 0}, platform.SystemLayoutDefaults())
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, ".mihari-data-"+testTxnID)
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(stage, "subscriptions"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "subscriptions", "catalog.json"), []byte("candidate catalog"), 0600); err != nil {
		t.Fatal(err)
	}
	session := &nativeInstallSession{layout: layout, state: nativeInstallState{TransactionID: testTxnID, DataStage: stage, Layout: layout, DataAction: InstallDataCreate}}
	if err := session.prepareDataParts(ctx); err != nil {
		t.Fatal(err)
	}
	x := &InstallTransaction{Artifacts: InstallArtifacts{BootID: "fixture-boot"}}
	session.state.BootID = "fixture-boot"
	effect := &nativeInstallEffects{transaction: x, state: session.state}
	part := session.state.DataParts[0]
	action := JournalAction{Kind: JournalActionDataPublish, TargetRole: JournalRoleData, CandidateRef: "data:" + sha256Hex(part.Name), OldState: "absent", NewState: part.Hash}
	if err := effect.applyDataPart(ctx, action); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "subscriptions", "catalog.json")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(lockPath)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("live private data lock was replaced")
	}
	action.Kind = JournalActionDataIsolate
	action.OldState, action.NewState = action.NewState, "absent"
	if err := effect.applyDataPart(ctx, action); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "subscriptions")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial publication not isolated: %v", err)
	}
	after, err = os.Stat(lockPath)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("rollback replaced private data lock")
	}
}

func TestNativeInstallSession_PrivateMetadataBindsRecoveryAuthority(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	capability, err := platform.OpenTrustedRoot(ctx, root, platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := capability.Close(); err != nil {
			t.Error(err)
		}
	})
	store, err := NewInstallJournalStore(ctx, capability)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := platform.ResolveLayout(platform.LayoutInput{Data: root, InstallRoot: filepath.Join(root, "install"), EUID: 0}, platform.SystemLayoutDefaults())
	if err != nil {
		t.Fatal(err)
	}
	transaction := &InstallTransaction{Store: store, Artifacts: InstallArtifacts{BootID: "fixture-boot"}}
	session := &nativeInstallSession{base: capability, layout: layout, tx: transaction, state: nativeInstallState{Foreground: true, TransactionID: testTxnID, BootID: "fixture-boot", Layout: layout, DataAction: InstallDataRetain}}
	if err := session.saveState(ctx); err != nil {
		t.Fatal(err)
	}
	transaction.Artifacts.CandidateHash = sha256Hex("candidate")
	marker, err := store.CreateTransactionMarker(ctx, testTxnID)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := transaction.buildJournal(InstallRequest{Operation: InstallOperationRecover, Layout: InstallLayoutPrivate}, testTxnID, marker, transaction.Artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, journal); err != nil {
		t.Fatal(err)
	}
	restored := &nativeInstallSession{base: capability, layout: layout, tx: &InstallTransaction{Store: store, Artifacts: InstallArtifacts{BootID: "fixture-boot"}}}
	present, err := restored.loadState(ctx)
	if err != nil || !present {
		t.Fatalf("actual private backup did not load: present=%v err=%v", present, err)
	}
	if restored.tx.preparedAuthority.ServiceBackup.Identity != journal.ServiceBackup.Identity || journal.ServiceBackup.Identity == "8:3003" {
		t.Fatal("recovery used fabricated backup identity")
	}
	path := filepath.Join(root, "transactions", testTxnID, "unit")
	if err := os.WriteFile(path, []byte("tampered authority"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.loadState(ctx); err == nil {
		t.Fatal("accepted tampered private recovery authority")
	}
}
