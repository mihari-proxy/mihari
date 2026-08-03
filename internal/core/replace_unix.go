//go:build !windows

package core

import "os"

func replaceBinary(candidate, target string) error {
	return os.Rename(candidate, target)
}
