//go:build windows

package platform

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// UninstallControlRoot resolves the fixed machine control directory without
// creating it or enumerating its ProgramData parent.
func UninstallControlRoot() (string, error) {
	programData, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return "", err
	}
	return filepath.Join(programData, "Mihari", "install-control"), nil
}
