package platform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDiscoveryLinux_ProcDirectoryProcessChurn(t *testing.T) {
	fd, err := unix.Open("/proc", trustedDirFlags, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer assertDiscoveryClose(t, func() error { return unix.Close(fd) })
	var before unix.Stat_t
	var fs unix.Statfs_t
	if err := unix.Fstat(fd, &before); err != nil {
		t.Fatal(err)
	}
	if err := unix.Fstatfs(fd, &fs); err != nil {
		t.Fatal(err)
	}
	mountID, err := linuxMountID(fd)
	if err != nil {
		t.Fatal(err)
	}
	if fs.Type != unix.PROC_SUPER_MAGIC || mountID == 0 {
		t.Fatal("fixture is not an identified procfs mount")
	}

	// Other processes can exit concurrently. Keep up to eight owned children
	// alive until a changed count is observed; never depend on an exact delta.
	var after unix.Stat_t
	for range 8 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		child := exec.CommandContext(ctx, "/bin/cat")
		input, err := child.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := child.Start(); err != nil {
			assertDiscoveryClose(t, input.Close)
			t.Fatal(err)
		}
		t.Cleanup(func() {
			assertDiscoveryClose(t, input.Close)
			if err := child.Wait(); err != nil {
				t.Errorf("reap proc fixture child: %v", err)
			}
		})
		if err := unix.Fstat(fd, &after); err != nil {
			t.Fatal(err)
		}
		if after.Nlink != before.Nlink {
			break
		}
	}
	if after.Nlink == before.Nlink {
		t.Fatal("owned process creation did not expose a changed proc directory link count")
	}
	t.Logf("held proc directory links changed from %d to %d", before.Nlink, after.Nlink)
	if err := checkDiscoveryProcDirectory(after, before, fs, mountID, mountID); err != nil {
		t.Fatalf("same proc directory rejected after process creation: %v", err)
	}
}

func TestDiscoveryLinux_ProcDirectoryProof(t *testing.T) {
	original := unix.Stat_t{Dev: 59, Ino: 1, Mode: unix.S_IFDIR | 0555, Uid: 0, Gid: 0, Nlink: 300}
	for _, tc := range []struct {
		name   string
		change func(*unix.Stat_t, *unix.Stat_t, *unix.Statfs_t, *uint64)
		want   error
	}{
		{"unchanged", func(*unix.Stat_t, *unix.Stat_t, *unix.Statfs_t, *uint64) {}, nil},
		{"process added", func(s, _ *unix.Stat_t, _ *unix.Statfs_t, _ *uint64) { s.Nlink++ }, nil},
		{"process reaped", func(s, _ *unix.Stat_t, _ *unix.Statfs_t, _ *uint64) { s.Nlink-- }, nil},
		{"device replaced", func(s, _ *unix.Stat_t, _ *unix.Statfs_t, _ *uint64) { s.Dev++ }, ErrIdentityMismatch},
		{"inode replaced", func(s, _ *unix.Stat_t, _ *unix.Statfs_t, _ *uint64) { s.Ino++ }, ErrIdentityMismatch},
		{"owner changed", func(s, _ *unix.Stat_t, _ *unix.Statfs_t, _ *uint64) { s.Uid++ }, ErrIdentityMismatch},
		{"group changed", func(s, _ *unix.Stat_t, _ *unix.Statfs_t, _ *uint64) { s.Gid++ }, ErrIdentityMismatch},
		{"mode changed", func(s, _ *unix.Stat_t, _ *unix.Statfs_t, _ *uint64) { s.Mode |= 0020 }, ErrIdentityMismatch},
		{"type changed", func(s, _ *unix.Stat_t, _ *unix.Statfs_t, _ *uint64) { s.Mode = unix.S_IFREG | 0555 }, ErrIdentityMismatch},
		{"unlinked now", func(s, _ *unix.Stat_t, _ *unix.Statfs_t, _ *uint64) { s.Nlink = 0 }, ErrIdentityMismatch},
		{"unlinked originally", func(_, o *unix.Stat_t, _ *unix.Statfs_t, _ *uint64) { o.Nlink = 0 }, ErrIdentityMismatch},
		{"both unlinked", func(s, o *unix.Stat_t, _ *unix.Statfs_t, _ *uint64) { s.Nlink, o.Nlink = 0, 0 }, ErrIdentityMismatch},
		{"both regular", func(s, o *unix.Stat_t, _ *unix.Statfs_t, _ *uint64) {
			s.Mode, o.Mode = unix.S_IFREG|0555, unix.S_IFREG|0555
		}, ErrIdentityMismatch},
		{"foreign filesystem", func(_, _ *unix.Stat_t, fs *unix.Statfs_t, _ *uint64) { fs.Type = unix.TMPFS_MAGIC }, os.ErrPermission},
		{"mount replaced", func(_, _ *unix.Stat_t, _ *unix.Statfs_t, id *uint64) { *id = 43 }, ErrIdentityMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, old := original, original
			fs := unix.Statfs_t{Type: unix.PROC_SUPER_MAGIC}
			mountID := uint64(42)
			tc.change(&st, &old, &fs, &mountID)
			if err := checkDiscoveryProcDirectory(st, old, fs, mountID, 42); !errors.Is(err, tc.want) {
				t.Fatalf("proc directory proof = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDiscoveryLinux_TargetIdentityStillRequiresLinkCount(t *testing.T) {
	for _, kind := range []uint32{unix.S_IFREG, unix.S_IFDIR} {
		original := unix.Stat_t{Dev: 10, Ino: 42, Mode: kind | 0600, Uid: 1000, Gid: 1000, Nlink: 1}
		if !sameDiscoveryStat(original, original) {
			t.Fatal("unchanged target identity rejected")
		}
		changed := original
		for changed.Nlink = 0; changed.Nlink <= 2; changed.Nlink += 2 {
			if sameDiscoveryStat(changed, original) {
				t.Fatalf("target type %o accepted changed link count %d", kind, changed.Nlink)
			}
		}
	}
}

func TestDiscoveryLinux_CredentialRejectsHardLinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credential")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(dir, trustedDirFlags, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer assertDiscoveryClose(t, func() error { return unix.Close(fd) })
	b := nativeDiscoveryBackend{}
	parent := discoveryRef{fd: fd}
	ref, err := b.child(parent, "credential")
	if err != nil {
		t.Fatal(err)
	}
	defer assertDiscoveryClose(t, func() error { return b.close(ref) })
	m, err := b.inspect(ref, true, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	if m.node.links != 2 {
		t.Fatal("fixture did not create a second hard link")
	}
	if data, err := b.read(parent, "credential", m); data != nil || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("hard-linked credential not refused: %v", err)
	}
}

func TestDiscoveryLinux_FDReferenceProof(t *testing.T) {
	original := unix.Stat_t{Dev: 59, Ino: 42, Mode: unix.S_IFLNK | 0700, Uid: 1000, Nlink: 1}
	if !validDiscoveryFDReference(original, 59, 1000) {
		t.Fatal("kernel fd reference rejected")
	}
	for _, tc := range []struct {
		name   string
		change func(*unix.Stat_t)
	}{
		{"no link", func(s *unix.Stat_t) { s.Nlink = 0 }},
		{"multiple links", func(s *unix.Stat_t) { s.Nlink = 2 }},
		{"foreign device", func(s *unix.Stat_t) { s.Dev++ }},
		{"foreign owner", func(s *unix.Stat_t) { s.Uid++ }},
		{"regular file", func(s *unix.Stat_t) { s.Mode = unix.S_IFREG | 0700 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := original
			tc.change(&st)
			if validDiscoveryFDReference(st, 59, 1000) {
				t.Fatal("unsafe kernel fd reference accepted")
			}
		})
	}
}
