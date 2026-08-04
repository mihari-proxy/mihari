//go:build !windows

package update

import "os"

func replaceBinary(candidate, target string) error {
	return os.Rename(candidate, target)
}
