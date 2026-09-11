//go:build linux || darwin

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/credential"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestUnixSystemLayout_ReadOnlyClientConstruction(t *testing.T) {
	defaults := platform.SystemLayoutDefaults()
	// Native default paths are inspected as values only; no host machine root IO.
	layout, err := platform.ResolveLayout(platform.LayoutInput{EUID: uint32(os.Geteuid())}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if layout.Data.Root != filepath.Join(defaults.BaseDir, "data") || layout.CredentialPath != filepath.Join(defaults.BaseDir, "control.token") || layout.ChannelPath != filepath.Join(defaults.BaseDir, "mihari-channel") {
		t.Fatalf("external discovery rejoined D: %+v", layout)
	}
	locator, err := layout.Locator(uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	if locator.ExpectedOwner != 0 {
		t.Fatal("system peer owner not fixed to root")
	}
	client := controlclient.WithCredentialProvider(locator, credential.NewProvider(locator))
	if client == nil {
		t.Fatal("readonly client unavailable")
	}
}

func TestUnixSystemLayout_UserLoggingFailureDoesNotCreateData(t *testing.T) {
	parent := t.TempDir()
	machine := filepath.Join(parent, "machine-data")
	layout := platform.ResolvedLayout{Mode: platform.SystemMode, Data: platform.NewPaths(machine)}
	fs, err := platform.OpenClientLogFS(context.Background(), layout)
	if fs != nil {
		_ = fs.Close()
		t.Fatal("empty U acquired filesystem")
	}
	if err == nil {
		t.Fatal("empty U did not report memory-only logging")
	}
	if _, err := os.Stat(machine); !os.IsNotExist(err) {
		t.Fatalf("user logging failure fell back to D: %v", err)
	}
}
