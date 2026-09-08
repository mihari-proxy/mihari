//go:build !windows

package app

import "path/filepath"

func installationPathKey(path string) string {
	return filepath.Clean(path)
}
