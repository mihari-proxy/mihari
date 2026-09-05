//go:build linux || darwin

package platform

import "testing"

func assertTestClose(t *testing.T, close func() error) {
	t.Helper()
	if err := close(); err != nil {
		t.Errorf("close test resource: %v", err)
	}
}
