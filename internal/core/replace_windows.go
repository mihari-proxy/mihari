//go:build windows

package core

import (
	"errors"
	"fmt"
	"os"
	"time"
)

func replaceBinary(candidate, target string) (error, error) {
	return replaceBinaryWithOps(candidate, target, os.Rename)
}

func replaceBinaryWithOps(candidate, target string, rename func(string, string) error) (warning, resultErr error) {
	stash := fmt.Sprintf("%s.old-%d", target, time.Now().UnixNano())
	stashed := false
	if err := rename(target, stash); err == nil {
		stashed = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := rename(candidate, target); err != nil {
		if stashed {
			return nil, errors.Join(err, rename(stash, target))
		}
		return nil, err
	}
	// A later startup retries deletion after old processes have released the image.
	return nil, nil
}
