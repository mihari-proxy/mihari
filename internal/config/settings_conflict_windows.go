package config

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isSettingsConflict(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
