//go:build darwin

package platform

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

func unixACLHasNoAdditionalAuthority(fd int) (bool, error) {
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil {
		return false, fmt.Errorf("query publish filesystem permissions: %w", err)
	}
	name := unix.ByteSliceToString(fs.Fstypename[:])
	if (name != "apfs" && name != "hfs") || fs.Flags&unix.MNT_LOCAL == 0 || fs.Flags&unix.MNT_IGNORE_OWNERSHIP != 0 {
		return false, nil
	}
	// fgetattrlist is handle-relative and available without CGO. Request only
	// extended security: uint32 length followed by attrreference_t. XNU returns
	// an empty reference when no ACL is attached. Any nonempty ACL (including
	// DELETE/DELETE_CHILD grants) remains conservatively unproved.
	// ABI: xnu/bsd/sys/attr.h and xnu/bsd/vfs/vfs_attrlist.c.
	attrs := struct {
		Count                           uint16
		Reserved                        uint16
		Common, Volume, Dir, File, Fork uint32
	}{Count: 5, Common: unix.ATTR_CMN_EXTENDED_SECURITY}
	var buf [4096]byte
	_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, uintptr(fd), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0)
	runtime.KeepAlive(&attrs)
	runtime.KeepAlive(&buf)
	if errno != 0 {
		return false, fmt.Errorf("query publish access ACL: %w", errno)
	}
	length := binary.NativeEndian.Uint32(buf[:4])
	if length < 12 || length > uint32(len(buf)) {
		return false, fmt.Errorf("query publish access ACL: invalid attribute buffer")
	}
	return binary.NativeEndian.Uint32(buf[8:12]) == 0, nil
}
