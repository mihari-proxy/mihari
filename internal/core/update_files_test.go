package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileUpdate_RetainsBackupUntilOwnerAcceptsRollback(t *testing.T) {
	root := t.TempDir()
	name := "mihomo"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.Mkdir(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "bin", name)
	if err := os.WriteFile(binary, []byte("old core"), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := NewUpdateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	tx := "1234567890abcdef1234567890abcdef"
	for role, body := range map[ProvenanceRole][]byte{UpdateMarker: []byte(tx), UpdateCandidate: []byte("new core")} {
		if err := store.Save(ctx, role, tx, body); err != nil {
			t.Fatal(err)
		}
	}
	observed, err := store.Inspect(ctx, UpdateCandidate, tx)
	if err != nil {
		t.Fatal(err)
	}
	u, err := BeginUpdate(ctx, store, tx, observed, UpdateIntent{Previous: CoreSelection{Channel: "stable"}, Next: CoreSelection{Channel: "alpha"}, WasRunning: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(binary); err != nil || string(body) != "new core" {
		t.Fatalf("publish: %q %v", body, err)
	}
	var channel string
	if err := u.Rollback(ctx, func(selection CoreSelection) error { channel = selection.Channel; return nil }); err != nil {
		t.Fatal(err)
	}
	if channel != "stable" {
		t.Fatalf("restored channel: %q", channel)
	}
	if body, err := os.ReadFile(binary); err != nil || string(body) != "old core" {
		t.Fatalf("rollback: %q %v", body, err)
	}
	if body, err := store.Load(ctx, UpdateBackup, tx); err != nil || string(body) != "old core" {
		t.Fatalf("backup retired before health: %q %v", body, err)
	}
	// A fresh daemon must detect interruption, never execute to infer the result.
	reopened, err := NewUpdateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := authorizePendingUpdate(context.Background(), reopened); err == nil {
		t.Fatal("interrupted update permits execution")
	}
	runner := &fileUpdateRunner{}
	if _, err := (Installer{Updates: reopened, Runner: runner}).DetectVersion(ctx, binary); err == nil || len(runner.calls) != 0 {
		t.Fatalf("version probe executed an interrupted core: calls=%v, err=%v", runner.calls, err)
	}
	if err := u.CompleteRollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := CleanupCompletedUpdates(ctx, store); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx, UpdateJournal, ""); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal not retired: %v", err)
	}
}

func TestFileUpdate_RejectsChangedCandidate(t *testing.T) {
	root := t.TempDir()
	store, err := NewUpdateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	tx := "1234567890abcdef1234567890abcdef"
	if err := store.Save(t.Context(), UpdateMarker, tx, []byte(tx)); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(t.Context(), UpdateCandidate, tx, []byte("checked")); err != nil {
		t.Fatal(err)
	}
	before, err := store.Inspect(t.Context(), UpdateCandidate, tx)
	if err != nil {
		t.Fatal(err)
	}
	rel, _, err := fileUpdatePath(UpdateCandidate, tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte("changed"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err = BeginUpdate(t.Context(), store, tx, before, UpdateIntent{Previous: CoreSelection{Channel: "stable"}, Next: CoreSelection{Channel: "alpha"}})
	if err == nil {
		t.Fatal("changed candidate published")
	}
}

func TestFileUpdate_PrepareRequiresOwnerAndKeepsInstalledCore(t *testing.T) {
	root := t.TempDir()
	store, err := NewUpdateStore(root)
	if err != nil {
		t.Fatal(err)
	}
	name := "mihomo-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOARCH == "amd64" {
		name += "-compatible"
	}
	name += "-v1.19.0"
	archive := gzipFixture(t, []byte("downloaded core"))
	if runtime.GOOS == "windows" {
		name += ".zip"
		archive = zipFixture(t, []byte("downloaded core"))
	} else {
		name += ".gz"
	}
	server := releaseFixture(t, name, archive)
	defer server.Close()
	runner := &fileUpdateRunner{}
	installer := Installer{Updates: store, HTTPClient: server.Client(), APIBase: server.URL, Runner: runner}
	rel, _, err := fileUpdatePath(InstalledBinary, "")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("old core"), 0700); err != nil {
		t.Fatal(err)
	}
	candidate, err := installer.Prepare(t.Context(), InstallRequest{BinaryPath: binary, DataDir: root, ConfigPath: filepath.Join(root, "runtime", "config.yaml"), Channel: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.Cleanup()
	if len(runner.calls) != 2 {
		t.Fatalf("candidate version and config not checked: %v", runner.calls)
	}
	prepared, ok := candidate.(PreparedUpdate)
	if !ok {
		t.Fatal("ordinary candidate cannot participate in owner rollback")
	}
	if _, err := candidate.Commit(); err == nil {
		t.Fatal("candidate committed without owner health/settings acceptance")
	}
	if body, err := os.ReadFile(binary); err != nil || string(body) != "old core" {
		t.Fatalf("prepare replaced installed core: %q %v", body, err)
	}
	u, err := prepared.BeginUpdate(t.Context(), UpdateIntent{Previous: CoreSelection{Channel: "stable"}, Next: CoreSelection{Channel: "stable", Version: candidate.Version()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := u.Commit(t.Context(), func(CoreSelection) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := CleanupCompletedUpdates(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(binary); err != nil || string(body) != "downloaded core" {
		t.Fatalf("owner install failed: %q %v", body, err)
	}
}

// fileUpdateRunner records both candidate checks without executing a real core.
type fileUpdateRunner struct{ calls [][]string }

func (r *fileUpdateRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	return []byte("Mihomo Meta v1.19.0"), nil
}
