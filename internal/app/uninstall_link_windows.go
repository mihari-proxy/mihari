package app

import (
	"os"
	"syscall"
)

func uninstallRootLinkReason(info os.FileInfo) string {
	if info.Mode()&os.ModeSymlink != 0 {
		return "symbolic link"
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if ok && data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "reparse point"
	}
	return ""
}
