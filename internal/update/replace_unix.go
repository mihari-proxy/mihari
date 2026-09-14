//go:build !windows

package update

import "os"

func replaceBinary(candidate, target string) (error, error) {
	return nil, os.Rename(candidate, target)
}
