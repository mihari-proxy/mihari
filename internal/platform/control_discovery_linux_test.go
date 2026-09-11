package platform

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoveryLinux_SearchOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("ordinary UID search-only fixture")
	}
	path := filepath.Join(t.TempDir(), "search")
	if err := os.Mkdir(path, 0111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { assertDiscoveryClose(t, func() error { return os.Chmod(path, 0700) }) })
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err == nil {
		assertDiscoveryClose(t, func() error { return unix.Close(fd) })
		t.Fatal("fixture unexpectedly directory-readable")
	}
	fd, err = unix.Open(path, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer assertDiscoveryClose(t, func() error { return unix.Close(fd) })
	b := nativeDiscoveryBackend{}
	m, err := b.inspect(discoveryRef{fd: fd}, true, uint32(os.Geteuid()))
	if err != nil {
		t.Fatalf("search-only metadata rejected: %v", err)
	}
	if m.node.mode&0777 != 0111 {
		t.Fatalf("metadata wrong mode: %o", m.node.mode)
	}
}
func TestDiscoveryLinux_ReadKnownName(t *testing.T) {
	dir := t.TempDir()
	payload := strings.Repeat("a", 64)
	if err := os.WriteFile(filepath.Join(dir, "c"), []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(dir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer assertDiscoveryClose(t, func() error { return unix.Close(fd) })
	b := nativeDiscoveryBackend{}
	p := discoveryRef{fd: fd}
	r, err := b.child(p, "c")
	if err != nil {
		t.Fatal(err)
	}
	defer assertDiscoveryClose(t, func() error { return b.close(r) })
	m, err := b.inspect(r, true, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	data, err := b.read(p, "c", m)
	if err != nil {
		t.Fatalf("bounded known-name read: %v", err)
	}
	if string(data) != payload {
		t.Fatal("wrong bytes")
	}
}

func TestDiscoveryLinux_RetainsRenamedACLIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "held")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(path, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer assertDiscoveryClose(t, func() error { return unix.Close(fd) })
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	m, err := (nativeDiscoveryBackend{}).inspect(discoveryRef{fd: fd}, true, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	if m.node.mode&0777 != 0700 {
		t.Fatal("proc bridge was retargeted to replacement")
	}
}
func TestDiscoveryLinux_NoACLBridgeLeaks(t *testing.T) {
	fd, err := unix.Open(t.TempDir(), unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer assertDiscoveryClose(t, func() error { return unix.Close(fd) })
	before, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if err := discoveryFDACL(fd, true, uint32(os.Geteuid())); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("fd leak: before=%d after=%d", len(before), len(after))
	}
}
func TestDiscoveryLinux_RejectsChangedCredential(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(dir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer assertDiscoveryClose(t, func() error { return unix.Close(fd) })
	b := nativeDiscoveryBackend{}
	p := discoveryRef{fd: fd}
	r, err := b.child(p, "c")
	if err != nil {
		t.Fatal(err)
	}
	defer assertDiscoveryClose(t, func() error { return b.close(r) })
	m, err := b.inspect(r, true, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("b", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	if bytes, err := b.read(p, "c", m); err == nil || bytes != nil {
		t.Fatalf("changed credential accepted: err=%v", err)
	}
}

func TestDiscoveryLinux_ActualACLPolicy(t *testing.T) {
	path := t.TempDir()
	fd, err := unix.Open(path, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer assertDiscoveryClose(t, func() error { return unix.Close(fd) })
	for _, tc := range []struct {
		name        string
		perms, mask uint16
		strict, ok  bool
	}{{"read only", 7, 5, false, true}, {"effective foreign write", 7, 7, false, false}, {"attached ACL on app root", 5, 5, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			if err := unix.Setxattr(path, "system.posix_acl_access", posixACLFixture(tc.perms, tc.mask), 0); err != nil {
				t.Fatal(err)
			}
			err := discoveryFDACL(fd, tc.strict, uint32(os.Geteuid()))
			if (err == nil) != tc.ok {
				t.Fatalf("ACL result=%v want accepted=%v", err, tc.ok)
			}
		})
	}
	if err := unix.Removexattr(path, "system.posix_acl_access"); err != nil {
		t.Fatal(err)
	}
	if err := unix.Setxattr(path, "system.posix_acl_default", posixACLFixture(5, 5), 0); err != nil {
		t.Fatal(err)
	}
	if err := discoveryFDACL(fd, true, uint32(os.Geteuid())); err == nil {
		t.Fatal("default ACL accepted on app root")
	}
}

func TestDiscoveryLinux_CredentialSize(t *testing.T) {
	for _, size := range []int{0, 63, 64, 65, 66, 1024} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "c"), []byte(strings.Repeat("a", size)), 0600); err != nil {
				t.Fatal(err)
			}
			fd, err := unix.Open(dir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer assertDiscoveryClose(t, func() error { return unix.Close(fd) })
			b := nativeDiscoveryBackend{}
			p := discoveryRef{fd: fd}
			r, err := b.child(p, "c")
			if err != nil {
				t.Fatal(err)
			}
			defer assertDiscoveryClose(t, func() error { return b.close(r) })
			m, err := b.inspect(r, true, uint32(os.Geteuid()))
			if err != nil {
				t.Fatal(err)
			}
			data, err := b.read(p, "c", m)
			if size == 64 || size == 65 {
				if err != nil || len(data) != size {
					t.Fatalf("valid size rejected: %v", err)
				}
			} else if !errors.Is(err, ErrControlData) || data != nil {
				t.Fatalf("bad size accepted: %v", err)
			}
		})
	}
}

func TestDiscoveryLinux_DirectoryOpenRejectsFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(dir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer assertDiscoveryClose(t, func() error { return unix.Close(fd) })
	b := nativeDiscoveryBackend{}
	r, err := b.directory(discoveryRef{fd: fd}, "file")
	if err == nil {
		assertDiscoveryClose(t, func() error { return b.close(r) })
		t.Fatal("directory acquisition accepted regular inode")
	}
}
