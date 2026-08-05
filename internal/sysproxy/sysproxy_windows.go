//go:build windows

package sysproxy

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// internetSettingsPath is the per-user WinINET configuration key relative to
// HKCU or HKEY_USERS\<SID>.
const internetSettingsPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// INTERNET_OPTION_* selectors for InternetSetOptionW. Broadcasting both makes
// already-running apps pick up the change without a restart.
const (
	internetOptionSettingsChanged = 39
	internetOptionRefresh         = 37
)

var (
	modwininet            = windows.NewLazySystemDLL("wininet.dll")
	procInternetSetOption = modwininet.NewProc("InternetSetOptionW")
)

// proxyOverrideValue formats defaultBypass for WinINET ProxyOverride
// (semicolon-separated, with trailing <local>).
func proxyOverrideValue() string {
	return strings.Join(append(append([]string{}, defaultBypass...), "<local>"), ";")
}

// enable writes the WinINET proxy keys and notifies the system.
func enable(host string, port int) error {
	key, err := openInternetSettings(registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	server := NormalizeServer(host, port)
	if err := key.SetStringValue("ProxyServer", server); err != nil {
		return fmt.Errorf("sysproxy: set ProxyServer: %w", err)
	}
	if err := key.SetStringValue("ProxyOverride", proxyOverrideValue()); err != nil {
		return fmt.Errorf("sysproxy: set ProxyOverride: %w", err)
	}
	if err := key.SetDWordValue("ProxyEnable", 1); err != nil {
		return fmt.Errorf("sysproxy: set ProxyEnable: %w", err)
	}
	return notifyChange()
}

// disable clears the ProxyEnable flag, leaving the server value in place so the
// user's prior endpoint is remembered by the OS proxy UI.
func disable() error {
	key, err := openInternetSettings(registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	if err := key.SetDWordValue("ProxyEnable", 0); err != nil {
		return fmt.Errorf("sysproxy: clear ProxyEnable: %w", err)
	}
	return notifyChange()
}

// get reads the current WinINET proxy state.
func get() (State, error) {
	key, err := openInternetSettings(registry.QUERY_VALUE)
	if err != nil {
		return State{}, err
	}
	defer key.Close()

	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil && err != registry.ErrNotExist {
		return State{}, fmt.Errorf("sysproxy: read ProxyEnable: %w", err)
	}
	server, _, err := key.GetStringValue("ProxyServer")
	if err != nil && err != registry.ErrNotExist {
		return State{}, fmt.Errorf("sysproxy: read ProxyServer: %w", err)
	}
	st := State{Enabled: enabled == 1}
	if st.Enabled {
		st.Server = server
	}
	return st, nil
}

// openInternetSettings opens the Internet Settings key for the target user hive.
func openInternetSettings(access uint32) (registry.Key, error) {
	root, path, err := resolveInternetSettingsTarget()
	if err != nil {
		return 0, err
	}
	key, err := registry.OpenKey(root, path, access)
	if err != nil {
		return 0, fmt.Errorf("sysproxy: open registry: %w", err)
	}
	return key, nil
}

// notifyChange broadcasts that the WinINET settings changed so live processes
// reload them. Failures here are non-fatal: the registry is already updated.
func notifyChange() error {
	_, _, _ = procInternetSetOption.Call(0, uintptr(internetOptionSettingsChanged), 0, 0)
	_, _, _ = procInternetSetOption.Call(0, uintptr(internetOptionRefresh), 0, 0)
	return nil
}
