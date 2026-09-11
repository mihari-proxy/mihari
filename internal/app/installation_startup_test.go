package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

type installationStartupMemory struct {
	raw          []byte
	syncErr      error
	confirmCalls int
}

func (s *installationStartupMemory) ReadState(context.Context) ([]byte, string, error) {
	return append([]byte(nil), s.raw...), fmt.Sprintf("%x", sha256.Sum256(s.raw)), nil
}
func (s *installationStartupMemory) ConfirmState(context.Context, string) error {
	s.confirmCalls++
	return s.syncErr
}

func installationStartupFixture(t *testing.T) (InstallationState, *installationStartupMemory) {
	t.Helper()
	root := t.TempDir()
	state := InstallationState{Schema: InstallationStateSchema, ID: strings.Repeat("1", 32), State: InstallationStateComplete, Operation: InstallationOperationRepair, DataPolicy: InstallationDataRetain, ResetEntries: []string{}, Target: InstallationManifest{
		Installed: true, DataRoot: root, InstallRoot: filepath.Join(root, "program"), Endpoint: filepath.Join(root, "control.sock"), Credential: filepath.Join(root, "control.token"), DataIdentity: &InstallationIdentity{BootID: "boot-a", Key: "root-identity", Marker: "marker-a"}, Binary: &InstallationBinary{Path: filepath.Join(root, "program", "mihari"), SHA256: strings.Repeat("a", 64), Version: "1.0.0"}, DefinitionSHA256: strings.Repeat("b", 64),
	}}
	raw, err := EncodeInstallationState(state)
	if err != nil {
		t.Fatal(err)
	}
	return state, &installationStartupMemory{raw: raw}
}

func TestInstallationStartup_CompleteAndDurableBeforeScopeAccess(t *testing.T) {
	_, store := installationStartupFixture(t)
	verifications := 0
	manifest, err := ConfirmInstallationStartup(context.Background(), store, func(context.Context, InstallationManifest) error {
		if store.confirmCalls == 0 {
			t.Fatal("scope accepted before durable complete")
		}
		verifications++
		return nil
	})
	if err != nil || !manifest.Installed || verifications != 2 {
		t.Fatalf("verified=%d installed=%v err=%v", verifications, manifest.Installed, err)
	}
}

func TestInstallationStartup_ApplyingCannotStart(t *testing.T) {
	state, store := installationStartupFixture(t)
	state.State = InstallationStateApplying
	state.Owner = &InstallationOwner{BootID: "boot-a", PID: 42, Start: "123"}
	var err error
	store.raw, err = EncodeInstallationState(state)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ConfirmInstallationStartup(context.Background(), store, func(context.Context, InstallationManifest) error {
		t.Fatal("applying accessed business scope")
		return nil
	})
	if err == nil || store.confirmCalls != 0 {
		t.Fatal("applying allowed startup")
	}
}

func TestInstallationStartup_SyncAndIdentityFailuresBlockStart(t *testing.T) {
	for _, failure := range []string{"sync", "resource", "record changed", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			state, store := installationStartupFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failure == "sync" {
				store.syncErr = errors.New("sync failed")
			}
			if failure == "canceled" {
				cancel()
			}
			_, err := ConfirmInstallationStartup(ctx, store, func(context.Context, InstallationManifest) error {
				if failure == "resource" {
					return errors.New("scope changed")
				}
				if failure == "record changed" {
					state.ID = strings.Repeat("2", 32)
					store.raw, _ = EncodeInstallationState(state)
				}
				return nil
			})
			if err == nil {
				t.Fatal("unproven installation allowed startup")
			}
		})
	}
}
