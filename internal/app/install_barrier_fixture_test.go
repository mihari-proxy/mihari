package app

import (
	"errors"
	"os"
	"testing"

	"github.com/mihari-proxy/mihari/internal/service"
)

func nativeValidationBarrierMatches(predecessor service.StatusKind, link string, readErr error, authority string) bool {
	if authority != InstallAuthoritySource {
		return false
	}
	if predecessor == service.StatusNotInstalled {
		return errors.Is(readErr, os.ErrNotExist)
	}
	return readErr == nil && link == "/dev/null"
}
func TestNativeValidationBarrier_GreenfieldAndInstalled(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    service.StatusKind
		link      string
		err       error
		authority string
		want      bool
	}{
		{"greenfield", service.StatusNotInstalled, "", os.ErrNotExist, InstallAuthoritySource, true},
		{"installed mask", service.StatusStopped, "/dev/null", nil, InstallAuthoritySource, true},
		{"installed missing", service.StatusStopped, "", os.ErrNotExist, InstallAuthoritySource, false},
		{"greenfield published", service.StatusNotInstalled, "", os.ErrInvalid, InstallAuthoritySource, false},
		{"wrong authority", service.StatusNotInstalled, "", os.ErrNotExist, InstallAuthorityTarget, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nativeValidationBarrierMatches(tc.status, tc.link, tc.err, tc.authority); got != tc.want {
				t.Fatalf("validation barrier=%v want %v", got, tc.want)
			}
		})
	}
}
