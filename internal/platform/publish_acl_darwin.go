//go:build darwin

package platform

import (
	"encoding/binary"
	"fmt"

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
	attrs := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
	var buf [4096]byte
	if err := darwinFDAttributes(fd, &attrs, buf[:], 0); err != nil {
		return false, fmt.Errorf("query publish access ACL: %w", err)
	}
	length := binary.NativeEndian.Uint32(buf[:4])
	if length < 12 || length > uint32(len(buf)) {
		return false, fmt.Errorf("query publish access ACL: invalid attribute buffer")
	}
	return binary.NativeEndian.Uint32(buf[8:12]) == 0, nil
}
