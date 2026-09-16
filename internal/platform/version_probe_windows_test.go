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

func TestWindowsVersionProbe_IdentificationLinkedToken(t *testing.T) {
	// Windows may return a linked UAC token usable only for identification.
	// Reproduce that token type without requiring an elevated test runner.
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
	identity, err := readWindowsVersionIdentity(probe.token)
	if err != nil {
		t.Fatal(err)
	}
	identity.elevated = true // model the caller of the filtered linked token
	var linked windows.Token
	if err := windows.DuplicateTokenEx(probe.token, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, nil, windows.SecurityIdentification, windows.TokenImpersonation, &linked); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := linked.Close(); err != nil {
			t.Error(err)
		}
	})
	var shell windows.Token
	primary, err := duplicateWindowsVersionProbeToken(linked, identity, func() (windows.Token, error) {
		err := windows.DuplicateTokenEx(probe.token, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY, nil, windows.SecurityImpersonation, windows.TokenPrimary, &shell)
		return shell, err
	})
	if err != nil {
		t.Fatalf("identification-only linked token must use the verified same-logon primary token: %v", err)
	}
	t.Cleanup(func() {
		if err := primary.Close(); err != nil {
			t.Error(err)
		}
	})
	actual, err := readWindowsVersionIdentity(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWindowsVersionIdentity(actual, identity.user, identity.session); err != nil {
		t.Fatal(err)
	}
	if _, err := readWindowsVersionIdentity(shell); err == nil {
		t.Fatal("shell source token leaked")
	}
	for _, mode := range []string{"shell unavailable", "invalid shell token", "shell identification token", "missing caller logon", "non-elevated caller", "invalid linked token", "primary already usable"} {
		t.Run(mode, func(t *testing.T) {
			caller, selected := identity, linked
			wantCalls := 1
			switch mode {
			case "missing caller logon":
				caller.logon = nil
				wantCalls = 0
			case "non-elevated caller":
				caller.elevated = false
				wantCalls = 0
			case "invalid linked token":
				selected = 0
				wantCalls = 0
			case "primary already usable":
				selected = probe.token
				wantCalls = 0
			}
			calls := 0
			var source windows.Token
			got, err := duplicateWindowsVersionProbeToken(selected, caller, func() (windows.Token, error) {
				calls++
				if mode == "shell unavailable" {
					return 0, os.ErrNotExist
				}
				if mode == "invalid shell token" {
					return 0, nil
				}
				err := windows.DuplicateTokenEx(probe.token, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, nil, windows.SecurityIdentification, windows.TokenImpersonation, &source)
				return source, err
			})
			if got != 0 {
				if closeErr := got.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
			}
			if (err == nil) != (mode == "primary already usable") || calls != wantCalls {
				t.Fatalf("token=%v error=%v shell calls=%d want=%d", got, err, calls, wantCalls)
			}
			if mode == "shell unavailable" && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("shell error lost: %v", err)
			}
			if source != 0 {
				if _, err := readWindowsVersionIdentity(source); err == nil {
					t.Fatal("failed fallback leaked source token")
				}
			}
		})
	}
}

func TestWindowsVersionProbe_ShellIdentityRequiresSameLogon(t *testing.T) {
	sid := func(value string) *windows.SID {
		t.Helper()
		result, err := windows.StringToSid(value)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	user := sid("S-1-5-21-1-2-3-1000")
	logon := sid("S-1-5-5-1-2")
	expected := windowsVersionIdentity{user: user, logon: logon, session: 1, integrity: 0x2000}
	for _, tc := range []struct {
		name   string
		change func(*windowsVersionIdentity)
		want   bool
	}{
		{"same logon", func(*windowsVersionIdentity) {}, true},
		{"different user", func(id *windowsVersionIdentity) { id.user = sid("S-1-5-21-1-2-3-1001") }, false},
		{"different desktop session", func(id *windowsVersionIdentity) { id.session++ }, false},
		{"same user different logon", func(id *windowsVersionIdentity) { id.logon = sid("S-1-5-5-1-3") }, false},
		{"missing logon", func(id *windowsVersionIdentity) { id.logon = nil }, false},
		{"elevated shell", func(id *windowsVersionIdentity) { id.elevated = true }, false},
		{"admin shell", func(id *windowsVersionIdentity) { id.admin = true }, false},
		{"high integrity shell", func(id *windowsVersionIdentity) { id.integrity = 0x3000 }, false},
		{"UI access shell", func(id *windowsVersionIdentity) { id.uiAccess = true }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual := expected
			tc.change(&actual)
			err := validateWindowsVersionShellIdentity(actual, expected)
			if (err == nil) != tc.want || (!tc.want && !errors.Is(err, os.ErrPermission)) {
				t.Fatalf("accepted=%v want=%v error=%v", err == nil, tc.want, err)
			}
		})
	}
}

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
	if err := probe.Run(context.Background(), exec.Command("must-not-execute.exe", "self", "version", "--json")); !errors.Is(err, os.ErrClosed) {
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
		if err := probe.Run(context.Background(), cmd); !errors.Is(err, os.ErrInvalid) || cmd.Process != nil {
			t.Fatalf("unexpected command accepted: %v", err)
		}
	}
	cmd := exec.Command(filepath.Join(t.TempDir(), "absent.exe"), "self", "version", "--json")
	cmd.Env = []string{}
	cmd.SysProcAttr = &syscall.SysProcAttr{AdditionalInheritedHandles: []syscall.Handle{1}}
	if err := probe.Run(context.Background(), cmd); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("inherited authority accepted: %v", err)
	}
	cmd.SysProcAttr = nil
	if err := probe.Run(context.Background(), cmd); err == nil || cmd.Process != nil {
		t.Fatalf("absent binary started: %v", err)
	}
	expectedToken := syscall.Token(probe.token)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Token != expectedToken || cmd.SysProcAttr.ParentProcess != 0 || !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags != windows.CREATE_NO_WINDOW || len(cmd.SysProcAttr.AdditionalInheritedHandles) != 0 {
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
