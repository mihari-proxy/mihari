package app

import (
	"path/filepath"
	"strings"
)

func installationPathKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}
