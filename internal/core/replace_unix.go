//go:build !windows

package core

import "os"

func replaceBinary(candidate, target string) (error, error) {
	return nil, os.Rename(candidate, target)
}
