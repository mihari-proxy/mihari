//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
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
}

func bootstrapRecoverySession(t *testing.T, layout platform.ResolvedLayout, store *InstallJournalStore) *nativeInstallSession {
	t.Helper()
	ctx := context.Background()
	h := newInstallHarness(t, InstallDataCreate)
	if store != nil {
		h.tx.Store = store
	}
	h.tx.Service = nil
	s := &nativeInstallSession{layout: layout, tx: h.tx, state: nativeInstallState{Foreground: true, TransactionID: testTxnID, BootID: testBootID, Layout: layout, DataAction: InstallDataCreate}}
	if err := s.saveStateAt(ctx, "unit-bootstrap"); err != nil {
		t.Fatal(err)
	}
	s.tx.Artifacts.CandidateHash = sha256Hex("candidate")
	marker, err := s.tx.Store.CreateTransactionMarker(ctx, testTxnID)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := s.tx.buildJournal(InstallRequest{Operation: InstallOperationInstall, Layout: InstallLayoutSystem}, testTxnID, marker, s.tx.Artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.tx.Store.Save(ctx, journal); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNativeInstallState_BootstrapRollbackRejectsUnrelatedFinalMetadata(t *testing.T) {
	ctx := context.Background()
	layout := platform.ResolvedLayout{Mode: platform.SystemMode, Data: platform.Paths{Root: "/var/lib/mihari/data"}, InstallRoot: "/usr/local/lib/mihari", ControlEndpoint: "/var/lib/mihari/control.sock", CredentialPath: "/var/lib/mihari/control.token"}
	for _, field := range []string{"layout", "stage", "identity", "source", "boot", "files"} {
		t.Run(field, func(t *testing.T) {
			s := bootstrapRecoverySession(t, layout, nil)
			s.state.DataStage = filepath.Join(filepath.Dir(layout.Data.Root), ".mihari-data-"+testTxnID)
			s.state.DataIdentity = "8:55@42"
			switch field {
			case "layout":
				s.state.Layout.InstallRoot += "-other"
			case "stage":
				s.state.DataStage += "-other"
			case "identity":
				s.state.DataIdentity = ""
			case "source":
				s.state.Source = "/unrelated/source"
			case "boot":
				s.state.BootID += "-other"
			case "files":
				s.state.Files = map[string]nativeInstallFile{"unrelated": {Path: "/unrelated"}}
			}
			if err := s.saveState(ctx); err != nil {
				t.Fatal(err)
			}
			restored := &nativeInstallSession{layout: layout, tx: &InstallTransaction{Store: s.tx.Store, Artifacts: InstallArtifacts{BootID: testBootID}}}
			if present, err := restored.loadState(ctx); err != nil || !present {
				t.Fatalf("bootstrap recovery metadata: present=%v err=%v", present, err)
			}
			if err := restored.tx.RecoverLocked(ctx, &fakeInstallLease{held: true, global: true}); err == nil {
				t.Fatal("rollback completed without validating the unreferenced final metadata")
			}
		})
	}
}

func TestNativeInstallState_BootstrapRollbackRemovesUnreferencedCandidate(t *testing.T) {
	for _, crossBoot := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-boot", true: "cross-boot"}[crossBoot], func(t *testing.T) {
			ctx, root := nativeInstallFixture(t)
			layout := platform.ResolvedLayout{Mode: platform.SystemMode, Data: platform.Paths{Root: filepath.Join(root, "data")}, InstallRoot: filepath.Join(root, "install"), ControlEndpoint: filepath.Join(root, "control.sock"), CredentialPath: filepath.Join(root, "control.token")}
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
			s := bootstrapRecoverySession(t, layout, store)
			if err := s.prepareData(ctx, InstallRequest{}, &nativeReleaseInputs{}); err != nil {
				t.Fatal(err)
			}
			currentBoot := testBootID
			if crossBoot {
				currentBoot += "-next"
				s.state.DataIdentity = "previous-device:previous-inode@previous-mount"
			}
			if err := s.saveState(ctx); err != nil {
				t.Fatal(err)
			}
			// Crash before switching the main journal from unit-bootstrap to unit.
			restored := &nativeInstallSession{layout: layout, tx: &InstallTransaction{Store: s.tx.Store, Artifacts: InstallArtifacts{BootID: currentBoot}}}
			if present, err := restored.loadState(ctx); err != nil || !present {
				t.Fatalf("bootstrap recovery metadata: present=%v err=%v", present, err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				if err := restored.tx.RecoverLocked(ctx, &fakeInstallLease{held: true, global: true}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Lstat(s.state.DataStage); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unreferenced candidate survived completed rollback: %v", err)
			}
			if _, err := os.Lstat(layout.Data.Root); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rollback published foreground data: %v", err)
			}
			if restored.tx.journal.Phase != InstallPhaseComplete || restored.tx.journal.RecoveryAuthority != InstallAuthoritySource {
				t.Fatal("preparation crash did not recover source authority")
			}
		})
	}
}

func TestNativeInstallState_BootstrapCleanupAcrossBoots(t *testing.T) {
	for _, scenario := range []string{"full", "daemon-removed", "marker-removed", "locks-removed", "same-boot-marker-removed", "same-boot-locks-removed", "same-boot-replacement", "wrong-marker", "wrong-daemon", "unexpected-file", "unexpected-lock-file", "missing-marker-before-daemon"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			owner := uint32(os.Geteuid())
			stagePath := migrationTrustedTempDir(t)
			root, err := platform.OpenTrustedRoot(ctx, stagePath, platform.RootPolicy{Owner: owner, Mode: 0700})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := root.Close(); err != nil {
					t.Error(err)
				}
			})
			if scenario != "locks-removed" && scenario != "same-boot-locks-removed" {
				locks, err := root.OpenDir(ctx, "locks", platform.RootPolicy{Owner: owner, Mode: 0700, AllowCreate: true})
				if err != nil {
					t.Fatal(err)
				}
				if scenario != "marker-removed" && scenario != "same-boot-marker-removed" && scenario != "missing-marker-before-daemon" {
					marker := testTxnID
					if scenario == "wrong-marker" {
						marker += "-other"
					}
					if err := locks.WriteFile(ctx, "install-data-id", []byte(marker), 0600, nil); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "unexpected-lock-file" {
					if err := locks.WriteFile(ctx, "unknown", nil, 0600, nil); err != nil {
						t.Fatal(err)
					}
				}
				if err := locks.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "daemon-removed" && scenario != "marker-removed" && scenario != "locks-removed" && scenario != "same-boot-marker-removed" && scenario != "same-boot-locks-removed" {
				var data []byte
				if scenario == "wrong-daemon" {
					data = []byte("unexpected")
				}
				if err := root.WriteFile(ctx, "daemon.lock", data, 0600, nil); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "unexpected-file" {
				if err := root.WriteFile(ctx, "unknown", nil, 0600, nil); err != nil {
					t.Fatal(err)
				}
			}
			_, actual, _, _, err := root.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			state := nativeInstallState{Foreground: true, TransactionID: testTxnID, BootID: testBootID, DataIdentity: "previous-device:previous-inode@previous-mount"}
			currentBoot := testBootID + "-next"
			if scenario == "same-boot-replacement" {
				currentBoot = testBootID
			}
			if scenario == "same-boot-marker-removed" || scenario == "same-boot-locks-removed" {
				currentBoot, state.DataIdentity = testBootID, actual
			}
			effect := &nativeInstallEffects{state: state, transaction: &InstallTransaction{Artifacts: InstallArtifacts{BootID: currentBoot}}}
			before, err := hashNativeDataTree(ctx, stagePath)
			if err != nil {
				t.Fatal(err)
			}
			err = effect.cleanupBootstrapDataContents(ctx, root, actual)
			wantErr := scenario == "same-boot-replacement" || scenario == "wrong-marker" || scenario == "wrong-daemon" || scenario == "unexpected-file" || scenario == "unexpected-lock-file" || scenario == "missing-marker-before-daemon"
			if (err != nil) != wantErr {
				t.Fatalf("cleanup error=%v wantErr=%v", err, wantErr)
			}
			if !wantErr {
				if err := effect.cleanupBootstrapDataContents(ctx, root, actual); err != nil {
					t.Fatal("repeat cleanup", err)
				}
				names, err := root.ReadNames(ctx)
				if err != nil || len(names) != 0 {
					t.Fatalf("candidate contents remain: names=%v err=%v", names, err)
				}
			} else {
				after, err := hashNativeDataTree(ctx, stagePath)
				if err != nil || after != before {
					t.Fatalf("rejected cleanup changed candidate contents: err=%v", err)
				}
			}
		})
	}
}

func TestNativeInstallState_FinalizedForegroundCleanupKeepsRollbackShape(t *testing.T) {
	ctx := context.Background()
	root, err := platform.OpenTrustedRoot(ctx, migrationTrustedTempDir(t), platform.RootPolicy{Owner: uint32(os.Geteuid()), Mode: 0700})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	// Publishing parts into an existing private P moves its locks marker out of
	// this stage. Rollback isolates the published part instead of moving it back.
	if err := root.WriteFile(ctx, "daemon.lock", nil, 0600, nil); err != nil {
		t.Fatal(err)
	}
	_, actual, _, _, err := root.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	effect := &nativeInstallEffects{state: nativeInstallState{Foreground: true, TransactionID: testTxnID, BootID: testBootID, DataIdentity: actual}, transaction: &InstallTransaction{Artifacts: InstallArtifacts{BootID: testBootID}}}
	if err := effect.cleanupUnpublishedDataContents(ctx, root, actual); err != nil {
		t.Fatalf("finalized foreground rollback candidate refused: %v", err)
	}
	if names, err := root.ReadNames(ctx); err != nil || len(names) != 0 {
		t.Fatalf("finalized foreground candidate remained: names=%v err=%v", names, err)
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

func TestNativeInstallLifecycle_MaskedHashRequiresMatchingCompletedAuthority(t *testing.T) {
	layout := platform.ResolvedLayout{Mode: platform.SystemMode, Data: platform.Paths{Root: "/var/lib/mihari/data"}, InstallRoot: "/usr/local/lib/mihari", ControlEndpoint: "/var/lib/mihari/control.sock", CredentialPath: "/var/lib/mihari/control.token"}
	journal := InstallJournal{TransactionID: testTxnID, Phase: InstallPhaseComplete, RecoveryAuthority: InstallAuthorityTarget, Mode: InstallLayoutSystem, TargetPath: layout.Data.Root, DataRoot: layout.Data.Root, InstallPath: layout.InstallRoot, EndpointPath: layout.ControlEndpoint, CredentialPath: layout.CredentialPath, CandidateHash: sha256Hex("verified binary")}
	old := service.Definition{Masked: true}
	for _, operation := range []string{"stop", "uninstall"} {
		s := nativeInstallSession{layout: layout, state: nativeInstallState{TransactionID: testTxnID, Layout: layout}, tx: &InstallTransaction{journal: journal}}
		if hash, err := s.lifecycleCandidateHash(operation, old); err != nil || hash != journal.CandidateHash {
			t.Fatalf("completed masked %s authority: hash=%q err=%v", operation, hash, err)
		}
		// Nonexecuting cleanup must also remain available when an unmasked
		// definition's executable has disappeared since installation.
		unmasked := service.Definition{Binary: filepath.Join(t.TempDir(), "missing-binary")}
		if hash, err := s.lifecycleCandidateHash(operation, unmasked); err != nil || hash != journal.CandidateHash {
			t.Fatalf("completed unmasked %s with missing binary: hash=%q err=%v", operation, hash, err)
		}
	}
	for _, field := range []string{"transaction", "state-layout", "phase", "authority", "mode", "target", "data", "install", "endpoint", "credential", "hash", "start", "restart"} {
		t.Run(field, func(t *testing.T) {
			s := nativeInstallSession{layout: layout, state: nativeInstallState{TransactionID: testTxnID, Layout: layout}, tx: &InstallTransaction{journal: journal}}
			operation := "uninstall"
			switch field {
			case "transaction":
				s.tx.journal.TransactionID = "other"
			case "state-layout":
				s.state.Layout.InstallRoot += "-other"
			case "phase":
				s.tx.journal.Phase = InstallPhasePrepared
			case "authority":
				s.tx.journal.RecoveryAuthority = InstallAuthoritySource
			case "mode":
				s.tx.journal.Mode = InstallLayoutPrivate
			case "target":
				s.tx.journal.TargetPath += "-other"
			case "data":
				s.tx.journal.DataRoot += "-other"
			case "install":
				s.tx.journal.InstallPath += "-other"
			case "endpoint":
				s.tx.journal.EndpointPath += "-other"
			case "credential":
				s.tx.journal.CredentialPath += "-other"
			case "hash":
				s.tx.journal.CandidateHash = "unverified"
			default:
				operation = field
			}
			for _, definition := range []service.Definition{old, {Binary: filepath.Join(t.TempDir(), "missing-binary")}} {
				if _, err := s.lifecycleCandidateHash(operation, definition); err == nil {
					t.Fatalf("lifecycle borrowed invalid %s authority: masked=%v", field, definition.Masked)
				}
			}
		})
	}
}

func TestNativeInstallLifecycle_UnmaskedLegacyHashUsesReadableBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-binary")
	if err := os.WriteFile(path, []byte("legacy binary"), 0700); err != nil {
		t.Fatal(err)
	}
	s := nativeInstallSession{tx: &InstallTransaction{}}
	for _, operation := range []string{"stop", "uninstall", "start", "restart"} {
		if hash, err := s.lifecycleCandidateHash(operation, service.Definition{Binary: path}); err != nil || hash != sha256Hex("legacy binary") {
			t.Fatalf("legacy %s could not read its binary: hash=%q err=%v", operation, hash, err)
		}
	}
}
