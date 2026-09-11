package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// NT SERVICE\TrustedInstaller. C:\ and Program Files are owned by this SID.
const windowsTrustedInstallerSID = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"

func openReplacementFile(ctx context.Context, path string) (file *os.File, id string, trusted bool, verify func() error, closeParent func() error, err error) {
	type link struct {
		path   string
		handle windows.Handle
		id     fileIdentity
	}
	var chain []link
	closeParent = func() error {
		var e error
		for _, l := range chain {
			e = errors.Join(e, windows.CloseHandle(l.handle))
		}
		return e
	}
	cleanup := closeParent
	defer func() {
		if err != nil {
			err = errors.Join(err, cleanup())
		}
	}()
	volume := filepath.VolumeName(path)
	current := volume + string(os.PathSeparator)
	parts := strings.Split(strings.TrimPrefix(path, current), string(os.PathSeparator))
	paths := []string{current}
	for _, part := range parts {
		current = filepath.Join(current, part)
		paths = append(paths, current)
	}
	elevated := windows.GetCurrentProcessToken().IsElevated()
	system, systemErr := processIsLocalSystem()
	user, userErr := currentUserSID()
	trusted = systemErr == nil && userErr == nil
	elevated = elevated || system
	for i, p := range paths {
		if err = ctx.Err(); err != nil {
			return nil, "", false, nil, nil, err
		}
		h, e := replacementWindowsOpen(p, i == len(paths)-1)
		if e != nil {
			return nil, "", false, nil, nil, e
		}
		ident, e := identityFromHandle(h)
		chain = append(chain, link{p, h, ident})
		if e != nil {
			return nil, "", false, nil, nil, e
		}
		sd, e := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if e != nil || !replacementWindowsExecutionTrust(sd, elevated, user, filepath.Dir(p) == p) {
			trusted = false
		}
	}
	last := chain[len(chain)-1]
	canonical, err := finalPathFromHandle(last.handle)
	if err != nil {
		return nil, "", false, nil, nil, err
	}
	// The file owns the final handle; the parent cleanup owns only directories.
	chain = chain[:len(chain)-1]
	file = os.NewFile(uintptr(last.handle), canonical)
	verify = func() error {
		for _, l := range append(append([]link(nil), chain...), last) {
			h, e := replacementWindowsOpen(l.path, l.path == path)
			if e != nil {
				return e
			}
			actual, e := identityFromHandle(h)
			if trusted {
				sd, securityErr := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
				if securityErr != nil || !replacementWindowsExecutionTrust(sd, elevated, user, filepath.Dir(l.path) == l.path) {
					e = errors.Join(e, ErrIdentityMismatch, securityErr)
				}
			}
			e = errors.Join(e, windows.CloseHandle(h))
			if e != nil {
				return e
			}
			if actual != l.id {
				return ErrIdentityMismatch
			}
		}
		return nil
	}
	return file, fmt.Sprintf("%x:%x", last.id.volume, last.id.fileID), trusted, verify, closeParent, nil
}

func replacementWindowsOpen(path string, file bool) (windows.Handle, error) {
	access := uint32(windows.FILE_READ_ATTRIBUTES | windows.READ_CONTROL | windows.SYNCHRONIZE)
	options := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_DIRECTORY_FILE)
	if file {
		access |= windows.FILE_READ_DATA
		options = windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT | windows.FILE_NON_DIRECTORY_FILE
	}
	h, err := openNTPath(path, access, windows.FILE_OPEN, options, 0, nil)
	if errors.Is(err, windows.STATUS_ACCESS_DENIED) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// Readable bytes remain observable even when ACL inspection is denied.
		// GetSecurityInfo then leaves execution trust unproved.
		h, err = openNTPath(path, access&^windows.READ_CONTROL, windows.FILE_OPEN, options, 0, nil)
	}
	if err != nil {
		return 0, err
	}
	if err = rejectReparse(h, path); err != nil {
		return 0, errors.Join(err, windows.CloseHandle(h))
	}
	return h, nil
}

func replacementWindowsExecutionTrust(sd *windows.SECURITY_DESCRIPTOR, elevated bool, user *windows.SID, volumeRoot bool) bool {
	if sd == nil {
		return false
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false
	}
	installer, err := windows.StringToSid(windowsTrustedInstallerSID)
	if err != nil {
		return false
	}
	allowed := func(sid *windows.SID) bool {
		return sid != nil && (sid.Equals(system) || sid.Equals(admins) || sid.Equals(installer) || (!elevated && user != nil && sid.Equals(user)))
	}
	owner, _, err := sd.Owner()
	if err != nil || !allowed(owner) {
		return false
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return false
	}
	write := windows.ACCESS_MASK(windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | installControlFileDeleteChild | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_WRITE | windows.GENERIC_ALL)
	if volumeRoot {
		// Volume roots commonly let Authenticated Users create directories.
		// That does not allow replacing existing children such as Program Files.
		write &^= windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return false
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return false
		}
		if ace.Mask&write != 0 && !allowed((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
			return false
		}
	}
	return true
}

func validateReplacementParent(ctx context.Context, path string) error {
	var paths []string
	for {
		paths = append(paths, path)
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	for i := len(paths) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		handle, err := replacementWindowsOpen(paths[i], false)
		if err != nil {
			return err
		}
		if err = windows.CloseHandle(handle); err != nil {
			return err
		}
	}
	return nil
}
