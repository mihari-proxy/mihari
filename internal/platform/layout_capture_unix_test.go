//go:build linux || darwin

package platform

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureLayout_PrivateCapturesInstallOverride(t *testing.T) {
	root := filepath.Join(leaseTempDir(t), "portable")
	t.Setenv("MIHARI_DATA", root)
	t.Setenv("MIHARI_INSTALL_ROOT", "relative-install")
	t.Setenv("MIHARI_CONTROL_ENDPOINT", filepath.Join(filepath.Dir(root), "external.sock"))
	t.Setenv("MIHARI_CONTROL_CREDENTIAL", filepath.Join(filepath.Dir(root), "external.token"))
	t.Setenv("HOME", "/must-not-be-consulted-for-private-layout")
	layout, uid, err := CaptureLayout(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if uid != uint32(os.Geteuid()) || layout.Data.Root != root || layout.ClientLogs.Root != root || layout.Mode != PrivateMode {
		t.Fatalf("private layout=%+v uid=%d", layout, uid)
	}
	if layout.InstallRoot != filepath.Join(cwd, "relative-install") {
		t.Fatalf("install override not captured: %s", layout.InstallRoot)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("capture created private root: %v", err)
	}
}
