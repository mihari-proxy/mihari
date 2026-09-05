package platform

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoveryDarwin_SearchOnlyTail(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("ordinary UID search-only fixture")
	}
	dir := t.TempDir()
	child := filepath.Join(dir, "search")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("a", 64)
	if err := os.WriteFile(filepath.Join(child, "c"), []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(child, 0111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { assertDiscoveryClose(t, func() error { return os.Chmod(child, 0700) }) })
	fd, err := unix.Open(child, trustedDirFlags, 0)
	if err == nil {
		assertDiscoveryClose(t, func() error { return unix.Close(fd) })
		t.Fatal("fixture unexpectedly readable")
	}
	fd, err = unix.Open(dir, trustedDirFlags, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer assertDiscoveryClose(t, func() error { return unix.Close(fd) })
	b := nativeDiscoveryBackend{}
	parent := discoveryRef{fd: fd, tail: "."}
	r, err := b.child(parent, "search")
	if err != nil {
		t.Fatal(err)
	}
	m, err := b.inspect(r, true, uint32(os.Geteuid()))
	if err != nil {
		t.Fatalf("search-only combined metadata: %v", err)
	}
	if m.node.mode&0777 != 0111 {
		t.Fatal("wrong metadata mode")
	}
	c, err := b.child(r, "c")
	if err != nil {
		t.Fatal(err)
	}
	cm, err := b.inspect(c, true, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	data, err := b.read(r, "c", cm)
	if err != nil || string(data) != payload {
		t.Fatalf("known-name read failed: %v", err)
	}
}
