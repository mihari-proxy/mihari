//go:build linux

package sysproxy

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformBackend_RuntimeBus(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "gsettings"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", root)
	backend := PlatformBackend{}
	if err := backend.CheckWritable(t.Context()); err == nil {
		t.Error("accepted a runtime directory with no session bus")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(root, "bus"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := backend.CheckWritable(t.Context()); err != nil {
		t.Errorf("rejected a listening runtime bus: %v", err)
	}
	t.Run("explicit_address_without_runtime_directory", func(t *testing.T) {
		t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+listener.Addr().String())
		t.Setenv("XDG_RUNTIME_DIR", "")
		if err := backend.CheckWritable(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := backend.CheckWritable(t.Context()); err == nil {
		t.Error("accepted a stale bus socket")
	}
}

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

func TestCheckWritable_CanceledBeforeToolLookup(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := (PlatformBackend{}).CheckWritable(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CheckWritable returned %v, want context.Canceled", err)
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
		{"preflight", func() error { return backend.CheckWritable(t.Context()) }},
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
