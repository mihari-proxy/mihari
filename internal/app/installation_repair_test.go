package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallationRepair_PreservesEveryUserDataByte(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeRepair)
	files := map[string]string{
		"mihari.yaml": "broken: [user config", "subscriptions/catalog.yaml": "secret subscription",
		"preferences/tui.json": "{user-choice}", "logs/mihari-daemon.log": "old log", "unknown.data": "user import",
	}
	want := map[string]string{}
	for name, body := range files {
		path := filepath.Join(h.target.DataRoot, name)
		writeInstallationFixture(t, path, body)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want[name] = fmt.Sprintf("%x", sha256.Sum256(raw))
	}
	h.apply = func(_ context.Context, target InstallationManifest) (InstallationManifest, error) {
		writeInstallationFixture(t, target.Binary.Path, "new executable")
		return target, nil
	}
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan}); err != nil {
		t.Fatal(err)
	}
	for name, hash := range want {
		raw, err := os.ReadFile(filepath.Join(h.target.DataRoot, name))
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != hash {
			t.Fatalf("repair changed %s: err=%v", name, err)
		}
	}
}

func TestInstallationReset_FreshDeletesOnlyBoundManagedEntries(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeFresh)
	for _, name := range []string{"mihari.yaml", "subscriptions/catalog.yaml", "preferences/tui.json", "logs/daemon.log", "web/credential", "unknown.data", "control.token"} {
		writeInstallationFixture(t, filepath.Join(h.target.DataRoot, name), name)
	}
	h.apply = func(_ context.Context, target InstallationManifest) (InstallationManifest, error) {
		for _, entry := range h.input.Delete {
			if err := os.RemoveAll(entry.Path); err != nil {
				return InstallationManifest{}, err
			}
		}
		return target, nil
	}
	plan, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: plan, ResetConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mihari.yaml", "subscriptions", "preferences", "logs", "web"} {
		if _, err := os.Stat(filepath.Join(h.target.DataRoot, name)); !os.IsNotExist(err) {
			t.Fatalf("managed entry %s remains or stat failed: %v", name, err)
		}
	}
	for _, name := range []string{"unknown.data", "control.token"} {
		if raw, err := os.ReadFile(filepath.Join(h.target.DataRoot, name)); err != nil || string(raw) != name {
			t.Fatalf("protected entry %s changed: %q %v", name, raw, err)
		}
	}
}

func TestInstallationRepair_AfterInterruptedFreshDoesNotRestoreDeletedData(t *testing.T) {
	h := newInstallationManagerHarness(t, InstallationModeFresh)
	deletedPath := filepath.Join(h.target.DataRoot, "mihari.yaml")
	unknownPath := filepath.Join(h.target.DataRoot, "unknown.data")
	writeInstallationFixture(t, deletedPath, "old config")
	writeInstallationFixture(t, unknownPath, "user import")
	h.apply = func(_ context.Context, target InstallationManifest) (InstallationManifest, error) {
		if err := os.Remove(deletedPath); err != nil {
			return InstallationManifest{}, err
		}
		return InstallationManifest{}, fmt.Errorf("interrupted after first reset entry")
	}
	fresh, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: fresh, ResetConfirmed: true}); err == nil {
		t.Fatal("interrupted fresh reported success")
	}
	interrupted, err := DecodeInstallationState(bytes.NewReader(h.raw))
	if err != nil || interrupted.State != InstallationStateApplying {
		t.Fatalf("interrupted state=%+v err=%v", interrupted, err)
	}

	h.input.Mode = InstallationModeRepair
	h.input.Instance.RecordID = interrupted.ID
	h.input.Instance.RecordSHA256 = fmt.Sprintf("%x", sha256.Sum256(h.raw))
	h.input.Delete = []InstallationEntry{}
	h.input.Preserve = []InstallationEntry{{Path: unknownPath, Category: InstallationCategoryUnknown}, {Path: h.target.Credential, Category: InstallationCategoryCredential}}
	h.apply = nil
	repair, err := h.manager.Plan(context.Background(), h.request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.manager.Execute(context.Background(), InstallationExecuteRequest{Plan: repair}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(deletedPath); !os.IsNotExist(err) {
		t.Fatalf("repair restored fresh-deleted data: %v", err)
	}
	if raw, err := os.ReadFile(unknownPath); err != nil || string(raw) != "user import" {
		t.Fatalf("repair changed unknown data: %q %v", raw, err)
	}
	latest := h.publishedStates[len(h.publishedStates)-2]
	if latest.Base == nil || latest.Base.Binary.Version != "old" || latest.SourceScope != interrupted.SourceScope {
		t.Fatalf("repeat repair did not keep flat base/source: %+v", latest)
	}
}
