//go:build linux || darwin

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestNativeInstallState_ForegroundDataPreparationKeepsAuthority(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	h := newInstallHarness(t, InstallDataCreate)
	layout := platform.ResolvedLayout{Mode: platform.SystemMode, Data: platform.Paths{Root: filepath.Join(root, "data")}, InstallRoot: filepath.Join(root, "install"), ControlEndpoint: filepath.Join(root, "control.sock"), CredentialPath: filepath.Join(root, "control.token")}
	h.tx.Service = nil
	s := &nativeInstallSession{layout: layout, tx: h.tx, state: nativeInstallState{Foreground: true, TransactionID: testTxnID, BootID: testBootID, Layout: layout, DataAction: InstallDataCreate, DataHash: sha256Hex(testTxnID)}}
	if err := s.prepareData(ctx, InstallRequest{}, &nativeReleaseInputs{}); err != nil {
		t.Fatal(err)
	}
	if !s.state.Foreground || s.state.DataAction != InstallDataCreate || s.state.DataHash != sha256Hex(testTxnID) || s.state.DataIdentity == "" {
		t.Fatal("preparing data discarded foreground transaction authority")
	}
	if err := s.saveState(ctx); err != nil {
		t.Fatal(err)
	}
	if s.tx.serviceEffects != nil {
		t.Fatal("foreground state required a service adapter")
	}
}

func TestNativeInstallSource_RejectsOverlapBeforeStaging(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	sourcePath := filepath.Join(root, "source")
	if err := os.Mkdir(sourcePath, 0700); err != nil {
		t.Fatal(err)
	}
	source, err := openReadOnlyMigrationRoot(ctx, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, target := range []string{sourcePath, root, filepath.Join(sourcePath, "missing", "data")} {
		if err := rejectNativeMigrationOverlap(ctx, source, target); err == nil {
			t.Fatalf("overlapping source/target accepted: %s", target)
		}
	}
	if err := rejectNativeMigrationOverlap(ctx, source, filepath.Join(root, "sibling")); err != nil {
		t.Fatalf("independent sibling rejected: %v", err)
	}
	names, err := os.ReadDir(sourcePath)
	if err != nil || len(names) != 0 {
		t.Fatalf("overlap check mutated source: entries=%d err=%v", len(names), err)
	}
}

func TestNativeInstallState_BootstrapBackupSurvivesPreparationCrash(t *testing.T) {
	ctx := context.Background()
	h := newInstallHarness(t, InstallDataCreate)
	layout := platform.ResolvedLayout{Mode: platform.SystemMode, Data: platform.Paths{Root: h.art.DataRoot}, InstallRoot: h.art.Install, ControlEndpoint: h.art.Endpoint, CredentialPath: h.art.Credential}
	h.tx.Service = nil
	s := &nativeInstallSession{layout: layout, tx: h.tx, state: nativeInstallState{Foreground: true, TransactionID: testTxnID, BootID: testBootID, Layout: layout, DataAction: InstallDataCreate}}
	if !installJournalPathAllowed("transactions/" + testTxnID + "/unit-bootstrap") {
		t.Fatal("immutable bootstrap recovery backup is not a permitted journal path")
	}
	if err := s.saveStateAt(ctx, "unit-bootstrap"); err != nil {
		t.Fatal(err)
	}
	h.tx.Artifacts.CandidateHash = sha256Hex("candidate")
	marker, err := h.tx.Store.CreateTransactionMarker(ctx, testTxnID)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := h.tx.buildJournal(InstallRequest{Operation: InstallOperationInstall, Layout: InstallLayoutSystem}, testTxnID, marker, h.tx.Artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.tx.Store.Save(ctx, journal); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after the final metadata is saved but before the journal
	// references it. The initial hash-bound backup must remain recoverable.
	s.state.DataStage = filepath.Join(filepath.Dir(layout.Data.Root), ".mihari-data-"+testTxnID)
	s.state.DataIdentity = "8:55@42"
	if err := s.saveState(ctx); err != nil {
		t.Fatal(err)
	}
	restored := &nativeInstallSession{layout: layout, tx: &InstallTransaction{Store: h.tx.Store, Artifacts: InstallArtifacts{BootID: testBootID}}}
	if present, err := restored.loadState(ctx); err != nil || !present {
		t.Fatalf("bootstrap recovery metadata lost: present=%v err=%v", present, err)
	}
	if !restored.state.Foreground || restored.state.DataStage != "" {
		t.Fatal("journal consumed uncommitted prepared metadata")
	}
	lease := &fakeInstallLease{held: true, global: true}
	if err := restored.tx.RecoverLocked(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if restored.tx.journal.Phase != InstallPhaseComplete || restored.tx.journal.RecoveryAuthority != InstallAuthoritySource {
		t.Fatal("preparation crash did not recover source authority")
	}
}

func TestNativeInstallState_CreateIdentityCoversWholeTreeAndParts(t *testing.T) {
	for _, partial := range []bool{false, true} {
		state := nativeInstallState{TransactionID: testTxnID, BootID: testBootID, DataAction: InstallDataCreate, DataIdentity: "8:11@42"}
		actual := JournalObject{Present: true, Identity: "8:11", MountID: "42", BootID: testBootID}
		if partial {
			state.TargetObject = JournalObject{Present: true, Identity: "8:12", MountID: "42", BootID: testBootID}
			state.DataParts = []nativeInstallDataPart{{Name: "locks"}}
			actual = state.TargetObject
		}
		s := nativeInstallSession{state: state}
		expected, marker := s.retainedDataExpected()
		if !retainedDataMatches(expected, marker, actual, sha256Hex(testTxnID), testBootID) {
			t.Fatalf("created directory identity not retained: partial=%v expected=%+v", partial, expected)
		}
		actual.Identity += "-replacement"
		if retainedDataMatches(expected, marker, actual, sha256Hex(testTxnID), testBootID) {
			t.Fatal("replacement directory accepted")
		}
	}
}
