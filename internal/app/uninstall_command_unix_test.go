//go:build !windows

package app

import "testing"

func TestCommandRemoveFailureIsFatalOnUnix(t *testing.T) {
	if !commandRemoveFailureIsFatal {
		t.Fatal("matching command removal failure must stop uninstall on unix")
	}
}
