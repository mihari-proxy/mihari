//go:build !windows

package app

func defaultUninstallCommandDirectory() string {
	return "/usr/local/bin"
}

// commandRemoveFailureIsFatal is true on Unix: a matching PATH command must
// not remain after complete uninstall. Tests replace it.
var commandRemoveFailureIsFatal = true
