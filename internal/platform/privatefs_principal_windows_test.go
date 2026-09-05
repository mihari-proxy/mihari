//go:build windows

package platform

import (
	"golang.org/x/sys/windows"
	"testing"
)

func TestPrivateDataPrincipal_AdminOwnerPreservesExplicitUser(t *testing.T) {
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;" + user.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	got, err := privateDataPrincipal(sd)
	if err != nil || !got.Equals(user) {
		t.Fatalf("admin owner lost explicit user: principal=%v error=%v", got, err)
	}
}

func TestPrivateDataPrincipal_IndividualOwnerIsUnchanged(t *testing.T) {
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + user.String() + "D:P(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		t.Fatal(err)
	}
	got, err := privateDataPrincipal(sd)
	if err != nil || !got.Equals(user) {
		t.Fatal("individual owner changed")
	}
}

func TestPrivateDataPrincipal_LegacyLocalSystemDoesNotGuessUser(t *testing.T) {
	old := processIsLocalSystem
	t.Cleanup(func() { processIsLocalSystem = old })
	processIsLocalSystem = func() (bool, error) { return true, nil }
	sd, err := windows.SecurityDescriptorFromString("O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		t.Fatal(err)
	}
	got, err := privateDataPrincipal(sd)
	admins, e := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil || e != nil || !got.Equals(admins) {
		t.Fatal("LocalSystem guessed an unrelated desktop user")
	}
}

func TestPrivateDataPrincipal_DenyDoesNotBecomeGrant(t *testing.T) {
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("O:BAD:P(D;;GR;;;" + user.String() + ")(A;;FA;;;" + user.String() + ")(A;;FA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := privateDataPrincipal(sd); err == nil {
		t.Fatal("deny ACE was converted into full access")
	}
}

func TestPrivateDataPrincipal_SystemDoesNotLeaveBroadRootUnhardened(t *testing.T) {
	old := processIsLocalSystem
	t.Cleanup(func() { processIsLocalSystem = old })
	processIsLocalSystem = func() (bool, error) { return true, nil }
	sd, err := windows.SecurityDescriptorFromString("O:BAD:P(A;;FA;;;BA)(A;;FA;;;SY)(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := privateDataPrincipal(sd); err == nil {
		t.Fatal("unresolved SYSTEM accepted broad root without hardening")
	}
}
