//go:build linux

package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/sysproxy"
)

func TestSystemProxyWithoutDesktopSessionDoesNotDegrade(t *testing.T) {
	root := t.TempDir()
	// gsettings can return success while dconf fails to commit without a session.
	script := "#!/bin/sh\nif [ \"$1\" = get ]; then printf \"'none'\\n\"; else printf 'dconf-WARNING: failed to commit changes: no session bus\\n' >&2; fi\n"
	if err := os.WriteFile(filepath.Join(root, "gsettings"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	manager := newTestManager(Options{SysProxy: sysproxy.PlatformBackend{}, Settings: defaultSysProxySettings(false), SettingsPath: filepath.Join(root, "settings.yaml")})
	_, err := manager.EnableSystemProxy(t.Context(), Operation{ID: "no-session", Source: "test"}, false)
	if err == nil || !strings.Contains(err.Error(), "read system proxy state") {
		t.Fatal("expected missing desktop session error")
	}
	if manager.Snapshot().Health == "degraded" {
		t.Fatalf("missing desktop session degraded daemon: %v", err)
	}
	if manager.settingsSnapshot().SystemProxyDesired {
		t.Fatal("saved unsupported proxy intent")
	}
	if manager.Snapshot().Revision != 0 {
		t.Fatal("unsupported proxy operation advanced revision")
	}
	if _, err := os.Stat(filepath.Join(root, "settings.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsupported operation wrote settings: %v", err)
	}
}
