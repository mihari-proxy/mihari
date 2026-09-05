//go:build windows

package platform

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	privateShare                 = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE
	dirAccess                    = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.FILE_GENERIC_EXECUTE | windows.WRITE_DAC
	fileRenameInformationExClass = 65
	privateFileAllAccess         = 0x001f01ff // FILE_ALL_ACCESS (WinNT.h).
)

var processIsLocalSystem = currentProcessIsLocalSystem

var privateRootSecurity = func(h windows.Handle) (*windows.SECURITY_DESCRIPTOR, error) {
	return windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
}
var privateHardenHandle = hardenHandle

type privateFSState struct {
	root  windows.Handle
	dirs  map[string]windows.Handle
	owner *windows.SID
}

type fileIdentity struct {
	volume uint64
	fileID [16]byte
}

type dirIdentityState struct {
	handle windows.Handle
	id     fileIdentity
}

type fileIDInfo struct {
	VolumeSerialNumber uint64
	FileID             [16]byte
}

type fileAttributeTagInfo struct {
	FileAttributes uint32
	ReparseTag     uint32
}

type fileRenameInformationEx struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32
	FileName       [1]uint16
}

func (fs *PrivateFS) openRoot() error {
	h, err := openNTPath(fs.root, dirAccess, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT, windows.FILE_ATTRIBUTE_DIRECTORY, nil)
	if isWindowsNotFound(err) {
		privileged, perr := processIsLocalSystem()
		if perr != nil {
			return fmt.Errorf("query LocalSystem membership: %w", perr)
		}
		if privileged {
			return fmt.Errorf("private fs: refuse to create data root as LocalSystem")
		}
		if err := createDataRoot(fs.root); err != nil && !isWindowsExist(err) {
			return fmt.Errorf("create private data root: %w", err)
		}
		h, err = openNTPath(fs.root, dirAccess, windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT, windows.FILE_ATTRIBUTE_DIRECTORY, nil)
	}
	if err != nil {
		return fmt.Errorf("open private data root: %w", err)
	}
	attr, err := handleAttributes(h)
	if err != nil {
		_ = windows.CloseHandle(h)
		return fmt.Errorf("stat private data root: %w", err)
	}
	if attr&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		_ = windows.CloseHandle(h)
		return fmt.Errorf("private data root is not a directory")
	}
	if err := fs.loadOwner(h); err != nil {
		_ = windows.CloseHandle(h)
		return err
	}
	// A SYSTEM process that cannot identify the individual user must not
	// overwrite a concurrent interactive repair with its legacy BA fallback.
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		_ = windows.CloseHandle(h)
		return err
	}
	if !fs.plat.owner.Equals(admins) {
		if err := hardenHandle(h, fs.dirSDDL()); err != nil {
			_ = windows.CloseHandle(h)
			return fmt.Errorf("harden private data root: %w", err)
		}
	}
	fs.plat.root = h
	fs.plat.dirs = make(map[string]windows.Handle)
	return nil
}

func (fs *PrivateFS) closePlatform() error {
	var errs []error
	fs.dirsMu.Lock()
	for name, h := range fs.plat.dirs {
		if h != 0 {
			errs = append(errs, windows.CloseHandle(h))
		}
		delete(fs.plat.dirs, name)
	}
	fs.dirsMu.Unlock()
	if fs.plat.root != 0 {
		errs = append(errs, windows.CloseHandle(fs.plat.root))
		fs.plat.root = 0
	}
	return errors.Join(errs...)
}

func (fs *PrivateFS) ensureDirLocked(name string) error {
	sd, err := fs.privateDescriptor(true)
	if err != nil {
		return err
	}
	h, err := openRelative(fs.plat.root, name, dirAccess, windows.FILE_OPEN_IF,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		windows.FILE_ATTRIBUTE_DIRECTORY, sd)
	if err != nil {
		return fmt.Errorf("open dir %s: %w", name, err)
	}
	if err := rejectReparse(h, name); err != nil {
		_ = windows.CloseHandle(h)
		return err
	}
	if err := fs.hardenPrivateHandle(h, true); err != nil {
		_ = windows.CloseHandle(h)
		return fmt.Errorf("harden dir %s: %w", name, err)
	}
	fs.dirsMu.Lock()
	if old, ok := fs.plat.dirs[name]; ok {
		fs.dirsMu.Unlock()
		_ = windows.CloseHandle(h)
		_ = old
		return nil
	}
	fs.plat.dirs[name] = h
	fs.dirsMu.Unlock()
	return nil
}

func (fs *PrivateFS) dirHandle(name string) (windows.Handle, error) {
	fs.dirsMu.Lock()
	if h, ok := fs.plat.dirs[name]; ok {
		fs.dirsMu.Unlock()
		if err := fs.hardenPrivateHandle(h, true); err != nil {
			return 0, err
		}
		return h, nil
	}
	fs.dirsMu.Unlock()
	h, err := openRelative(fs.plat.root, name, dirAccess, windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		windows.FILE_ATTRIBUTE_DIRECTORY, nil)
	if err != nil {
		return 0, fmt.Errorf("open dir %s: %w", name, err)
	}
	if err := rejectReparse(h, name); err != nil {
		_ = windows.CloseHandle(h)
		return 0, err
	}
	fs.dirsMu.Lock()
	if old, ok := fs.plat.dirs[name]; ok {
		fs.dirsMu.Unlock()
		_ = windows.CloseHandle(h)
		if err := fs.hardenPrivateHandle(old, true); err != nil {
			return 0, err
		}
		return old, nil
	}
	fs.plat.dirs[name] = h
	fs.dirsMu.Unlock()
	if err := fs.hardenPrivateHandle(h, true); err != nil {
		return 0, err
	}
	return h, nil
}

func (fs *PrivateFS) openAppendLocked(dir, name string) (*os.File, error) {
	parent, err := fs.dirHandle(dir)
	if err != nil {
		return nil, err
	}
	sd, err := fs.privateDescriptor(false)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.FILE_APPEND_DATA | windows.FILE_GENERIC_READ | windows.WRITE_DAC | windows.SYNCHRONIZE | windows.FILE_READ_ATTRIBUTES | windows.FILE_WRITE_ATTRIBUTES)
	h, err := openRelative(parent, name, access, windows.FILE_OPEN_IF,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		windows.FILE_ATTRIBUTE_NORMAL, sd)
	if err != nil {
		return nil, fmt.Errorf("open append %s: %w", name, err)
	}
	if err := rejectReparse(h, name); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	if err := fs.hardenPrivateHandle(h, false); err != nil {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("harden %s: %w", name, err)
	}
	return os.NewFile(uintptr(h), name), nil
}

func (fs *PrivateFS) openReadCheckedLocked(dir, name string, expected FileIdentity) (*os.File, error) {
	parent, err := fs.dirHandle(dir)
	if err != nil {
		return nil, err
	}
	h, err := openRelative(parent, name, windows.FILE_GENERIC_READ, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0, nil)
	if err != nil {
		return nil, fmt.Errorf("open read %s: %w", name, err)
	}
	if err := rejectReparse(h, name); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	id, err := identityFromHandle(h)
	if err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	got := FileIdentity{plat: id}
	if got != expected {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("%w", ErrIdentityMismatch)
	}
	return os.NewFile(uintptr(h), name), nil
}

func (fs *PrivateFS) repairAccessCheckedLocked(dir, name string, expected FileIdentity) (err error) {
	parent, err := fs.dirHandle(dir)
	if err != nil {
		return err
	}
	h, err := openRelative(parent, name, windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.WRITE_DAC|windows.SYNCHRONIZE, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, nil)
	if err != nil {
		if isWindowsNotFound(err) {
			return fmt.Errorf("open file for access repair: %w", os.ErrNotExist)
		}
		return fmt.Errorf("open file for access repair: %w", err)
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(h)) }()
	if err := rejectReparse(h, name); err != nil {
		return err
	}
	id, err := identityFromHandle(h)
	if err != nil {
		return err
	}
	if (FileIdentity{plat: id}) != expected {
		return ErrIdentityMismatch
	}
	return fs.hardenPrivateHandle(h, false)
}

func (fs *PrivateFS) createTempLocked(dir, pattern string) (*os.File, string, error) {
	parent, err := fs.dirHandle(dir)
	if err != nil {
		return nil, "", err
	}
	sd, err := fs.privateDescriptor(false)
	if err != nil {
		return nil, "", err
	}
	access := uint32(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.DELETE | windows.WRITE_DAC)
	for i := 0; i < 100; i++ {
		name, err := randomTempName(pattern)
		if err != nil {
			return nil, "", err
		}
		h, err := openRelative(parent, name, access, windows.FILE_CREATE,
			windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
			windows.FILE_ATTRIBUTE_NORMAL, sd)
		if err != nil {
			if isWindowsExist(err) {
				continue
			}
			return nil, "", fmt.Errorf("create temp %s: %w", name, err)
		}
		if err := rejectReparse(h, name); err != nil {
			_ = windows.CloseHandle(h)
			return nil, "", err
		}
		if err := fs.hardenPrivateHandle(h, false); err != nil {
			_ = windows.CloseHandle(h)
			_ = fs.removeLocked(dir, name)
			return nil, "", err
		}
		return os.NewFile(uintptr(h), name), name, nil
	}
	return nil, "", fmt.Errorf("create temp: exhausted names")
}

func canonicalChildDir(name string) (string, bool) {
	switch {
	case strings.EqualFold(name, privateLogDirName):
		return privateLogDirName, true
	case strings.EqualFold(name, privateExportDirName):
		return privateExportDirName, true
	}
	return "", false
}

func (fs *PrivateFS) readDirLocked(dir string) ([]FileEntry, error) {
	parent, err := fs.dirHandle(dir)
	if err != nil {
		return nil, err
	}
	list, err := openListingHandle(parent)
	if err != nil {
		return nil, fmt.Errorf("open dir listing %s: %w", dir, err)
	}
	defer windows.CloseHandle(list)
	ents, err := readWindowsDirents(list)
	if err != nil {
		return nil, err
	}
	entries := make([]FileEntry, 0, len(ents))
	for _, ent := range ents {
		h, err := openRelative(parent, ent.name, windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, windows.FILE_OPEN,
			windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, nil)
		if err != nil {
			if isWindowsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("stat %s: %w", ent.name, err)
		}
		id, err := identityFromHandle(h)
		attr, attrErr := handleAttributes(h)
		_ = windows.CloseHandle(h)
		if err != nil {
			return nil, err
		}
		if attrErr != nil {
			return nil, attrErr
		}
		entries = append(entries, FileEntry{
			Name:     ent.name,
			Mode:     modeFromAttributes(attr),
			Identity: FileIdentity{plat: id},
		})
	}
	return entries, nil
}

func (fs *PrivateFS) openDirIdentityLocked(name string) (*DirectoryIdentity, error) {
	h, err := fs.dirHandle(name)
	if err != nil {
		return nil, err
	}
	dup, err := dupHandle(h)
	if err != nil {
		return nil, err
	}
	id, err := identityFromHandle(dup)
	if err != nil {
		_ = windows.CloseHandle(dup)
		return nil, err
	}
	return &DirectoryIdentity{plat: dirIdentityState{handle: dup, id: id}}, nil
}

func (fs *PrivateFS) openPublishDirLocked(name string) (*PublishDir, error) {
	h, err := fs.dirHandle(name)
	if err != nil {
		if isWindowsNotFound(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	dup, err := dupHandle(h)
	if err != nil {
		return nil, err
	}
	d, err := publishDirFromHandle(dup)
	if err != nil {
		_ = windows.CloseHandle(dup)
		return nil, err
	}
	return d, nil
}

func (fs *PrivateFS) renameLocked(dir, oldName, newName string, replace bool) error {
	parent, err := fs.dirHandle(dir)
	if err != nil {
		return err
	}
	h, err := openRelative(parent, oldName, windows.DELETE|windows.SYNCHRONIZE|windows.FILE_READ_ATTRIBUTES, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, nil)
	if err != nil {
		return fmt.Errorf("rename %s: %w", oldName, err)
	}
	defer windows.CloseHandle(h)
	if err := rejectReparse(h, oldName); err != nil {
		return err
	}
	flags := uint32(windows.FILE_RENAME_POSIX_SEMANTICS)
	if replace {
		flags |= windows.FILE_RENAME_REPLACE_IF_EXISTS
	}
	if err := renameHandle(h, parent, newName, flags); err != nil {
		return fmt.Errorf("rename %s: %w", oldName, err)
	}
	return nil
}

func (fs *PrivateFS) removeLocked(dir, name string) error {
	parent, err := fs.dirHandle(dir)
	if err != nil {
		return err
	}
	h, err := openRelative(parent, name, windows.DELETE|windows.SYNCHRONIZE|windows.FILE_READ_ATTRIBUTES, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, nil)
	if err != nil {
		return fmt.Errorf("remove %s: %w", name, err)
	}
	defer windows.CloseHandle(h)
	if err := rejectReparse(h, name); err != nil {
		return err
	}
	info := uint32(windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_POSIX_SEMANTICS)
	var iosb windows.IO_STATUS_BLOCK
	if err := windows.NtSetInformationFile(h, &iosb, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), windows.FileDispositionInformationEx); err != nil {
		return fmt.Errorf("remove %s: %w", name, err)
	}
	return nil
}

// Close releases the duplicated directory handle. Repeat calls return nil.
func (d *DirectoryIdentity) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	if d.plat.handle != 0 {
		err := windows.CloseHandle(d.plat.handle)
		d.plat.handle = 0
		return err
	}
	return nil
}

func (d *DirectoryIdentity) identity() (FileIdentity, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return FileIdentity{}, errPrivateFSClosed
	}
	return FileIdentity{plat: d.plat.id}, nil
}

func (fs *PrivateFS) loadOwner(h windows.Handle) error {
	sd, err := privateRootSecurity(h)
	if err != nil {
		return fmt.Errorf("query data root owner: %w", err)
	}
	owner, err := privateDataPrincipal(sd)
	if err != nil || owner == nil {
		return fmt.Errorf("query data root owner: %w", err)
	}
	copied, err := owner.Copy()
	if err != nil {
		return err
	}
	fs.plat.owner = copied
	return nil
}

func privateDataPrincipal(sd *windows.SECURITY_DESCRIPTOR) (*windows.SID, error) {
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return nil, fmt.Errorf("read private data owner: %w", err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, err
	}
	if !owner.Equals(admins) {
		return owner.Copy()
	}
	// Elevated processes can create a directory owned by Administrators rather
	// than their user SID. Recover the individual principal from its existing
	// explicit full-control ACE, so a later LocalSystem service chooses the same
	// user. Never carry broad group ACEs into private log files.
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return nil, fmt.Errorf("private data root requires a DACL")
	}
	var principal *windows.SID
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return nil, err
		}
		// Never flatten object/callback or deny restrictions into an unconditional grant.
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return nil, fmt.Errorf("unsupported private data root ACE type %d", ace.Header.AceType)
		}
		if ace.Header.AceFlags&(windows.INHERITED_ACE|windows.INHERIT_ONLY_ACE) != 0 || ace.Mask&privateFileAllAccess != privateFileAllAccess {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		_, _, kind, lookupErr := sid.LookupAccount("")
		if lookupErr != nil {
			return nil, fmt.Errorf("resolve private data principal: %w", lookupErr)
		}
		if kind != windows.SidTypeUser {
			continue
		}
		if principal != nil && !principal.Equals(sid) {
			return nil, fmt.Errorf("ambiguous private data root users")
		}
		principal, err = sid.Copy()
		if err != nil {
			return nil, err
		}
	}
	if principal != nil {
		return principal, nil
	}
	// Legacy versions may already have removed the user ACE. An interactive
	// process that successfully opened the root with WRITE_DAC may repair it.
	// LocalSystem cannot infer an interactive user and keeps the existing owner.
	isSystem, err := processIsLocalSystem()
	if err != nil {
		return nil, err
	}
	if isSystem {
		if !legacyAdminRootDACL(sd, dacl, admins) {
			return nil, fmt.Errorf("unresolved private data root has unsafe permissions")
		}
		return owner.Copy()
	}
	return currentUserSID()
}

// Only the exact protected legacy trust boundary can be left untouched.
func legacyAdminRootDACL(sd *windows.SECURITY_DESCRIPTOR, dacl *windows.ACL, admins *windows.SID) bool {
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	var sawAdmin, sawSystem bool
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(dacl, i, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return false
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		switch {
		case sid.Equals(admins):
			sawAdmin = true
		case sid.String() == "S-1-5-18":
			sawSystem = true
		default:
			return false
		}
	}
	return sawAdmin && sawSystem
}

func (fs *PrivateFS) currentPrivateSDDL(directory bool) (string, error) {
	sd, err := privateRootSecurity(fs.plat.root)
	if err != nil {
		return "", fmt.Errorf("read current private root policy: %w", err)
	}
	principal, err := privateDataPrincipal(sd)
	if err != nil {
		return "", err
	}
	return principalSDDL(principal, directory), nil
}

func (fs *PrivateFS) privateDescriptor(directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
	sddl, err := fs.currentPrivateSDDL(directory)
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString(sddl)
}

func (fs *PrivateFS) hardenPrivateHandle(h windows.Handle, directory bool) error {
	// Recheck after applying: a service can have sampled BA immediately before
	// interactive startup grants the user. Repair that same held object again.
	for attempt := 0; attempt < 3; attempt++ {
		before, err := fs.currentPrivateSDDL(directory)
		if err != nil {
			return err
		}
		if err := privateHardenHandle(h, before); err != nil {
			return err
		}
		after, err := fs.currentPrivateSDDL(directory)
		if err != nil {
			return err
		}
		if before == after {
			return nil
		}
	}
	return fmt.Errorf("private root policy changed repeatedly during access repair")
}

func principalSDDL(principal *windows.SID, directory bool) string {
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	// Duplicating SYSTEM here is harmless but avoid it for the canonical policy.
	if principal.String() == "S-1-5-18" {
		return "D:P(A;" + inherit + ";FA;;;SY)"
	}
	return "D:P(A;" + inherit + ";FA;;;" + principal.String() + ")(A;" + inherit + ";FA;;;SY)"
}

func (fs *PrivateFS) dirSDDL() string { return principalSDDL(fs.plat.owner, true) }

func currentProcessIsLocalSystem() (bool, error) {
	sid, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false, err
	}
	return windows.GetCurrentProcessToken().IsMember(sid)
}

func currentUserSID() (*windows.SID, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return tu.User.Sid.Copy()
}

func createDataRoot(path string) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	sddl := "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)"
	if sid.Equals(system) {
		sddl = "D:P(A;OICI;FA;;;SY)"
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	sa := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
		InheritHandle:      0,
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	err = windows.CreateDirectory(p, &sa)
	runtime.KeepAlive(sd)
	return err
}

func hardenHandle(h windows.Handle, sddl string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	err = windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	runtime.KeepAlive(sd)
	return err
}

func openNTPath(path string, access, disposition, options, attr uint32, sd *windows.SECURITY_DESCRIPTOR) (windows.Handle, error) {
	nt, err := toNTPath(path)
	if err != nil {
		return 0, err
	}
	name, err := windows.NewNTUnicodeString(nt)
	if err != nil {
		return 0, err
	}
	oa := windows.OBJECT_ATTRIBUTES{
		ObjectName:         name,
		Attributes:         windows.OBJ_CASE_INSENSITIVE,
		SecurityDescriptor: sd,
	}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, access, &oa, &iosb, nil, attr, privateShare, disposition, options, 0, 0)
	runtime.KeepAlive(name)
	runtime.KeepAlive(sd)
	if err != nil {
		return 0, err
	}
	_ = windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, 0)
	return h, nil
}

func openListingHandle(parent windows.Handle) (windows.Handle, error) {
	// NT rejects "." as an object name. An empty name with RootDirectory
	// reopens the same directory as a new FILE_OBJECT (independent enumeration).
	var empty windows.NTUnicodeString
	access := uint32(windows.FILE_LIST_DIRECTORY | windows.SYNCHRONIZE | windows.FILE_READ_ATTRIBUTES)
	oa := windows.OBJECT_ATTRIBUTES{
		RootDirectory: parent,
		ObjectName:    &empty,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err := windows.NtCreateFile(&h, access, &oa, &iosb, nil, windows.FILE_ATTRIBUTE_DIRECTORY, privateShare, windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		return 0, err
	}
	_ = windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, 0)
	return h, nil
}

func openRelative(parent windows.Handle, name string, access, disposition, options, attr uint32, sd *windows.SECURITY_DESCRIPTOR) (windows.Handle, error) {
	return openRelativeWithShare(parent, name, access, disposition, options, attr, sd, privateShare)
}

func openRelativeWithShare(parent windows.Handle, name string, access, disposition, options, attr uint32, sd *windows.SECURITY_DESCRIPTOR, share uint32) (windows.Handle, error) {
	ntName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	oa := windows.OBJECT_ATTRIBUTES{
		RootDirectory:      parent,
		ObjectName:         ntName,
		Attributes:         windows.OBJ_CASE_INSENSITIVE,
		SecurityDescriptor: sd,
	}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var h windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&h, access, &oa, &iosb, nil, attr, share, disposition, options, 0, 0)
	runtime.KeepAlive(ntName)
	runtime.KeepAlive(sd)
	if err != nil {
		return 0, err
	}
	_ = windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, 0)
	return h, nil
}

func toNTPath(path string) (string, error) {
	p := path
	if strings.HasPrefix(p, `\\?\UNC\`) {
		return `\??\UNC\` + p[len(`\\?\UNC\`):], nil
	}
	if strings.HasPrefix(p, `\\?\`) {
		return `\??\` + p[4:], nil
	}
	if strings.HasPrefix(p, `\\`) {
		return `\??\UNC\` + strings.TrimPrefix(p, `\\`), nil
	}
	return `\??\` + p, nil
}

func rejectReparse(h windows.Handle, name string) error {
	attr, err := handleAttributes(h)
	if err != nil {
		return err
	}
	if attr&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("%s: reparse point or junction is not allowed", name)
	}
	return nil
}

func handleAttributes(h windows.Handle) (uint32, error) {
	var info fileAttributeTagInfo
	if err := windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err == nil {
		return info.FileAttributes, nil
	}
	var bh windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &bh); err != nil {
		return 0, err
	}
	return bh.FileAttributes, nil
}

func identityFromHandle(h windows.Handle) (fileIdentity, error) {
	var info fileIDInfo
	if err := windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return fileIdentity{}, err
	}
	return fileIdentity{volume: info.VolumeSerialNumber, fileID: info.FileID}, nil
}

func dupHandle(h windows.Handle) (windows.Handle, error) {
	var out windows.Handle
	err := windows.DuplicateHandle(windows.CurrentProcess(), h, windows.CurrentProcess(), &out, 0, false, windows.DUPLICATE_SAME_ACCESS)
	if err != nil {
		return 0, err
	}
	_ = windows.SetHandleInformation(out, windows.HANDLE_FLAG_INHERIT, 0)
	return out, nil
}

func renameHandle(h, parent windows.Handle, newName string, flags uint32) error {
	u16, err := windows.UTF16FromString(newName)
	if err != nil {
		return err
	}
	u16 = u16[:len(u16)-1]
	nameBytes := len(u16) * 2
	var dummy fileRenameInformationEx
	bufSize := int(unsafe.Offsetof(dummy.FileName)) + nameBytes
	buf := make([]byte, bufSize)
	info := (*fileRenameInformationEx)(unsafe.Pointer(&buf[0]))
	info.Flags = flags
	info.RootDirectory = parent
	info.FileNameLength = uint32(nameBytes)
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(&info.FileName[0])), len(u16)), u16)
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtSetInformationFile(h, &iosb, &buf[0], uint32(bufSize), fileRenameInformationExClass)
	runtime.KeepAlive(buf)
	return err
}

type windowsDirent struct {
	name string
	attr uint32
}

func readWindowsDirents(h windows.Handle) ([]windowsDirent, error) {
	buf := make([]byte, 64*1024)
	var ents []windowsDirent
	first := true
	for {
		class := uint32(windows.FileFullDirectoryInfo)
		if first {
			class = windows.FileFullDirectoryRestartInfo
			first = false
		}
		err := windows.GetFileInformationByHandleEx(h, class, &buf[0], uint32(len(buf)))
		if err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) || errors.Is(err, windows.ERROR_HANDLE_EOF) {
				break
			}
			if errors.Is(err, windows.ERROR_MORE_DATA) {
				buf = make([]byte, len(buf)*2)
				first = true
				ents = nil
				continue
			}
			return nil, err
		}
		ents = append(ents, parseFullDirInfo(buf)...)
	}
	return ents, nil
}

func parseFullDirInfo(buf []byte) []windowsDirent {
	const nameOffset = 68
	var ents []windowsDirent
	off := 0
	for off+nameOffset <= len(buf) {
		next := binary.LittleEndian.Uint32(buf[off:])
		attr := binary.LittleEndian.Uint32(buf[off+56:])
		nameLen := int(binary.LittleEndian.Uint32(buf[off+60:]))
		nameOff := off + nameOffset
		if nameLen < 0 || nameOff+nameLen > len(buf) {
			break
		}
		n := nameLen / 2
		u16 := unsafe.Slice((*uint16)(unsafe.Pointer(&buf[nameOff])), n)
		name := windows.UTF16ToString(u16)
		if name != "." && name != ".." && name != "" {
			ents = append(ents, windowsDirent{name: name, attr: attr})
		}
		if next == 0 {
			break
		}
		off += int(next)
	}
	return ents
}

func modeFromAttributes(attr uint32) os.FileMode {
	mode := os.FileMode(0o666)
	if attr&windows.FILE_ATTRIBUTE_READONLY != 0 {
		mode = 0o444
	}
	if attr&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		mode |= os.ModeDir
	}
	if attr&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		mode |= os.ModeSymlink
	}
	return mode
}

func isWindowsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		return true
	}
	var st windows.NTStatus
	if errors.As(err, &st) {
		switch st {
		case windows.STATUS_OBJECT_NAME_NOT_FOUND, windows.STATUS_OBJECT_PATH_NOT_FOUND:
			return true
		}
		switch st.Errno() {
		case windows.ERROR_FILE_NOT_FOUND, windows.ERROR_PATH_NOT_FOUND:
			return true
		}
	}
	return false
}

func isWindowsExist(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrExist) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
		return true
	}
	var st windows.NTStatus
	if errors.As(err, &st) {
		if st == windows.STATUS_OBJECT_NAME_COLLISION {
			return true
		}
		switch st.Errno() {
		case windows.ERROR_ALREADY_EXISTS, windows.ERROR_FILE_EXISTS:
			return true
		}
	}
	return false
}
