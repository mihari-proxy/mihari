// Package elevate reports process privilege. It never auto-relaunches elevated.
package elevate

import "github.com/mihari-proxy/mihari/internal/control/protocol"

// Checker reports whether the current process already has install privileges.
type Checker func() bool

// IsElevated reports whether the process is Administrator (Windows) or root (Unix).
// Tests may replace Check via SetChecker.
var Check Checker = platformElevated

// SetChecker overrides the elevation probe (tests only).
func SetChecker(checker Checker) {
	if checker == nil {
		Check = platformElevated
		return
	}
	Check = checker
}

// IsElevated returns Check().
func IsElevated() bool {
	if Check == nil {
		return platformElevated()
	}
	return Check()
}

// RequireElevated fails when the process is not already privileged.
// Mihari does not relaunch with UAC or sudo; the user must start an elevated shell.
func RequireElevated() error {
	if IsElevated() {
		return nil
	}
	return protocol.APIError{
		Code:    protocol.CodePermissionDenied,
		Message: "administrator privileges are required; re-run this command from an elevated shell",
	}
}
