//go:build linux

package platform

import "golang.org/x/sys/unix"

func renameatNoReplace(dirfd int, oldName, newName string) error {
	return unix.Renameat2(dirfd, oldName, dirfd, newName, unix.RENAME_NOREPLACE)
}

func renameatBetweenNoReplace(oldFD int, oldName string, newFD int, newName string) error {
	return unix.Renameat2(oldFD, oldName, newFD, newName, unix.RENAME_NOREPLACE)
}
