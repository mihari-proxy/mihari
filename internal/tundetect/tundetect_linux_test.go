//go:build linux

package tundetect

import "testing"

func TestLinuxProcessPathStripsDeletedSuffix(t *testing.T) {
	if got := trimDeletedProcessPath("/opt/mihomo (deleted)"); got != "/opt/mihomo" {
		t.Fatalf("got=%q", got)
	}
}
