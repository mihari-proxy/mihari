package platform

import (
	"errors"
	"io"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

func (nativeTrustedBackend) osAlias(int, string) (string, error) { return "", ErrUnsafeComponent }

func (nativeTrustedBackend) checkFS(fd int) error {
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil {
		return err
	}
	switch fs.Type {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC, unix.TMPFS_MAGIC, unix.RAMFS_MAGIC:
		return nil
	}
	return os.ErrPermission
}
func linuxACL(fd int, name string) ([]byte, error) {
	b := make([]byte, 65536)
	n, err := unix.Fgetxattr(fd, name, b)
	if errors.Is(err, unix.ENODATA) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return b[:n], nil
}
func clearTrustedDirectoryACL(fd int) error {
	for _, name := range []string{"system.posix_acl_access", "system.posix_acl_default"} {
		if err := unix.Fremovexattr(fd, name); err != nil && !errors.Is(err, unix.ENODATA) {
			return err
		}
		b, err := linuxACL(fd, name)
		if err != nil {
			return err
		}
		if b != nil {
			return os.ErrPermission
		}
	}
	return nil
}
func (nativeTrustedBackend) checkACL(fd int, strict bool, owner uint32) error {
	b, err := linuxACL(fd, "system.posix_acl_access")
	if err != nil {
		return err
	}
	if b != nil {
		if strict {
			return os.ErrPermission
		}
		if err := trustedPOSIXACL(b, owner); err != nil {
			return err
		}
	}
	if strict {
		b, err = linuxACL(fd, "system.posix_acl_default")
		if err != nil {
			return err
		}
		if b != nil {
			return os.ErrPermission
		}
	}
	return nil
}
func (nativeTrustedBackend) openDir(fd int, name string, below bool) (int, error) {
	if !below {
		child, err := unix.Openat(fd, name, trustedDirFlags, 0)
		return child, componentOpenError(err)
	}
	return linuxOpenBeneath(fd, name, linuxMountOps{unix.Openat2, unix.Openat, linuxMountID, unix.Close})
}

type linuxMountOps struct {
	open2   func(int, string, *unix.OpenHow) (int, error)
	open    func(int, string, int, uint32) (int, error)
	mountID func(int) (uint64, error)
	close   func(int) error
}

func linuxOpenBeneath(fd int, name string, ops linuxMountOps) (int, error) {
	return linuxOpenAt(fd, name, trustedDirFlags, 0, ops)
}
func (nativeTrustedBackend) openFile(fd int, name string, flags int, mode uint32) (int, error) {
	return linuxOpenAt(fd, name, flags, mode, linuxMountOps{unix.Openat2, unix.Openat, linuxMountID, unix.Close})
}
func linuxOpenAt(fd int, name string, flags int, mode uint32, ops linuxMountOps) (int, error) {
	child, err := ops.open2(fd, name, &unix.OpenHow{Flags: uint64(flags), Mode: uint64(mode), Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_XDEV})
	if !errors.Is(err, unix.ENOSYS) {
		if errors.Is(err, unix.EXDEV) || errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINVAL) {
			return -1, denied("open beneath anchor", err)
		}
		return child, componentOpenError(err)
	}
	parentID, err := ops.mountID(fd)
	if err != nil || parentID == 0 {
		return -1, denied("parent mount identity unavailable", err)
	}
	child, err = ops.open(fd, name, flags, mode)
	if err != nil {
		return -1, componentOpenError(err)
	}
	childID, err := ops.mountID(child)
	if err != nil || childID != parentID {
		return -1, errors.Join(denied("nested mount or missing mount identity", err), ops.close(child))
	}
	return child, nil
}
func linuxMountID(fd int) (uint64, error) { return linuxMountIDWith(fd, unix.Statx, linuxProcMountID) }
func linuxMountIDWith(fd int, statx func(int, string, int, int, *unix.Statx_t) error, fallback func(int) (uint64, error)) (uint64, error) {
	var st unix.Statx_t
	if err := statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &st); err == nil && st.Mask&unix.STATX_MNT_ID != 0 && st.Mnt_id != 0 {
		return st.Mnt_id, nil
	}
	return fallback(fd)
}

// procfs is a read-only metadata exception, not an application FS allowance.
// Numeric pid avoids following /proc/self; every opened component stays on the
// same root-owned procfs superblock and is opened without following symlinks.
func linuxProcMountID(target int) (_ uint64, err error) {
	fd, err := unix.Open("/proc", trustedDirFlags, 0)
	if err != nil {
		return 0, err
	}
	fds := []int{fd}
	defer func() {
		for i := len(fds) - 1; i >= 0; i-- {
			err = errors.Join(err, unix.Close(fds[i]))
		}
	}()
	var rootFS unix.Statfs_t
	var rootST unix.Stat_t
	if err = unix.Fstatfs(fd, &rootFS); err != nil {
		return 0, err
	}
	if err = unix.Fstat(fd, &rootST); err != nil {
		return 0, err
	}
	if rootFS.Type != unix.PROC_SUPER_MAGIC || rootST.Uid != 0 || rootST.Mode&0022 != 0 {
		return 0, os.ErrPermission
	}
	for _, name := range []string{strconv.Itoa(os.Getpid()), "fdinfo", strconv.Itoa(target)} {
		flags := trustedDirFlags
		if name == strconv.Itoa(target) && len(fds) == 3 {
			flags = unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		}
		fd, err = unix.Openat(fd, name, flags, 0)
		if err != nil {
			return 0, err
		}
		fds = append(fds, fd)
		var fs unix.Statfs_t
		var st unix.Stat_t
		if err = unix.Fstatfs(fd, &fs); err != nil {
			return 0, err
		}
		if err = unix.Fstat(fd, &st); err != nil {
			return 0, err
		}
		if fs.Type != unix.PROC_SUPER_MAGIC || fs.Fsid != rootFS.Fsid || st.Dev != rootST.Dev || st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 {
			return 0, os.ErrPermission
		}
		if len(fds) == 4 && (st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1) {
			return 0, os.ErrPermission
		}
	}
	// os.File owns just the final descriptor; earlier descriptors remain above.
	f := os.NewFile(uintptr(fd), "trusted fdinfo")
	fds = fds[:len(fds)-1]
	defer func() { err = errors.Join(err, f.Close()) }()
	b, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil {
		return 0, err
	}
	return parseTrustedMountID(b)
}
