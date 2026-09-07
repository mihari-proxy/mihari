//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
	"path/filepath"
	"testing"
)

func TestUnixProcess_MissingPrivateDaemonDoesNotCreateData(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "portable")
	t.Setenv("MIHARI_DATA", root)
	t.Setenv("MIHARI_CONTROL_ENDPOINT", "")
	t.Setenv("MIHARI_CONTROL_CREDENTIAL", "")
	t.Setenv("HOME", filepath.Join(parent, "ignored-home"))
	t.Setenv("SUDO_USER", "ignored-user")
	resetProcessLocalRoot()
	defer resetProcessLocalRoot()
	newPrivateFS = func(string) (*platform.PrivateFS, error) {
		t.Fatal("Unix client invoked legacy writable data-root initialization")
		return nil, nil
	}
	var out bytes.Buffer
	if code := executeProcess(context.Background(), []string{"status", "--json"}, &out, &out); code == 0 {
		t.Fatal("missing daemon unexpectedly available")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("client created data root: %v", err)
	}
}
