//go:build !windows

package config

import (
	"errors"
	"os"
	"testing"
)

func TestIsSettingsConflictUnix(t *testing.T) {
	for _, err := range []error{errors.New("ordinary error"), os.ErrPermission, os.ErrExist} {
		if isSettingsConflict(err) {
			t.Fatalf("isSettingsConflict(%v)=true, want false", err)
		}
	}
}
