//go:build windows

package platform

import (
	"golang.org/x/sys/windows"
	"testing"
)

func TestWindowsRuntimeSecurity_RequiresTrustedOwnerAndExactProtectedGrants(t *testing.T) {
	for _, tc := range []struct {
		name, sddl string
		want       bool
	}{
		{"administrators", "O:BAD:P(A;;GA;;;SY)(A;;GA;;;BA)", true},
		{"system", "O:SYD:P(A;;GA;;;SY)(A;;GA;;;BA)", true},
		{"untrusted owner", "O:BUD:P(A;;GA;;;SY)(A;;GA;;;BA)", false},
		{"missing owner", "D:P(A;;GA;;;SY)(A;;GA;;;BA)", false},
		{"inherited policy", "O:BAD:(A;;GA;;;SY)(A;;GA;;;BA)", false},
		{"extra grant", "O:BAD:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GR;;;BU)", false},
		{"insufficient system", "O:BAD:P(A;;GR;;;SY)(A;;GA;;;BA)", false},
		{"duplicate admins", "O:BAD:P(A;;GA;;;BA)(A;;GA;;;BA)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(tc.sddl)
			if err != nil {
				t.Fatal(err)
			}
			got, err := windowsRuntimeProtectedPolicy(sd, 0x1f003f)
			if err != nil || got != tc.want {
				t.Fatalf("protected=%v err=%v", got, err)
			}
		})
	}
}
