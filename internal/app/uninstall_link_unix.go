//go:build !windows

package app

import "os"

func uninstallRootLinkReason(info os.FileInfo) string {
	if info.Mode()&os.ModeSymlink != 0 {
		return "symbolic link"
	}
	return ""
}
