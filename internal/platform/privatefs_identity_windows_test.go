//go:build windows

package platform

import (
	"golang.org/x/sys/windows"
	"testing"
)

func TestCurrentProcessIsLocalSystem_UsesPrimaryToken(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	want := user.User.Sid.String() == "S-1-5-18"
	got, err := currentProcessIsLocalSystem()
	if err != nil {
		t.Fatalf("query process identity: %v", err)
	}
	if got != want {
		t.Fatalf("LocalSystem=%v want %v", got, want)
	}
}

func TestPrivateDataPrincipal_SystemAccountTypeDependsOnCaller(t *testing.T) {
	// Synthetic personal SID keeps the fixture valid even under a SYSTEM runner.
	user, err := windows.StringToSid("S-1-5-21-1-2-3-1001")
	if err != nil {
		t.Fatal(err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	oldLookup, oldSystem := privateAccountKind, processIsLocalSystem
	t.Cleanup(func() { privateAccountKind, processIsLocalSystem = oldLookup, oldSystem })
	processIsLocalSystem = func() (bool, error) { return true, nil }
	for _, kind := range []uint32{windows.SidTypeUser, windows.SidTypeWellKnownGroup} {
		privateAccountKind = func(sid *windows.SID) (uint32, error) {
			if sid.String() == "S-1-5-18" {
				return kind, nil
			}
			if sid.Equals(user) {
				return windows.SidTypeUser, nil
			}
			return oldLookup(sid)
		}
		for _, explicitUser := range []bool{true, false} {
			sddl := "O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)"
			want := admins
			if explicitUser {
				sddl += "(A;;FA;;;" + user.String() + ")"
				want = user
			}
			sd, err := windows.SecurityDescriptorFromString(sddl)
			if err != nil {
				t.Fatal(err)
			}
			got, err := privateDataPrincipal(sd)
			if err != nil || got == nil || !got.Equals(want) {
				t.Errorf("SYSTEM kind=%d explicitUser=%v got=%v err=%v want=%v", kind, explicitUser, got, err, want)
			}
		}
	}
}
