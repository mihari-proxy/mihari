//go:build windows

package core

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

func updateFileIdentity(f *os.File) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return "", err
	}
	if info.NumberOfLinks != 1 {
		return "", os.ErrPermission
	}
	return fmt.Sprintf("%x:%x:%x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}

// File contents are synced before publication. Windows does not support the
// Unix directory-fsync operation (matching the existing config writer).
func syncUpdateDirectory(*os.Root, string) error { return nil }
