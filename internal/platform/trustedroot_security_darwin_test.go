//go:build unix_security && darwin

package platform

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSecurityCreationACL(t *testing.T)  { securityDarwinACL(t) }
func TestSecurityDarwinACLABI(t *testing.T) { securityDarwinACL(t) }

func securityDarwinACL(t *testing.T) {
	parent := securityTrustedParent(t)
	for index, acl := range []string{"everyone allow read,search", "everyone deny write,delete", "everyone allow read,search,directory_inherit,only_inherit"} {
		path := filepath.Join(parent, fmt.Sprintf("%s-%d", t.Name(), index))
		root, err := OpenTrustedRoot(context.Background(), path, RootPolicy{Mode: 0700, AllowCreate: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := root.Close(); err != nil {
				t.Error(err)
			}
		})
		fd := root.chain[len(root.chain)-1].fd
		if err := (nativeTrustedBackend{}).checkFS(fd); err != nil {
			t.Fatal(err)
		}
		before, err := root.OpenDir(context.Background(), "before", RootPolicy{Mode: 0700, AllowCreate: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := before.Close(); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("/bin/chmod", "+a", acl, path).CombinedOutput(); err != nil {
			t.Fatalf("native ACL: %v %s", err, out)
		}
		denied, err := root.OpenDir(context.Background(), "denied", RootPolicy{Mode: 0700, AllowCreate: true})
		if denied != nil {
			_ = denied.Close()
			t.Fatal("creation accepted ACL")
		}
		if !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
		var oldACLStat unix.Stat_t
		if err := unix.Fstat(fd, &oldACLStat); err != nil {
			t.Fatal(err)
		}
		if err := clearTrustedDirectoryACL(fd); err != nil {
			t.Fatal(err)
		}
		var clearedStat unix.Stat_t
		if err := unix.Fstat(fd, &clearedStat); err != nil {
			t.Fatal(err)
		}
		if oldACLStat.Uid != clearedStat.Uid || oldACLStat.Gid != clearedStat.Gid || oldACLStat.Mode != clearedStat.Mode || oldACLStat.Ino != clearedStat.Ino {
			t.Fatal("ACL removal changed owner/group/mode/identity")
		}
		var fs unix.Statfs_t
		if err := unix.Fstatfs(fd, &fs); err != nil {
			t.Fatal(err)
		}
		attrs, err := getDiscoveryDarwinAttrs(fd, ".", true)
		if err != nil {
			t.Fatal(err)
		}
		if attrs.fsid != fs.Fsid.Val {
			t.Fatal("REALFSID and Fstatfs disagree")
		}
		after, err := root.OpenDir(context.Background(), "after", RootPolicy{Mode: 0700, AllowCreate: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := errors.Join(after.Close(), root.Close()); err != nil {
			t.Fatal(err)
		}
	}
}
