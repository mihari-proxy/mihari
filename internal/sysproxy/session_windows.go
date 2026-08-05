//go:build windows

package sysproxy

import (
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// localSystemSID is the well-known SID for NT AUTHORITY\SYSTEM.
const localSystemSID = "S-1-5-18"

// injectable hooks so unit tests can exercise hive selection without SYSTEM.
var (
	isLocalSystemFn  = detectLocalSystem
	consoleUserSIDFn = resolveConsoleUserSID
)

// shouldUseUserHive reports whether registry access must go through
// HKEY_USERS\<SID> instead of CURRENT_USER. LocalSystem's HKCU is the SYSTEM
// hive and does not affect the interactive desktop user.
func shouldUseUserHive(isSystem bool) bool {
	return isSystem
}

// internetSettingsKeyPath joins a user SID with the WinINET Internet Settings
// relative path for use under registry.USERS.
func internetSettingsKeyPath(sid string) string {
	return sid + `\` + internetSettingsPath
}

// resolveInternetSettingsTarget chooses the registry root and relative path for
// WinINET settings based on process identity and the console session user.
func resolveInternetSettingsTarget() (root registry.Key, path string, err error) {
	isSystem, err := isLocalSystemFn()
	if err != nil {
		return 0, "", err
	}
	if !shouldUseUserHive(isSystem) {
		return registry.CURRENT_USER, internetSettingsPath, nil
	}
	sid, err := consoleUserSIDFn()
	if err != nil {
		return 0, "", err
	}
	if sid == "" {
		return 0, "", fmt.Errorf("sysproxy: no interactive user session")
	}
	return registry.USERS, internetSettingsKeyPath(sid), nil
}

// detectLocalSystem reports whether the current process token is LocalSystem.
func detectLocalSystem() (bool, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return false, fmt.Errorf("sysproxy: open process token: %w", err)
	}
	defer token.Close()

	tu, err := token.GetTokenUser()
	if err != nil {
		return false, fmt.Errorf("sysproxy: get token user: %w", err)
	}
	sid := tu.User.Sid.String()
	if sid == "" {
		return false, fmt.Errorf("sysproxy: empty process token SID")
	}
	return sid == localSystemSID, nil
}

// resolveConsoleUserSID returns the SID string of the interactive console session
// user. Requires privileges available to LocalSystem (SeTcbPrivilege).
func resolveConsoleUserSID() (string, error) {
	sessionID := windows.WTSGetActiveConsoleSessionId()
	if sessionID == 0xFFFFFFFF {
		return "", fmt.Errorf("sysproxy: no interactive user session")
	}

	var token windows.Token
	if err := windows.WTSQueryUserToken(sessionID, &token); err != nil {
		return "", fmt.Errorf("sysproxy: no interactive user session: %w", err)
	}
	defer token.Close()

	tu, err := token.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("sysproxy: get console user token: %w", err)
	}
	sid := tu.User.Sid.String()
	if sid == "" {
		return "", fmt.Errorf("sysproxy: no interactive user session")
	}
	return sid, nil
}
