//go:build linux

package platform

import "golang.org/x/sys/unix"

func renameatNoReplace(dirfd int, oldName, newName string) error {
	return unix.Renameat2(dirfd, oldName, dirfd, newName, unix.RENAME_NOREPLACE)
}
