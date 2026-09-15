package platform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsVersionProbe_RejectsPrivilegedOrDifferentIdentity(t *testing.T) {
	user, err := windows.StringToSid("S-1-5-21-1-2-3-1000")
	if err != nil {
		t.Fatal(err)
	}
	other, err := windows.StringToSid("S-1-5-21-1-2-3-1001")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*windowsVersionIdentity)
		want   bool
	}{
		{"same user medium", func(*windowsVersionIdentity) {}, true},
		{"different user", func(id *windowsVersionIdentity) { id.user = other }, false},
		{"missing user", func(id *windowsVersionIdentity) { id.user = nil }, false},
		{"different session", func(id *windowsVersionIdentity) { id.session++ }, false},
		{"elevated", func(id *windowsVersionIdentity) { id.elevated = true }, false},
		{"admin group enabled", func(id *windowsVersionIdentity) { id.admin = true }, false},
		{"high integrity", func(id *windowsVersionIdentity) { id.integrity = 0x3000 }, false},
		{"medium plus", func(id *windowsVersionIdentity) { id.integrity = 0x2100 }, false},
		{"UI access", func(id *windowsVersionIdentity) { id.uiAccess = true }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := windowsVersionIdentity{user: user, session: 1, integrity: 0x2000}
			tc.change(&id)
			err := validateWindowsVersionIdentity(id, user, 1)
			if (err == nil) != tc.want || (!tc.want && !errors.Is(err, os.ErrPermission)) {
				t.Fatalf("token accepted=%v want=%v error=%v", err == nil, tc.want, err)
			}
		})
	}
	for _, kind := range []windows.WELL_KNOWN_SID_TYPE{windows.WinLocalSystemSid, windows.WinLocalServiceSid, windows.WinNetworkServiceSid} {
		sid, err := windows.CreateWellKnownSid(kind)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateWindowsVersionIdentity(windowsVersionIdentity{user: sid, integrity: 0x2000}, sid, 0); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("service identity accepted: %v", err)
		}
	}
}

func TestWindowsVersionProbe_InvalidAndClosedTokenCannotExecute(t *testing.T) {
	if _, err := readWindowsVersionIdentity(0); err == nil {
		t.Fatal("invalid token inspection succeeded")
	}
	probe := &WindowsUserVersionProbe{}
	if err := probe.Run(exec.Command("must-not-execute.exe", "self", "version", "--json")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed token run: %v", err)
	}
	if _, err := probe.Observe(context.Background(), filepath.Join(t.TempDir(), "mihari.exe")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed token observation: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsVersionProbe_CurrentTokenInspection(t *testing.T) {
	identity, err := readWindowsVersionIdentity(windows.GetCurrentProcessToken())
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if identity.user == nil || !identity.user.Equals(user.User.Sid) || identity.elevated != windows.GetCurrentProcessToken().IsElevated() || identity.integrity == 0 {
		t.Fatal("native token inspection lost user, elevation or integrity")
	}
}

func TestWindowsVersionProbe_CommandUsesOnlyHeldTokenAndClosesIt(t *testing.T) {
	if windows.GetCurrentProcessToken().IsElevated() {
		linked, err := windows.GetCurrentProcessToken().GetLinkedToken()
		if err != nil {
			t.Skip("host has no filtered UAC token")
		}
		if err := linked.Close(); err != nil {
			t.Fatal(err)
		}
	}
	probe, err := OpenWindowsUserVersionProbe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := probe.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, args := range [][]string{{"daemon"}, {"self", "update", "--json"}, {"self", "version", "--json", "extra"}} {
		cmd := exec.Command("must-not-execute.exe", args...)
		cmd.Env = []string{}
		if err := probe.Run(cmd); !errors.Is(err, os.ErrInvalid) || cmd.Process != nil {
			t.Fatalf("unexpected command accepted: %v", err)
		}
	}
	cmd := exec.Command(filepath.Join(t.TempDir(), "absent.exe"), "self", "version", "--json")
	cmd.Env = []string{}
	cmd.SysProcAttr = &syscall.SysProcAttr{AdditionalInheritedHandles: []syscall.Handle{1}}
	if err := probe.Run(cmd); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("inherited authority accepted: %v", err)
	}
	cmd.SysProcAttr = nil
	if err := probe.Run(cmd); err == nil || cmd.Process != nil {
		t.Fatalf("absent binary started: %v", err)
	}
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Token != syscall.Token(probe.token) || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags != windows.CREATE_NO_WINDOW || len(cmd.SysProcAttr.AdditionalInheritedHandles) != 0 {
		t.Fatal("start failure lost the held token or leaked inherited authority")
	}
	token := probe.token
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readWindowsVersionIdentity(token); err == nil {
		t.Fatal("token leaked after Close")
	}
}
