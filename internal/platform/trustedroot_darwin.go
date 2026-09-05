package platform

import (
	"errors"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

func (nativeTrustedBackend) osAlias(fd int, name string) (string, error) {
	var buf [64]byte
	n, err := unix.Readlinkat(fd, name, buf[:])
	if err != nil {
		return "", err
	}
	target := string(buf[:n])
	if !trustedDarwinAlias(name, target) {
		return "", ErrUnsafeComponent
	}
	return target, nil
}

func (nativeTrustedBackend) checkFS(fd int) error {
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil {
		return err
	}
	name := unix.ByteSliceToString(fs.Fstypename[:])
	if (name != "apfs" && name != "hfs") || fs.Flags&unix.MNT_LOCAL == 0 || fs.Flags&unix.MNT_IGNORE_OWNERSHIP != 0 {
		return os.ErrPermission
	}
	return nil
}
func (nativeTrustedBackend) checkACL(fd int, strict bool, _ uint32) error {
	attrs := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var buf [4096]byte
	// XNU fgetattrlist ABI, FSOPT_REPORT_FULLSIZE prevents accepting truncation.
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, uintptr(fd), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 4, 0)
	runtime.KeepAlive(&attrs)
	runtime.KeepAlive(&buf)
	if errno != 0 {
		return errno
	}
	return trustedDarwinACL(buf[:], strict)
}
func clearTrustedDirectoryACL(fd int) error {
	// KAUTH_UID_NONE/GID_NONE preserve ownership. xsecurity=1 removes ACL;
	// xsecurity=0 would leave it unchanged. mode=-1 preserves mode.
	const none = uint32(0xffffffff - 100)
	_, _, errno := unix.Syscall6(unix.SYS_FCHMOD_EXTENDED, uintptr(fd), uintptr(none), uintptr(none), ^uintptr(0), 1, 0)
	if errno != 0 {
		return errno
	}
	return (nativeTrustedBackend{}).checkACL(fd, true, 0)
}
func (b nativeTrustedBackend) openDir(fd int, name string, below bool) (int, error) {
	if !below {
		child, err := unix.Openat(fd, name, trustedDirFlags, 0)
		return child, componentOpenError(err)
	}
	return b.openFile(fd, name, trustedDirFlags, 0)
}
func (b nativeTrustedBackend) openFile(fd int, name string, flags int, mode uint32) (int, error) {
	var parentST unix.Stat_t
	var parentFS unix.Statfs_t
	if err := unix.Fstat(fd, &parentST); err != nil {
		return -1, err
	}
	if err := unix.Fstatfs(fd, &parentFS); err != nil {
		return -1, err
	}
	if err := b.checkFS(fd); err != nil {
		return -1, err
	}
	child, err := unix.Openat(fd, name, flags, mode)
	if err != nil {
		return -1, componentOpenError(err)
	}
	var st unix.Stat_t
	var fs unix.Statfs_t
	err = unix.Fstat(child, &st)
	if err == nil {
		err = unix.Fstatfs(child, &fs)
	}
	if err == nil {
		err = b.checkFS(child)
	}
	if err == nil && (st.Dev != parentST.Dev || fs.Fsid != parentFS.Fsid) {
		err = os.ErrPermission
	}
	if err != nil {
		return -1, errors.Join(err, unix.Close(child))
	}
	return child, nil
}
