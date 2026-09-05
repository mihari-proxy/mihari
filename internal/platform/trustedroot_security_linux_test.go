//go:build unix_security && linux

package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestTrustedRoot_RejectsNestedBindMount(t *testing.T) {
	parent := securityTrustedParent(t)
	if os.Getenv("MIHARI_SECURITY_MOUNT_NAMESPACE") != "isolated" {
		t.Fatal("security runner must provide an isolated mount namespace")
	}
	target := filepath.Join(parent, "rootpolicy-bind")
	r, err := OpenTrustedRoot(context.Background(), target, RootPolicy{Mode: 0700, AllowCreate: true})
	if err != nil {
		t.Fatalf("positive root: %v", err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
		for _, name := range []string{"source", "mount"} {
			if err := os.Remove(filepath.Join(target, name)); err != nil {
				t.Error(err)
			}
		}
		if err := os.Remove(target); err != nil {
			t.Error(err)
		}
	})
	for _, name := range []string{"source", "mount"} {
		d, err := r.OpenDir(context.Background(), name, RootPolicy{Mode: 0700, AllowCreate: true})
		if err != nil {
			t.Fatalf("positive child: %v", err)
		}
		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.Mount(filepath.Join(target, "source"), filepath.Join(target, "mount"), "", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Unmount(filepath.Join(target, "mount"), 0); err != nil {
			t.Error(err)
		}
	})
	d, err := r.OpenDir(context.Background(), "mount", RootPolicy{Mode: 0700})
	if d != nil {
		d.Close()
		t.Fatal("accepted nested bind mount")
	}
	if !errors.Is(err, os.ErrPermission) || errors.Is(err, ErrUnsafeComponent) {
		t.Fatalf("wrong mount rejection: %v", err)
	}
}
