//go:build linux

package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
)

// installSessionlessGsettings supplies a read-only observation independently of
// the session bus. A write would appear successful despite failing to commit.
func installSessionlessGsettings(t *testing.T, mode, port string, readFails bool) string {
	t.Helper()
	root := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = get ]; then
 if [ "$MIHARI_TEST_PROXY_READ_FAILS" = true ]; then exit 1; fi
 case "$3" in
 mode) printf "'%s'\n" "$MIHARI_TEST_PROXY_MODE" ;;
 host) printf "'127.0.0.1'\n" ;;
 port) printf '%s\n' "$MIHARI_TEST_PROXY_PORT" ;;
 esac
else
 : > "$MIHARI_TEST_PROXY_WRITE_MARKER"
 printf 'dconf-WARNING: failed to commit changes: no session bus\n' >&2
fi
`
	if err := os.WriteFile(filepath.Join(root, "gsettings"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("MIHARI_TEST_PROXY_MODE", mode)
	t.Setenv("MIHARI_TEST_PROXY_PORT", port)
	fail := "false"
	if readFails {
		fail = "true"
	}
	t.Setenv("MIHARI_TEST_PROXY_READ_FAILS", fail)
	t.Setenv("MIHARI_TEST_PROXY_WRITE_MARKER", filepath.Join(root, "write-attempted"))
	return root
}

func assertNoSessionlessProxyWrite(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, "write-attempted")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("attempted OS proxy write: %v", err)
	}
}

func TestSystemProxyWithoutDesktopSessionDoesNotDegrade(t *testing.T) {
	for _, enable := range []bool{true, false} {
		name, mode := "enable", "none"
		if !enable {
			name, mode = "disable", "manual"
		}
		t.Run(name, func(t *testing.T) {
			root := installSessionlessGsettings(t, mode, "9190", false)
			manager := newTestManager(Options{SysProxy: sysproxy.PlatformBackend{}, Settings: defaultSysProxySettings(!enable), SettingsPath: filepath.Join(root, "settings.yaml")})
			var err error
			if enable {
				_, err = manager.EnableSystemProxy(t.Context(), Operation{ID: "no-session", Source: "test"}, false)
			} else {
				_, err = manager.DisableSystemProxy(t.Context(), Operation{ID: "no-session", Source: "test"})
			}
			var api protocol.APIError
			if !errors.As(err, &api) || api.Code != protocol.CodeUpstreamFailure || !strings.Contains(diagnostics.Capture(err).Text, "GNOME desktop session is unavailable") {
				t.Fatalf("expected session preflight failure: %v", err)
			}
			if manager.Snapshot().Health == "degraded" {
				t.Fatalf("missing desktop session degraded daemon: %v", err)
			}
			if manager.settingsSnapshot().SystemProxyDesired != !enable {
				t.Fatal("changed unsupported proxy intent")
			}
			if manager.Snapshot().Revision != 0 {
				t.Fatal("unsupported proxy operation advanced revision")
			}
			if _, err := os.Stat(filepath.Join(root, "settings.yaml")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unsupported operation wrote settings: %v", err)
			}
			assertNoSessionlessProxyWrite(t, root)
		})
	}
}

func TestDataResetWithoutDesktopSessionUsesObservedProxy(t *testing.T) {
	for _, tc := range []struct {
		name, mode, port              string
		desired, readFails, wantReset bool
	}{
		{name: "disabled", mode: "none", wantReset: true},
		{name: "disabled_with_saved_intent", mode: "none", desired: true, wantReset: true},
		{name: "foreign", mode: "manual", port: "7890", wantReset: true},
		{name: "owned", mode: "manual", port: "9190", desired: true},
		{name: "owned_without_saved_intent", mode: "manual", port: "9190"},
		{name: "unreadable", readFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := installSessionlessGsettings(t, tc.mode, tc.port, tc.readFails)
			reset := &resetFailureStore{}
			manager := newTestManager(Options{BusinessReset: reset, SysProxy: sysproxy.PlatformBackend{}, Settings: defaultSysProxySettings(tc.desired)})
			manager.store.Store(state.Snapshot{Health: "ok", Core: state.CoreState{Status: "stopped"}})
			result, err := manager.ResetBusinessData(t.Context(), Operation{ID: "no-session-reset"}, protocol.DataResetRequest{Digest: "fixture", Confirm: true, ConfirmAgain: true})
			if tc.wantReset {
				if err != nil || !result.SetupRequired || !result.RestartRequired || !reset.pending {
					t.Fatalf("reset did not complete: result=%+v err=%v", result, err)
				}
			} else {
				if err == nil || reset.pending || manager.Snapshot().Revision != 0 || manager.settingsSnapshot().SystemProxyDesired != tc.desired {
					t.Fatalf("reset proceeded without confirmed proxy cleanup: err=%v pending=%v", err, reset.pending)
				}
			}
			if manager.Snapshot().Health == "degraded" {
				t.Fatal("proxy preflight degraded daemon")
			}
			assertNoSessionlessProxyWrite(t, root)
		})
	}
}
