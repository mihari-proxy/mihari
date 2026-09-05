package platform

import (
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

func TestTrustedRoot_DarwinFDACLRemoval(t *testing.T) {
	path := t.TempDir()
	fd, err := unix.Open(path, trustedDirFlags, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	b := nativeTrustedBackend{}
	before, err := b.stat(fd)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.checkFS(fd); err != nil {
		t.Fatalf("positive filesystem: %v", err)
	}
	if err := b.checkACL(fd, true, uint32(os.Geteuid())); err != nil {
		t.Fatalf("positive empty ACL: %v", err)
	}
	if out, err := exec.Command("/bin/chmod", "+a", "everyone allow read,search", path).CombinedOutput(); err != nil {
		t.Fatalf("ACL fixture: %v %s", err, out)
	}
	if err := b.checkACL(fd, false, uint32(os.Geteuid())); err != nil {
		t.Fatalf("read-only traversal: %v", err)
	}
	if err := b.checkACL(fd, true, uint32(os.Geteuid())); err == nil {
		t.Fatal("creation accepted attached ACL")
	}
	if err := clearTrustedDirectoryACL(fd); err != nil {
		t.Fatal(err)
	}
	if err := b.checkACL(fd, true, uint32(os.Geteuid())); err != nil {
		t.Fatalf("fd removal did not clear ACL: %v", err)
	}
	after, err := b.stat(fd)
	if err != nil || after != before {
		t.Fatalf("ACL removal changed owner/group/mode: %+v %+v %v", before, after, err)
	}
}
