//go:build windows

package app

import "testing"

func TestCommandRemoveFailureIsNotFatalOnWindows(t *testing.T) {
	if commandRemoveFailureIsFatal {
		t.Fatal("Windows may leave a running command file and continue uninstall")
	}
}
