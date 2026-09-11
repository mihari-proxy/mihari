//go:build unix_security && linux

package platform

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSecurityBindMountDenied(t *testing.T) {
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
	// The independent runner archive imports this synced identity record before
	// cleanup. A TERM between mount and test cleanup remains recoverable.
	record := map[string]any{"source": filepath.Join(target, "source"), "target": filepath.Join(target, "mount")}
	for _, name := range []string{"source", "mount"} {
		var st unix.Stat_t
		if err := unix.Lstat(filepath.Join(target, name), &st); err != nil {
			t.Fatal(err)
		}
		record[name+"_identity"] = map[string]any{"dev": st.Dev, "ino": st.Ino, "uid": st.Uid, "mode": st.Mode & 0777}
	}
	namespace, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		t.Fatal(err)
	}
	record["namespace"] = namespace
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := os.OpenFile(filepath.Join(parent, "mount-intent.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := intent.Write(raw)
	if err := errors.Join(writeErr, intent.Sync(), intent.Close()); err != nil {
		t.Fatal(err)
	}
	anchor, err := os.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(anchor.Sync(), anchor.Close()); err != nil {
		t.Fatal(err)
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
		if err := d.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("accepted nested bind mount")
	}
	if !errors.Is(err, os.ErrPermission) || errors.Is(err, ErrUnsafeComponent) {
		t.Fatalf("wrong mount rejection: %v", err)
	}
}

func TestSecurityCreationACL(t *testing.T) {
	parent := securityTrustedParent(t)
	for index, name := range []string{"system.posix_acl_access", "system.posix_acl_default"} {
		path := filepath.Join(parent, fmt.Sprintf("acl-attack-%d", index))
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
		positive, err := root.OpenDir(context.Background(), "before", RootPolicy{Mode: 0700, AllowCreate: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := positive.Close(); err != nil {
			t.Fatal(err)
		}
		// An access ACL also writes the POSIX group/other mode bits. Preserve
		// 0700 so the negative control exercises the attached-ACL rejection,
		// and removing the fixture restores the retained root's authority.
		acl := posixACLFixture(5, 0)
		binary.LittleEndian.PutUint16(acl[38:], 0) // ACL_OTHER permissions.
		if err := unix.Fsetxattr(fd, name, acl, 0); err != nil {
			t.Fatal(err)
		}
		var aclStat unix.Stat_t
		if err := unix.Fstat(fd, &aclStat); err != nil || aclStat.Mode&07777 != 0700 {
			t.Fatalf("ACL fixture changed directory mode: %04o %v", aclStat.Mode&07777, err)
		}
		bad, err := root.OpenDir(context.Background(), "denied", RootPolicy{Mode: 0700, AllowCreate: true})
		if bad != nil {
			_ = bad.Close()
			t.Fatal("creation under native ACL accepted")
		}
		if !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(path, "denied")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("denied creation modified tree")
		}
		if err := unix.Fremovexattr(fd, name); err != nil {
			t.Fatal(err)
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
