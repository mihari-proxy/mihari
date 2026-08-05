//go:build windows

package sysproxy

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestShouldUseUserHive(t *testing.T) {
	t.Parallel()

	if !shouldUseUserHive(true) {
		t.Fatal("shouldUseUserHive(true) = false, want true for LocalSystem")
	}
	if shouldUseUserHive(false) {
		t.Fatal("shouldUseUserHive(false) = true, want false for interactive user")
	}
}

func TestInternetSettingsKeyPath(t *testing.T) {
	t.Parallel()

	const sid = "S-1-5-21-3623811015-3361044348-30300820-1001"
	got := internetSettingsKeyPath(sid)
	want := sid + `\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	if got != want {
		t.Fatalf("internetSettingsKeyPath() = %q, want %q", got, want)
	}
}

func TestProxyOverrideValue(t *testing.T) {
	t.Parallel()

	got := proxyOverrideValue()
	want := "localhost;127.0.0.1;::1;<local>"
	if got != want {
		t.Fatalf("proxyOverrideValue() = %q, want %q", got, want)
	}
}

// Hook-mutating tests are not parallel: they swap package-level function vars.

func TestResolveInternetSettingsTarget_InteractiveUser(t *testing.T) {
	prevSystem := isLocalSystemFn
	prevSID := consoleUserSIDFn
	t.Cleanup(func() {
		isLocalSystemFn = prevSystem
		consoleUserSIDFn = prevSID
	})

	isLocalSystemFn = func() (bool, error) { return false, nil }
	consoleUserSIDFn = func() (string, error) {
		t.Fatal("consoleUserSIDFn must not be called for interactive user")
		return "", nil
	}

	root, path, err := resolveInternetSettingsTarget()
	if err != nil {
		t.Fatalf("resolveInternetSettingsTarget() error = %v", err)
	}
	if root != registry.CURRENT_USER {
		t.Fatalf("root = %v, want CURRENT_USER", root)
	}
	if path != internetSettingsPath {
		t.Fatalf("path = %q, want %q", path, internetSettingsPath)
	}
}

func TestResolveInternetSettingsTarget_LocalSystemWithSID(t *testing.T) {
	prevSystem := isLocalSystemFn
	prevSID := consoleUserSIDFn
	t.Cleanup(func() {
		isLocalSystemFn = prevSystem
		consoleUserSIDFn = prevSID
	})

	const sid = "S-1-5-21-1-2-3-1001"
	isLocalSystemFn = func() (bool, error) { return true, nil }
	consoleUserSIDFn = func() (string, error) { return sid, nil }

	root, path, err := resolveInternetSettingsTarget()
	if err != nil {
		t.Fatalf("resolveInternetSettingsTarget() error = %v", err)
	}
	if root != registry.USERS {
		t.Fatalf("root = %v, want USERS", root)
	}
	wantPath := internetSettingsKeyPath(sid)
	if path != wantPath {
		t.Fatalf("path = %q, want %q", path, wantPath)
	}
}

func TestResolveInternetSettingsTarget_LocalSystemNoSession(t *testing.T) {
	prevSystem := isLocalSystemFn
	prevSID := consoleUserSIDFn
	t.Cleanup(func() {
		isLocalSystemFn = prevSystem
		consoleUserSIDFn = prevSID
	})

	isLocalSystemFn = func() (bool, error) { return true, nil }
	consoleUserSIDFn = func() (string, error) {
		return "", errors.New("sysproxy: no interactive user session")
	}

	_, _, err := resolveInternetSettingsTarget()
	if err == nil {
		t.Fatal("resolveInternetSettingsTarget() error = nil, want no interactive session")
	}
	if !strings.Contains(err.Error(), "no interactive user session") {
		t.Fatalf("error = %v, want mention of no interactive user session", err)
	}
}

func TestResolveInternetSettingsTarget_LocalSystemEmptySID(t *testing.T) {
	prevSystem := isLocalSystemFn
	prevSID := consoleUserSIDFn
	t.Cleanup(func() {
		isLocalSystemFn = prevSystem
		consoleUserSIDFn = prevSID
	})

	isLocalSystemFn = func() (bool, error) { return true, nil }
	consoleUserSIDFn = func() (string, error) { return "", nil }

	_, _, err := resolveInternetSettingsTarget()
	if err == nil {
		t.Fatal("resolveInternetSettingsTarget() error = nil, want no interactive session")
	}
	if !strings.Contains(err.Error(), "no interactive user session") {
		t.Fatalf("error = %v, want mention of no interactive user session", err)
	}
}

func TestResolveInternetSettingsTarget_DetectSystemError(t *testing.T) {
	prevSystem := isLocalSystemFn
	t.Cleanup(func() { isLocalSystemFn = prevSystem })

	wantErr := errors.New("token failed")
	isLocalSystemFn = func() (bool, error) { return false, wantErr }

	_, _, err := resolveInternetSettingsTarget()
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}
