//go:build linux

package sysproxy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGetContext_CanceledBeforeCommandStarts(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "executed")
	script := "#!/bin/sh\n: > \"" + marker + "\"\nprintf \"'none'\\n\"\n"
	if err := os.WriteFile(filepath.Join(root, "gsettings"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := getContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command executed: %v", err)
	}
}

func TestPlatformBackend_NoSessionRejectsWritesBeforeCommand(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "executed")
	script := "#!/bin/sh\n: > \"" + marker + "\"\n"
	if err := os.WriteFile(filepath.Join(root, "gsettings"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	backend := PlatformBackend{}
	for _, tc := range []struct {
		name  string
		write func() error
	}{
		{"preflight", backend.CheckWritable},
		{"enable", func() error { return backend.Enable("127.0.0.1", 9190) }},
		{"disable", backend.Disable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.write(); err == nil {
				t.Fatal("write accepted without a desktop session")
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("command executed before session rejection: %v", err)
			}
		})
	}
}
