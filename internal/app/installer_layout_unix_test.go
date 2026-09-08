//go:build linux || darwin

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestUnixInstaller_TrustUsesCapturedInstallRoot(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "mihari-layout-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("MIHARI_DATA", filepath.Join(root, "private"))
	t.Setenv("MIHARI_INSTALL_ROOT", filepath.Join(root, "selected-install"))
	layout, _, err := platform.CaptureLayout(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MIHARI_INSTALL_ROOT", filepath.Join(root, "later-untrusted-install"))
	installer, err := NewUnixInstaller(layout, filepath.Join(root, "mihari"), "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(layout.InstallRoot, "install-trust"); installer.offlineRoot != want {
		t.Fatalf("offline trust=%q want captured install root=%q", installer.offlineRoot, want)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("constructor or capture performed filesystem writes: %v", entries)
	}
}
