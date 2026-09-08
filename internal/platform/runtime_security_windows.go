//go:build windows

package platform

import (
	"golang.org/x/sys/windows"
	"unsafe"
)

func windowsRuntimeProtectedPolicy(sd *windows.SECURITY_DESCRIPTOR, fullAccess uint32) (bool, error) {
	if sd == nil {
		return false, nil
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false, err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return false, err
	}
	if owner == nil || !owner.Equals(admins) && !owner.Equals(system) {
		return false, nil
	}
	control, _, err := sd.Control()
	if err != nil {
		return false, err
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return false, nil
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return false, err
	}
	if dacl == nil || dacl.AceCount != 2 {
		return false, nil
	}
	var sawAdmins, sawSystem bool
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return false, err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 || ace.Mask != windows.GENERIC_ALL && uint32(ace.Mask) != fullAccess {
			return false, nil
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		switch {
		case sid.Equals(admins):
			sawAdmins = true
		case sid.Equals(system):
			sawSystem = true
		default:
			return false, nil
		}
	}
	return sawAdmins && sawSystem, nil
}
