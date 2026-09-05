package platform

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"strconv"
)

// discoveryFDACL follows only the kernel reference to a still-owned O_PATH fd.
// The caller owns target's lifetime throughout; this never reads a link target
// or reopens an application pathname. See the published discovery ruling.
func discoveryFDACL(target int, strict bool, owner uint32) (err error) {
	var targetST unix.Stat_t
	if err = unix.Fstat(target, &targetST); err != nil {
		return err
	}
	if targetST.Mode&unix.S_IFMT == unix.S_IFLNK {
		return ErrUnsafeComponent
	}
	fd, err := unix.Open("/", trustedDirFlags, 0)
	if err != nil {
		return err
	}
	fds := []int{fd}
	names := []string{"proc", strconv.Itoa(os.Getpid()), "fd"}
	nodes := []unix.Stat_t{}
	defer func() {
		for i := len(fds) - 1; i >= 0; i-- {
			err = errors.Join(err, unix.Close(fds[i]))
		}
	}()
	var mountID uint64
	for i, name := range names {
		fd, err = unix.Openat(fd, name, trustedDirFlags, 0)
		if err != nil {
			return err
		}
		fds = append(fds, fd)
		var st unix.Stat_t
		var fs unix.Statfs_t
		if err = unix.Fstat(fd, &st); err != nil {
			return err
		}
		if err = unix.Fstatfs(fd, &fs); err != nil {
			return err
		}
		uid := uint32(os.Geteuid())
		if i == 0 {
			uid = 0
		}
		if st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Uid != uid || st.Mode&0022 != 0 || fs.Type != unix.PROC_SUPER_MAGIC {
			return os.ErrPermission
		}
		id, e := linuxMountID(fd)
		if e != nil || id == 0 {
			return denied("proc mount identity", e)
		}
		if i == 0 {
			mountID = id
		} else if id != mountID {
			return os.ErrPermission
		}
		nodes = append(nodes, st)
	}
	check := func() error {
		for i, name := range names {
			var st unix.Stat_t
			var fs unix.Statfs_t
			if e := unix.Fstatat(fds[i], name, &st, unix.AT_SYMLINK_NOFOLLOW); e != nil {
				return e
			}
			if !sameDiscoveryStat(st, nodes[i]) {
				return ErrIdentityMismatch
			}
			if e := unix.Fstatfs(fds[i+1], &fs); e != nil {
				return e
			}
			if fs.Type != unix.PROC_SUPER_MAGIC {
				return os.ErrPermission
			}
			id, e := linuxMountID(fds[i+1])
			if e != nil {
				return e
			}
			if id != mountID {
				return ErrIdentityMismatch
			}
		}
		var held, followed unix.Stat_t
		var reference unix.Stat_t
		if e := unix.Fstatat(fd, strconv.Itoa(target), &reference, unix.AT_SYMLINK_NOFOLLOW); e != nil {
			return e
		}
		if reference.Mode&unix.S_IFMT != unix.S_IFLNK || reference.Uid != uint32(os.Geteuid()) || reference.Nlink != 1 || reference.Dev != nodes[len(nodes)-1].Dev {
			return os.ErrPermission
		}
		if e := unix.Fstat(target, &held); e != nil {
			return e
		}
		if e := unix.Fstatat(fd, strconv.Itoa(target), &followed, 0); e != nil {
			return e
		}
		if !sameDiscoveryStat(held, targetST) || !sameDiscoveryStat(followed, targetST) {
			return ErrIdentityMismatch
		}
		return nil
	}
	if err = check(); err != nil {
		return err
	}
	path := "/proc/" + strconv.Itoa(os.Getpid()) + "/fd/" + strconv.Itoa(target)
	for _, selector := range []string{"system.posix_acl_access", "system.posix_acl_default"} {
		if selector == "system.posix_acl_default" && !strict {
			break
		}
		buf := make([]byte, 65536)
		n, e := unix.Getxattr(path, selector, buf)
		if e != nil && !errors.Is(e, unix.ENODATA) {
			return e
		}
		if e == nil {
			if n < 0 || n > len(buf) || strict {
				return os.ErrPermission
			}
			if e = trustedPOSIXACL(buf[:n], owner); e != nil {
				return e
			}
		}
		if err = check(); err != nil {
			return err
		}
	}
	return nil
}

func sameDiscoveryStat(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Uid == b.Uid && a.Gid == b.Gid && a.Nlink == b.Nlink
}
