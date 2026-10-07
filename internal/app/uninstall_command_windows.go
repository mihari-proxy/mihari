//go:build windows

package app

import (
	"os"
	"path/filepath"
)

func defaultUninstallCommandDirectory() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "mihari")
}

// commandRemoveFailureIsFatal is false on Windows because a running image
// cannot be unlinked. Tests replace it.
var commandRemoveFailureIsFatal = false
