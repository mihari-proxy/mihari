package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestTrustedRoot_LinuxDescriptorPrimitives(t *testing.T) {
	parent := t.TempDir()
	fd, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, func() error { return unix.Close(fd) })
	b := nativeTrustedBackend{}
	if err := b.checkFS(fd); err != nil {
		t.Fatalf("positive filesystem: %v", err)
	}
	if err := b.checkACL(fd, true, uint32(os.Geteuid())); err != nil {
		t.Fatalf("positive ACL: %v", err)
	}
	if err := os.Mkdir(filepath.Join(parent, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	child, err := b.openDir(fd, "directory", true)
	if err != nil {
		t.Fatalf("positive nofollow open: %v", err)
	}
	defer assertTestClose(t, func() error { return unix.Close(child) })
	n, err := b.stat(child)
	if err != nil || n.mode&0777 != 0700 {
		t.Fatalf("positive mode: %+v %v", n, err)
	}
	if err := os.Symlink("directory", filepath.Join(parent, "link")); err != nil {
		t.Fatal(err)
	}
	got, err := b.openDir(fd, "link", true)
	if got >= 0 {
		assertTestClose(t, func() error { return unix.Close(got) })
	}
	if !errors.Is(err, ErrUnsafeComponent) {
		t.Fatalf("symlink: %v", err)
	}
}

func TestTrustedRoot_LinuxMountFallback(t *testing.T) {
	for _, kind := range []string{"same mount", "bind mount", "missing identity", "EXDEV", "EINVAL", "ELOOP", "EAGAIN"} {
		t.Run(kind, func(t *testing.T) {
			opened, closed := 0, 0
			ops := linuxMountOps{
				open2: func(int, string, *unix.OpenHow) (int, error) {
					switch kind {
					case "EXDEV":
						return -1, unix.EXDEV
					case "EINVAL":
						return -1, unix.EINVAL
					case "ELOOP":
						return -1, unix.ELOOP
					case "EAGAIN":
						return -1, unix.EAGAIN
					}
					return -1, unix.ENOSYS
				},
				open: func(int, string, int, uint32) (int, error) { opened++; return 2, nil },
				mountID: func(fd int) (uint64, error) {
					if kind == "missing identity" {
						return 0, unix.ENOSYS
					}
					if kind == "bind mount" && fd == 2 {
						return 20, nil
					}
					return 10, nil
				},
				close: func(int) error { closed++; return nil },
			}
			fd, err := linuxOpenBeneath(1, "child", ops)
			if kind == "same mount" {
				if err != nil || fd != 2 {
					t.Fatalf("positive fallback: %d %v", fd, err)
				}
				return
			}
			if err == nil || fd >= 0 {
				t.Fatal("unproved mount accepted")
			}
			if (kind == "EXDEV" || kind == "EINVAL" || kind == "ELOOP" || kind == "EAGAIN") && opened != 0 {
				t.Fatal("security error downgraded")
			}
			if opened != closed {
				t.Fatal("rejected fallback leaked descriptor")
			}
		})
	}
}

func TestTrustedRoot_LinuxNativeMountID(t *testing.T) {
	fd, err := unix.Open(t.TempDir(), trustedDirFlags, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, func() error { return unix.Close(fd) })
	id, err := linuxMountID(fd)
	if err != nil || id == 0 {
		t.Fatalf("native mount identity: %d %v", id, err)
	}
	procID, err := linuxProcMountID(fd)
	if err != nil || procID != id {
		t.Fatalf("trusted procfs fallback: %d %v, want %d", procID, err, id)
	}
	called := false
	got, err := linuxMountIDWith(fd, func(int, string, int, int, *unix.Statx_t) error { return nil }, func(int) (uint64, error) { called = true; return procID, nil })
	if err != nil || got != id || !called {
		t.Fatalf("missing STATX_MNT_ID mask bypassed fallback: %d %v", got, err)
	}
}

type inheritedACLBackend struct {
	nativeTrustedBackend
	observed int
}

func (b *inheritedACLBackend) openFile(parent int, name string, flags int, mode uint32) (int, error) {
	fd, err := b.nativeTrustedBackend.openFile(parent, name, flags, mode)
	if err == nil && flags&unix.O_CREAT != 0 {
		if err = unix.Fsetxattr(fd, "system.posix_acl_access", posixACLFixture(5, 0), 0); err != nil {
			return -1, errors.Join(err, unix.Close(fd))
		}
		b.observed, err = dupCLOEXEC(fd)
		if err != nil {
			return -1, errors.Join(err, unix.Close(fd))
		}
	}
	return fd, err
}
func TestCreationParent_UnexpectedInheritedACLReceivesNoBytes(t *testing.T) {
	r, path := trustedTempCapability(t)
	if err := r.WriteFile(context.Background(), "positive", []byte("ok"), 0600, nil); err != nil {
		t.Fatalf("positive publication: %v", err)
	}
	b := &inheritedACLBackend{observed: -1}
	r.backend = b
	err := r.WriteFile(context.Background(), "secret", []byte("sensitive"), 0600, nil)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unexpected ACL not rejected: %v", err)
	}
	if b.observed < 0 {
		t.Fatal("did not reach newly created inode")
	}
	defer assertTestClose(t, func() error { return unix.Close(b.observed) })
	var st unix.Stat_t
	if err := unix.Fstat(b.observed, &st); err != nil {
		t.Fatal(err)
	}
	if st.Size != 0 {
		t.Fatal("sensitive bytes written before ACL validation")
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 1 {
		t.Fatalf("failed publication left namespace debris: %v %v", entries, err)
	}
}

func TestCreationParent_DefaultACLPreventsAnyCreation(t *testing.T) {
	r, path := trustedTempCapability(t)
	fd := r.chain[0].fd
	if err := r.WriteFile(context.Background(), "positive", []byte("ok"), 0600, nil); err != nil {
		t.Fatalf("positive publication: %v", err)
	}
	if err := unix.Fsetxattr(fd, "system.posix_acl_default", posixACLFixture(5, 5), 0); err != nil {
		t.Fatal(err)
	}
	err := r.WriteFile(context.Background(), "secret", []byte("sensitive"), 0600, nil)
	if !errors.Is(err, os.ErrPermission) || errors.Is(err, ErrUnsafeComponent) {
		t.Fatalf("wrong ACL rejection: %v", err)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ACL parent created objects: %v %v", entries, err)
	}
}

func TestCreationParent_LinuxDefaultAndAccessACL(t *testing.T) {
	parent := t.TempDir()
	fd, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, func() error { return unix.Close(fd) })
	b := nativeTrustedBackend{}
	owner := uint32(os.Geteuid())
	if err := b.checkACL(fd, true, owner); err != nil {
		t.Fatalf("positive control: %v", err)
	}
	for _, name := range []string{"system.posix_acl_access", "system.posix_acl_default"} {
		t.Run(name, func(t *testing.T) {
			if err := unix.Fsetxattr(fd, name, posixACLFixture(5, 5), 0); err != nil {
				t.Fatal(err)
			}
			if err := b.checkACL(fd, false, owner); err != nil {
				t.Fatalf("read-only traversal: %v", err)
			}
			if err := b.checkACL(fd, true, owner); err == nil {
				t.Fatal("creation accepted attached ACL")
			}
			if err := unix.Fremovexattr(fd, name); err != nil {
				t.Fatal(err)
			}
		})
	}
}
