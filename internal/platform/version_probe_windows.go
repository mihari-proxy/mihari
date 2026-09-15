package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// WindowsUserVersionProbe owns a same-user, non-administrative token for a
// single version observation. Observe, Run and Close must be called serially.
// Its trust policy must never be used to launch an elevated process.
type WindowsUserVersionProbe struct {
	token   windows.Token
	user    *windows.SID
	session uint32
}

// OpenWindowsUserVersionProbe uses the caller's filtered UAC token when elevated.
// Missing linked tokens and unprovable privilege reduction fail closed. It never
// selects another desktop user, prompts for elevation, or changes parent rights.
func OpenWindowsUserVersionProbe() (probe *WindowsUserVersionProbe, err error) {
	var source windows.Token
	if err = windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &source); err != nil {
		return nil, fmt.Errorf("open version probe token: %w", err)
	}
	// Any cleanup failure must also close a capability that would otherwise have
	// been returned successfully; callers cannot use a partially opened probe.
	defer func() {
		err = errors.Join(err, source.Close())
		if err != nil && probe != nil {
			err = errors.Join(err, probe.Close())
			probe = nil
		}
	}()
	identity, err := readWindowsVersionIdentity(source)
	if err != nil {
		return nil, err
	}
	selected := source
	if identity.elevated {
		selected, err = source.GetLinkedToken()
		if err != nil {
			return nil, fmt.Errorf("get non-elevated version probe token: %w", err)
		}
		defer func() { err = errors.Join(err, selected.Close()) }()
	}
	var primary windows.Token
	if err = windows.DuplicateTokenEx(selected, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY, nil, windows.SecurityImpersonation, windows.TokenPrimary, &primary); err != nil {
		return nil, fmt.Errorf("duplicate version probe token: %w", err)
	}
	probe = &WindowsUserVersionProbe{token: primary, user: identity.user, session: identity.session}
	if err = probe.validate(); err != nil {
		return probe, err
	}
	return probe, nil
}

// Observe checks the complete path under the held user's non-admin trust policy.
func (p *WindowsUserVersionProbe) Observe(ctx context.Context, path string) (ReplacementFile, error) {
	if err := p.validate(); err != nil {
		return ReplacementFile{}, err
	}
	return observeReplacementFile(ctx, path, func(ctx context.Context, path string) (*os.File, string, bool, func() error, func() error, error) {
		return openReplacementWindowsFile(ctx, path, false, p.user, true)
	})
}

// Run executes only the fixed version command; the caller owns its context,
// bounded pipes and isolated environment. Go inherits only the standard handles
// through its explicit handle list, never the updater's token or other handles.
func (p *WindowsUserVersionProbe) Run(cmd *exec.Cmd) error {
	if err := p.validate(); err != nil {
		return err
	}
	if cmd == nil || len(cmd.Args) != 4 || !slices.Equal(cmd.Args[1:], []string{"self", "version", "--json"}) || cmd.Env == nil || cmd.SysProcAttr != nil {
		return os.ErrInvalid
	}
	// Go uses CreateProcessAsUser for this token and owns process termination
	// and Wait. A start failure is returned; there is no elevated retry.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW, Token: syscall.Token(p.token)}
	return cmd.Run()
}

// Close releases the token after the version child has been reaped.
func (p *WindowsUserVersionProbe) Close() error {
	if p == nil || p.token == 0 {
		return nil
	}
	token := p.token
	p.token = 0
	return token.Close()
}

func (p *WindowsUserVersionProbe) validate() error {
	if p == nil || p.token == 0 {
		return os.ErrClosed
	}
	identity, err := readWindowsVersionIdentity(p.token)
	if err != nil {
		return err
	}
	return validateWindowsVersionIdentity(identity, p.user, p.session)
}

type windowsVersionIdentity struct {
	user               *windows.SID
	session, integrity uint32
	elevated, admin    bool
	uiAccess           bool
}

func validateWindowsVersionIdentity(id windowsVersionIdentity, user *windows.SID, session uint32) error {
	if user == nil || id.user == nil || !user.Equals(id.user) || id.session != session || id.elevated || id.admin || id.uiAccess || id.integrity > 0x2000 {
		return fmt.Errorf("version probe requires a same-user non-admin token: %w", os.ErrPermission)
	}
	for _, kind := range []windows.WELL_KNOWN_SID_TYPE{windows.WinLocalSystemSid, windows.WinLocalServiceSid, windows.WinNetworkServiceSid} {
		if user.IsWellKnown(kind) {
			return fmt.Errorf("service identity cannot probe a user-writable binary: %w", os.ErrPermission)
		}
	}
	return nil
}

func readWindowsVersionIdentity(token windows.Token) (id windowsVersionIdentity, err error) {
	user, err := token.GetTokenUser()
	if err != nil {
		return id, fmt.Errorf("read version probe user: %w", err)
	}
	id.user = user.User.Sid
	read := func(class uint32) (uint32, error) {
		var value, size uint32
		err := windows.GetTokenInformation(token, class, (*byte)(unsafe.Pointer(&value)), uint32(unsafe.Sizeof(value)), &size)
		if err == nil && size != uint32(unsafe.Sizeof(value)) {
			err = os.ErrInvalid
		}
		return value, err
	}
	elevated, err := read(windows.TokenElevation)
	if err != nil {
		return id, fmt.Errorf("read version probe elevation: %w", err)
	}
	id.elevated = elevated != 0
	uiAccess, err := read(windows.TokenUIAccess)
	if err != nil {
		return id, fmt.Errorf("read version probe UI access: %w", err)
	}
	id.uiAccess = uiAccess != 0
	id.session, err = read(windows.TokenSessionId)
	if err != nil {
		return id, fmt.Errorf("read version probe session: %w", err)
	}
	var label struct {
		windows.Tokenmandatorylabel
		sid [68]byte // SECURITY_MAX_SID_SIZE: header plus 15 subauthorities.
	}
	var size uint32
	if err = windows.GetTokenInformation(token, windows.TokenIntegrityLevel, (*byte)(unsafe.Pointer(&label)), uint32(unsafe.Sizeof(label)), &size); err != nil {
		return id, fmt.Errorf("read version probe integrity: %w", err)
	}
	if label.Label.Sid == nil || !label.Label.Sid.IsValid() {
		return id, os.ErrInvalid
	}
	// Compare well-known labels without asking a syscall to return an interior
	// pointer into this Go-owned buffer (which violates checkptr's provenance).
	// Unknown labels, including medium-plus, remain above our execution ceiling.
	id.integrity = 0xffffffff
	for _, level := range []struct {
		kind windows.WELL_KNOWN_SID_TYPE
		rid  uint32
	}{{windows.WinUntrustedLabelSid, 0}, {windows.WinLowLabelSid, 0x1000}, {windows.WinMediumLabelSid, 0x2000}} {
		if label.Label.Sid.IsWellKnown(level.kind) {
			id.integrity = level.rid
			break
		}
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		return id, fmt.Errorf("read version probe groups: %w", err)
	}
	for _, group := range groups.AllGroups() {
		if group.Sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) && group.Attributes&windows.SE_GROUP_ENABLED != 0 {
			id.admin = true
		}
	}
	return id, nil
}
