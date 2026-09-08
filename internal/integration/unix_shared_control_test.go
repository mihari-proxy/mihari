//go:build linux || darwin

package integration

import (
	"github.com/mihari-proxy/mihari/internal/platform"
	"path/filepath"
	"testing"
)

// Actual two-UID authenticated IPC, D/U denial and v2 export are exercised by
// cmd/mihari TestUnixSecurity_FullAssembly through the T19 validated fixture.
func TestUnixSharedControl_UsesOneRootPeerAcrossUserLayouts(t *testing.T) {
	defaults := platform.SystemLayoutDefaults()
	var endpoint, credential string
	for _, uid := range []uint32{1001, 1002} {
		defaults.TrustedHome = filepath.Join("/home", map[uint32]string{1001: "fixture-a", 1002: "fixture-b"}[uid])
		layout, err := platform.ResolveLayout(platform.LayoutInput{EUID: uid}, defaults)
		if err != nil {
			t.Fatal(err)
		}
		locator, err := layout.Locator(uid)
		if err != nil {
			t.Fatal(err)
		}
		if endpoint != "" && (endpoint != locator.Endpoint || credential != locator.Credential) {
			t.Fatal("users selected different machine control")
		}
		if locator.ExpectedOwner != 0 || layout.ClientLogs.Root == layout.Data.Root {
			t.Fatal("user/root ownership boundaries collapsed")
		}
		endpoint, credential = locator.Endpoint, locator.Credential
	}
}
