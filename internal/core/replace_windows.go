//go:build windows

package core

import (
	"errors"
	"fmt"
	"os"
	"time"
)

func replaceBinary(candidate, target string) error {
	stash := fmt.Sprintf("%s.old-%d", target, time.Now().UnixNano())
	stashed := false
	if err := os.Rename(target, stash); err == nil {
		stashed = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(candidate, target); err != nil {
		if stashed {
			_ = os.Rename(stash, target)
		}
		return err
	}
	if stashed {
		_ = os.Remove(stash)
	}
	return nil
}
