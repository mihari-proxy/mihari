package platform

import (
	"golang.org/x/sys/unix"
	"runtime"
	"strings"
	"unsafe"
)

// macOS12: retain the readable root and validate every protected relative
// prefix before it can be used by a later lookup. No O_SEARCH assumption.
type nativeDiscoveryBackend struct{}

func (b nativeDiscoveryBackend) directory(p discoveryRef, name string) (discoveryRef, error) {
	return b.child(p, name)
}

func (nativeDiscoveryBackend) root() (discoveryRef, error) {
	fd, err := unix.Open("/", trustedDirFlags, 0)
	return discoveryRef{fd: fd, tail: ".", owned: true}, err
}
func (nativeDiscoveryBackend) child(p discoveryRef, name string) (discoveryRef, error) {
	return discoveryRef{fd: p.fd, tail: discoveryTail(p, name)}, nil
}
func discoveryTail(p discoveryRef, name string) string {
	if p.tail == "." {
		return name
	}
	return p.tail + "/" + name
}
func (nativeDiscoveryBackend) name(p discoveryRef, name string) (trustedNode, error) {
	return (nativeTrustedBackend{}).statAt(p.fd, discoveryTail(p, name))
}
func (nativeDiscoveryBackend) alias(p discoveryRef, name string) (string, error) {
	if p.tail != "." {
		return "", ErrUnsafeComponent
	}
	return (nativeTrustedBackend{}).osAlias(p.fd, name)
}
func (nativeDiscoveryBackend) close(r discoveryRef) error { return unix.Close(r.fd) }
func (b nativeDiscoveryBackend) inspect(r discoveryRef, strict bool, owner uint32) (discoveryMetadata, error) {
	var st unix.Stat_t
	if err := unix.Fstatat(r.fd, r.tail, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return discoveryMetadata{}, err
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return discoveryMetadata{}, ErrUnsafeComponent
	}
	attrs, err := getDiscoveryDarwinAttrs(r.fd, r.tail, strict)
	if err != nil {
		return discoveryMetadata{}, denied("discovery attributes", err)
	}
	if attrs.dev != uint32(st.Dev) || attrs.ino != st.Ino || attrs.uid != st.Uid || attrs.gid != st.Gid || attrs.mode != uint32(st.Mode) || !discoveryDarwinType(attrs.objtype, uint32(st.Mode)) {
		return discoveryMetadata{}, ErrIdentityMismatch
	}
	mount, err := discoveryDarwinMount(r.fd, attrs.fsid)
	if err != nil {
		return discoveryMetadata{}, err
	}
	n, err := (nativeTrustedBackend{}).statAt(r.fd, r.tail)
	if err != nil {
		return discoveryMetadata{}, err
	}
	if n != trustedNodeFromStat(st) {
		return discoveryMetadata{}, ErrIdentityMismatch
	}
	return discoveryMetadata{node: n, mount: mount, size: st.Size}, nil
}
func discoveryDarwinType(kind, mode uint32) bool {
	want := map[uint32]uint32{unix.S_IFREG: 1, unix.S_IFDIR: 2, unix.S_IFSOCK: 6}
	return want[mode&unix.S_IFMT] != 0 && want[mode&unix.S_IFMT] == kind
}
func getDiscoveryDarwinAttrs(fd int, path string, strict bool) (discoveryDarwinAttrs, error) {
	return loadDiscoveryDarwinAttrs(strict, func(absence bool) ([]byte, error) { return queryDiscoveryDarwinAttrs(fd, path, absence) })
}
func queryDiscoveryDarwinAttrs(fd int, path string, absence bool) ([]byte, error) {
	name, err := unix.BytePtrFromString(path)
	if err != nil {
		return nil, err
	}
	attrs := unix.Attrlist{Bitmapcount: 5, Commonattr: discoveryDarwinCommon, Forkattr: discoveryDarwinRealFSID}
	buf := make([]byte, 4096)
	options := unix.FSOPT_NOFOLLOW | unix.FSOPT_REPORT_FULLSIZE | unix.FSOPT_ATTR_CMN_EXTENDED | unix.FSOPT_PACK_INVAL_ATTRS
	if absence {
		attrs.Commonattr &^= unix.ATTR_CMN_RETURNED_ATTRS
		attrs.Forkattr = 0
		options = unix.FSOPT_NOFOLLOW | unix.FSOPT_REPORT_FULLSIZE
	}
	//nolint:staticcheck // SA1019: x/sys v0.46.0 has no Getattrlistat wrapper; this reviewed macOS12 ABI requires native discovery test execution.
	_, _, errno := unix.Syscall6(unix.SYS_GETATTRLISTAT, uintptr(fd), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(options))
	runtime.KeepAlive(name)
	runtime.KeepAlive(attrs)
	runtime.KeepAlive(buf)
	if errno != 0 {
		return nil, errno
	}
	return buf, nil
}
func discoveryDarwinMount(fd int, fsid [2]int32) (discoveryMount, error) {
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil {
		return discoveryMount{}, err
	}
	if fs.Fsid.Val == fsid {
		return checkedDarwinMount(fs)
	}
	m, err := completeDiscoveryMount(fsid, func(capacity int) ([]discoveryMount, int, error) {
		buf := make([]unix.Statfs_t, capacity)
		n, err := unix.Getfsstat(buf, unix.MNT_NOWAIT)
		if err != nil {
			return nil, n, err
		}
		entries := make([]discoveryMount, len(buf))
		for i, f := range buf {
			entries[i] = darwinMountRecord(f)
		}
		return entries, n, nil
	})
	if err != nil {
		return discoveryMount{}, denied("discovery mount snapshot", err)
	}
	if err = checkDarwinMountRecord(m); err != nil {
		return discoveryMount{}, err
	}
	return m, nil
}
func darwinMountRecord(fs unix.Statfs_t) discoveryMount {
	return discoveryMount{fsid: fs.Fsid.Val, kind: strings.TrimRight(string(fs.Fstypename[:]), "\x00"), flags: uint64(fs.Flags), owner: fs.Owner}
}
func checkedDarwinMount(fs unix.Statfs_t) (discoveryMount, error) {
	m := darwinMountRecord(fs)
	return m, checkDarwinMountRecord(m)
}
func (b nativeDiscoveryBackend) read(p discoveryRef, name string, m discoveryMetadata) ([]byte, error) {
	fd, err := unix.Openat(p.fd, discoveryTail(p, name), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, componentOpenError(err)
	}
	return readDiscoveryFD(fd, m, func(fd int) (discoveryMetadata, error) {
		var st unix.Stat_t
		var fs unix.Statfs_t
		if err := unix.Fstat(fd, &st); err != nil {
			return discoveryMetadata{}, err
		}
		if err := unix.Fstatfs(fd, &fs); err != nil {
			return discoveryMetadata{}, err
		}
		mount, err := checkedDarwinMount(fs)
		if err != nil {
			return discoveryMetadata{}, err
		}
		if err = (nativeTrustedBackend{}).checkACL(fd, true, m.node.uid); err != nil {
			return discoveryMetadata{}, denied("credential ACL", err)
		}
		n, err := (nativeTrustedBackend{}).stat(fd)
		if err != nil {
			return discoveryMetadata{}, err
		}
		return discoveryMetadata{node: n, mount: mount, size: st.Size}, nil
	})
}
