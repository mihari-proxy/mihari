//go:build linux || windows

package platform

import (
	"net"
	"testing"
)

func TestListenIPMatches_UnspecifiedQuery(t *testing.T) {
	for _, tc := range []struct {
		local, query string
		want         bool
	}{
		{"127.0.0.1", "0.0.0.0", true},
		{"192.0.2.1", "0.0.0.0", true},
		{"::1", "::", true},
		{"::1", "0.0.0.0", false},
		{"127.0.0.1", "::", true},
		{"::", "0.0.0.0", true},
		{"0.0.0.0", "127.0.0.1", true},
		{"::", "127.0.0.1", true},
		{"127.0.0.1", "192.0.2.1", false},
	} {
		t.Run(tc.local+"/"+tc.query, func(t *testing.T) {
			if got := listenIPMatches(net.ParseIP(tc.local), net.ParseIP(tc.query)); got != tc.want {
				t.Fatalf("match=%v want=%v", got, tc.want)
			}
		})
	}
}
