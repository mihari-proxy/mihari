package config

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestIsSettingsConflictWindows(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "sharing violation", err: windows.ERROR_SHARING_VIOLATION, want: true},
		{name: "lock violation", err: windows.ERROR_LOCK_VIOLATION, want: true},
		{name: "permission", err: os.ErrPermission, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isSettingsConflict(test.err); got != test.want {
				t.Fatalf("isSettingsConflict(%v)=%v, want %v", test.err, got, test.want)
			}
		})
	}
}
