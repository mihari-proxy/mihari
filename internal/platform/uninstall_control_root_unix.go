//go:build !windows

package platform

// UninstallControlRoot returns no separate control root on Unix because the
// fixed install-control directory is an entry of the selected system base root.
func UninstallControlRoot() (string, error) { return "", nil }
