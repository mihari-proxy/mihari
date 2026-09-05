//go:build darwin

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPublishWorkspace_DarwinDeleteChildACLLeavesEmptyOrphan(t *testing.T) {
	d, err := OpenPublishDir(privatePublishParent(t))
	if err != nil {
		t.Fatal(err)
	}
	cleanupPublishDir(t, d)
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
	// ACL-only DELETE_CHILD authority must override the apparently safe 0700
	// POSIX mode. This is isolated filesystem state, not a real user directory.
	if out, err := exec.Command("/bin/chmod", "+a", "everyone allow delete_child", d.Path()).CombinedOutput(); err != nil {
		t.Fatalf("set test ACL: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("/bin/chmod", "-N", d.Path()).CombinedOutput(); err != nil {
			t.Errorf("clear test ACL: %v: %s", err, out)
		}
	})
	if err := w.Close(); err == nil {
		t.Fatal("DELETE_CHILD ACL accepted")
	}
	entries, err := os.ReadDir(filepath.Join(d.Path(), w.name))
	if err != nil || len(entries) != 0 {
		t.Fatalf("orphan contents=%v error=%v", entries, err)
	}
}
