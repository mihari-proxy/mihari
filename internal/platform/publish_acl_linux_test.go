//go:build linux

package platform

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPublishWorkspace_LinuxAccessACLChangeLeavesEmptyOrphan(t *testing.T) {
	d, err := OpenPublishDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	w, err := d.CreateWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	f, _, err := w.CreateTemp("payload-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// Linux POSIX ACL version 2: owner rwx, named UID 4242 rwx,
	// group r-x, mask rwx, other ---. The extra writer is real kernel state.
	acl := make([]byte, 4+5*8)
	binary.LittleEndian.PutUint32(acl, 2)
	for i, entry := range []struct {
		tag, perm uint16
		id        uint32
	}{{1, 7, 0xffffffff}, {2, 7, 4242}, {4, 5, 0xffffffff}, {16, 7, 0xffffffff}, {32, 0, 0xffffffff}} {
		offset := 4 + i*8
		binary.LittleEndian.PutUint16(acl[offset:], entry.tag)
		binary.LittleEndian.PutUint16(acl[offset+2:], entry.perm)
		binary.LittleEndian.PutUint32(acl[offset+4:], entry.id)
	}
	if err := unix.Fsetxattr(d.plat.fd, "system.posix_acl_access", acl, 0); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err == nil {
		t.Fatal("ACL writer accepted")
	}
	entries, err := os.ReadDir(filepath.Join(d.Path(), w.name))
	if err != nil || len(entries) != 0 {
		t.Fatalf("orphan contents=%v error=%v", entries, err)
	}
}
