//go:build linux

package tundetect

import (
	"context"
	"errors"
	"testing"
)

func TestLinuxProcessPathStripsDeletedSuffix(t *testing.T) {
	if got := trimDeletedProcessPath("/opt/mihomo (deleted)"); got != "/opt/mihomo" {
		t.Fatalf("got=%q", got)
	}
}

func TestLinuxEnumeration_Canceled(t *testing.T) {
	for _, name := range []string{"tun", "mihomo"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			var err error
			if name == "tun" {
				_, err = enumerateLinuxTun(ctx)
			} else {
				_, err = enumerateLinuxMihomo(ctx)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
