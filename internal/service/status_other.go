//go:build !windows

package service

import "strings"

// platformQueryStatus is a no-op off Windows; callers use the kardianos path.
func platformQueryStatus(string) (StatusKind, error) {
	return StatusUnknown, nil
}

func isAccessDeniedError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "access is denied") ||
		strings.Contains(lower, "access denied") ||
		strings.Contains(lower, "permission denied")
}
