package platform

import "os"

// OpenSnapshot opens a no-follow read handle whose identity must match expected.
func OpenSnapshot(fs *PrivateFS, path string, expected FileIdentity) (*os.File, error) {
	return fs.OpenReadChecked(path, expected)
}
