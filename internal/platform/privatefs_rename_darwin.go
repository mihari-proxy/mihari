//go:build darwin

package platform

import "golang.org/x/sys/unix"

func renameatNoReplace(dirfd int, oldName, newName string) error {
	return unix.RenameatxNp(dirfd, oldName, dirfd, newName, unix.RENAME_EXCL)
}

func renameatBetweenNoReplace(oldFD int, oldName string, newFD int, newName string) error {
	return unix.RenameatxNp(oldFD, oldName, newFD, newName, unix.RENAME_EXCL)
}
