//go:build linux

package platform

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// Only local filesystems with Linux POSIX ACL semantics are proved here.
// Network, FUSE and other extended authorization models fail closed.
func unixACLHasNoAdditionalAuthority(fd int) (bool, error) {
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil {
		return false, fmt.Errorf("query publish filesystem permissions: %w", err)
	}
	switch fs.Type {
	case unix.EXT4_SUPER_MAGIC, unix.TMPFS_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC, unix.RAMFS_MAGIC:
	default:
		return false, nil
	}
	// An absent access ACL proves mode/sticky permissions are authoritative.
	// Any ACL is conservatively rejected, even when it might only restrict access.
	// Default ACLs affect creation, not authority over this directory entry.
	_, err := unix.Fgetxattr(fd, "system.posix_acl_access", nil)
	if errors.Is(err, unix.ENODATA) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("query publish access ACL: %w", err)
	}
	return false, nil
}
