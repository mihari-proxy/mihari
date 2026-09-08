//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	installControlFileDeleteChild  = 0x0040 // FILE_DELETE_CHILD (WinNT.h).
	installControlWindowsDirAccess = windows.FILE_LIST_DIRECTORY | windows.FILE_TRAVERSE |
		windows.FILE_READ_ATTRIBUTES | fileAddFile | fileAddSubdirectory | installControlFileDeleteChild |
		windows.READ_CONTROL | windows.WRITE_DAC | windows.WRITE_OWNER | windows.SYNCHRONIZE
	installControlWindowsFileAccess = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE |
		windows.FILE_READ_ATTRIBUTES | windows.WRITE_DAC | windows.WRITE_OWNER | windows.DELETE | windows.SYNCHRONIZE
)

type installControlWindowsDeps struct {
	knownFolderPath func() (string, error)
	descriptor      func(bool) (*windows.SECURITY_DESCRIPTOR, error)
	checkSecurity   func(windows.Handle, bool) error
	hardenSecurity  func(windows.Handle, bool) error
	checkVolume     func(windows.Handle) error
	flushFile       func(windows.Handle) error
	syncVolume      func(windows.Handle) error
	rename          func(windows.Handle, windows.Handle, string, uint32) error
}

type windowsInstallControl struct {
	deps                installControlWindowsDeps
	programData, mihari windows.Handle
	root, state         windows.Handle
	programDataPath     string
	programDataID       fileIdentity
	mihariID, rootID    fileIdentity
	stateID             fileIdentity
}

type windowsInstallControlLock struct {
	mu         sync.Mutex
	deps       installControlWindowsDeps
	file       windows.Handle
	parent     windows.Handle
	name       string
	fileID     fileIdentity
	parentID   fileIdentity
	held       bool
	closed     bool
	overlapped windows.Overlapped
}

// OpenInstallControlReadOnly opens the fixed ProgramData control directory
// without creating or repairing any object.
func OpenInstallControlReadOnly(ctx context.Context) (*InstallControl, error) {
	return openWindowsInstallControl(ctx, true, defaultInstallControlWindowsDeps())
}

// OpenInstallControlForUpdate opens the fixed ProgramData control directory
// for a caller that will explicitly take operation.lock before mutation.
func OpenInstallControlForUpdate(ctx context.Context) (*InstallControl, error) {
	return openWindowsInstallControl(ctx, false, defaultInstallControlWindowsDeps())
}

// InitializeInstallControlForUpdate atomically publishes a fully initialized
// control directory when it is absent. A racing loser opens the winner's K.
func InitializeInstallControlForUpdate(ctx context.Context, initial []byte) (*InstallControl, InstallPublication, error) {
	return initializeWindowsInstallControl(ctx, initial, defaultInstallControlWindowsDeps())
}

func defaultInstallControlWindowsDeps() installControlWindowsDeps {
	return installControlWindowsDeps{
		knownFolderPath: func() (string, error) {
			return windows.KnownFolderPath(windows.FOLDERID_ProgramData, windows.KF_FLAG_DEFAULT)
		},
		descriptor: func(directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
			return windows.SecurityDescriptorFromString(installControlWindowsSDDL(directory))
		},
		checkSecurity:  checkInstallControlWindowsSecurity,
		hardenSecurity: hardenInstallControlWindowsSecurity,
		checkVolume:    checkInstallControlWindowsVolume,
		flushFile:      windows.FlushFileBuffers,
		syncVolume:     syncInstallControlWindowsVolume,
		rename:         renameHandle,
	}
}

func openWindowsInstallControl(ctx context.Context, readOnly bool, deps installControlWindowsDeps) (_ *InstallControl, err error) {
	if err = validateInstallControlWindowsDeps(deps); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	programDataPath, err := deps.knownFolderPath()
	if err != nil {
		return nil, fmt.Errorf("resolve ProgramData known folder: %w", err)
	}
	if !filepath.IsAbs(programDataPath) || filepath.Clean(programDataPath) != programDataPath {
		return nil, fmt.Errorf("invalid ProgramData known folder: %w", os.ErrInvalid)
	}
	p := &windowsInstallControl{deps: deps, programDataPath: programDataPath}
	defer func() {
		if err != nil {
			err = errors.Join(err, p.close())
		}
	}()
	p.programData, err = openInstallControlWindowsDirectoryPath(programDataPath)
	if err != nil {
		return nil, fmt.Errorf("open ProgramData known folder: %w", err)
	}
	if err = rejectReparse(p.programData, "ProgramData"); err != nil {
		return nil, err
	}
	p.programDataID, err = identityFromHandle(p.programData)
	if err != nil {
		return nil, fmt.Errorf("identify ProgramData known folder: %w", err)
	}
	p.mihari, err = openInstallControlWindowsDirectory(p.programData, "Mihari")
	if isWindowsNotFound(err) {
		return nil, fmt.Errorf("open Mihari installation directory: %w", os.ErrNotExist)
	}
	if err != nil {
		return nil, fmt.Errorf("open Mihari installation directory: %w", err)
	}
	if err = p.checkDirectory(p.programData, "Mihari", p.mihari, &p.mihariID); err != nil {
		return nil, err
	}
	p.root, err = openInstallControlWindowsDirectory(p.mihari, installControlDirName)
	if isWindowsNotFound(err) {
		return nil, fmt.Errorf("open installation control: %w", os.ErrNotExist)
	}
	if err != nil {
		return nil, fmt.Errorf("open installation control: %w", err)
	}
	if err = p.checkDirectory(p.mihari, installControlDirName, p.root, &p.rootID); err != nil {
		return nil, err
	}
	if !readOnly {
		if err = deps.checkVolume(p.root); err != nil {
			return nil, fmt.Errorf("validate installation control volume: %w", err)
		}
	}
	for _, name := range []string{installOperationLockName, installStartupLockName} {
		h, _, openErr := p.openFixedFile(name)
		if isWindowsNotFound(openErr) {
			return nil, fmt.Errorf("%w: missing %s", ErrInstallControlUninitialized, name)
		}
		if openErr != nil {
			return nil, openErr
		}
		if closeErr := windows.CloseHandle(h); closeErr != nil {
			return nil, fmt.Errorf("close %s validation handle: %w", name, closeErr)
		}
	}
	p.state, p.stateID, err = p.openFixedFile(installStateName)
	if isWindowsNotFound(err) {
		return nil, fmt.Errorf("%w: missing %s", ErrInstallControlUninitialized, installStateName)
	}
	if err != nil {
		return nil, err
	}
	control := newInstallControl(p, readOnly)
	return control, nil
}

func initializeWindowsInstallControl(ctx context.Context, initial []byte, deps installControlWindowsDeps) (_ *InstallControl, publication InstallPublication, err error) {
	if len(initial) > installStateMaxBytes {
		return nil, publication, ErrInstallStateTooLarge
	}
	if err = validateInstallControlWindowsDeps(deps); err != nil {
		return nil, publication, err
	}
	if err = ctx.Err(); err != nil {
		return nil, publication, err
	}
	programDataPath, err := deps.knownFolderPath()
	if err != nil {
		return nil, publication, fmt.Errorf("resolve ProgramData known folder: %w", err)
	}
	if !filepath.IsAbs(programDataPath) || filepath.Clean(programDataPath) != programDataPath {
		return nil, publication, fmt.Errorf("invalid ProgramData known folder: %w", os.ErrInvalid)
	}
	programData, err := openInstallControlWindowsDirectoryPath(programDataPath)
	if err != nil {
		return nil, publication, fmt.Errorf("open ProgramData known folder: %w", err)
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(programData)) }()
	if err = rejectReparse(programData, "ProgramData"); err != nil {
		return nil, publication, err
	}
	if err = deps.checkVolume(programData); err != nil {
		return nil, publication, fmt.Errorf("validate installation control volume: %w", err)
	}
	mihari, err := openOrCreateInstallControlWindowsDirectory(programData, "Mihari", deps)
	if err != nil {
		return nil, publication, err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(mihari)) }()
	if existing, openErr := openInstallControlWindowsDirectory(mihari, installControlDirName); openErr == nil {
		if closeErr := windows.CloseHandle(existing); closeErr != nil {
			return nil, publication, closeErr
		}
		control, openErr := openWindowsInstallControl(ctx, false, deps)
		return control, publication, openErr
	} else if !isWindowsNotFound(openErr) {
		return nil, publication, fmt.Errorf("probe installation control: %w", openErr)
	}
	temp, tempName, createErr := createInstallControlWindowsTempDir(mihari, deps)
	if createErr != nil {
		return nil, publication, createErr
	}
	published := false
	defer func() {
		if !published {
			err = errors.Join(err, cleanupInstallControlWindowsTemp(temp))
		} else {
			err = errors.Join(err, windows.CloseHandle(temp))
		}
	}()
	for _, item := range []struct {
		name string
		body []byte
	}{
		{name: installOperationLockName},
		{name: installStartupLockName},
		{name: installStateName, body: initial},
	} {
		if err = createInstallControlWindowsFile(temp, item.name, item.body, deps); err != nil {
			return nil, publication, err
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, publication, err
	}
	err = deps.rename(temp, mihari, installControlDirName, windows.FILE_RENAME_POSIX_SEMANTICS)
	if isWindowsExist(err) {
		err = nil
		control, openErr := openWindowsInstallControl(ctx, false, deps)
		return control, publication, openErr
	}
	if err != nil {
		return nil, publication, fmt.Errorf("publish installation control directory: %w", err)
	}
	published = true
	publication.Published = true
	if err = deps.syncVolume(temp); err != nil {
		return nil, publication, fmt.Errorf("sync installation control volume after directory publication: %w", err)
	}
	control, err := openWindowsInstallControl(ctx, false, deps)
	if err != nil {
		return nil, publication, err
	}
	state, digest, readErr := control.ReadState(ctx)
	if readErr != nil || digest != installStateSHA256(initial) || string(state) != string(initial) {
		_ = control.Close()
		return nil, publication, errors.Join(ErrInstallStateChanged, readErr)
	}
	publication.Durable = true
	_ = tempName
	return control, publication, nil
}

func validateInstallControlWindowsDeps(deps installControlWindowsDeps) error {
	if deps.knownFolderPath == nil || deps.descriptor == nil || deps.checkSecurity == nil ||
		deps.hardenSecurity == nil || deps.checkVolume == nil || deps.flushFile == nil ||
		deps.syncVolume == nil || deps.rename == nil {
		return os.ErrInvalid
	}
	return nil
}

func (p *windowsInstallControl) readState(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.verifyNamespace(); err != nil {
		return nil, err
	}
	return readInstallControlWindowsFile(p.state)
}

func (p *windowsInstallControl) archiveState(ctx context.Context, expected string) error {
	b, err := p.readMatchingState(ctx, expected)
	if err != nil {
		return err
	}
	_, err = p.publishNamed(ctx, installPreviousStateName, b, nil, "", false)
	return err
}

func (p *windowsInstallControl) publishState(ctx context.Context, previous string, next []byte) (InstallPublication, error) {
	if _, err := p.readMatchingState(ctx, previous); err != nil {
		return InstallPublication{}, err
	}
	id := p.stateID
	return p.publishNamed(ctx, installStateName, next, &id, previous, true)
}

func (p *windowsInstallControl) confirmState(ctx context.Context, expected string) error {
	if _, err := p.readMatchingState(ctx, expected); err != nil {
		return err
	}
	if err := p.deps.flushFile(p.state); err != nil {
		return fmt.Errorf("flush held installation state: %w", err)
	}
	if err := p.deps.syncVolume(p.root); err != nil {
		return fmt.Errorf("sync installation control volume: %w", err)
	}
	_, err := p.readMatchingState(ctx, expected)
	return err
}

func (p *windowsInstallControl) probeOperation(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	l, err := p.acquireLock(installOperationLockName)
	if errors.Is(err, ErrInstallControlBusy) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, l.close()
}

func (p *windowsInstallControl) lockOperation(ctx context.Context) (installControlLockPlatform, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p.acquireLock(installOperationLockName)
}

func (p *windowsInstallControl) lockStartup(ctx context.Context) (installControlLockPlatform, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p.acquireLock(installStartupLockName)
}

func (p *windowsInstallControl) close() error {
	var errs []error
	for _, h := range []*windows.Handle{&p.state, &p.root, &p.mihari, &p.programData} {
		if *h != 0 {
			errs = append(errs, windows.CloseHandle(*h))
			*h = 0
		}
	}
	return errors.Join(errs...)
}

func (p *windowsInstallControl) publishNamed(ctx context.Context, name string, body []byte, expected *fileIdentity, expectedDigest string, retainState bool) (publication InstallPublication, err error) {
	if err = ctx.Err(); err != nil {
		return publication, err
	}
	if err = p.verifyNamespace(); err != nil {
		return publication, err
	}
	var target windows.Handle
	var targetID fileIdentity
	target, targetID, err = p.openFixedFile(name)
	if expected == nil {
		if err == nil {
			// Archive replacement is identity-bound to the object observed now.
			expected = &targetID
		} else if !isWindowsNotFound(err) {
			return publication, err
		}
	} else if err != nil {
		return publication, errors.Join(ErrInstallStateChanged, err)
	} else if targetID != *expected {
		_ = windows.CloseHandle(target)
		return publication, ErrInstallStateChanged
	}
	if target != 0 {
		defer func() { err = errors.Join(err, windows.CloseHandle(target)) }()
	}
	temp, _, tempErr := p.createTempFile(".state-tmp-")
	if tempErr != nil {
		return publication, tempErr
	}
	tempPublished := false
	defer func() {
		if !tempPublished {
			err = errors.Join(err, deleteInstallControlWindowsHandle(temp))
		} else if !retainState {
			err = errors.Join(err, windows.CloseHandle(temp))
		}
	}()
	if err = writeInstallControlWindowsFile(temp, body); err != nil {
		return publication, err
	}
	if err = p.deps.flushFile(temp); err != nil {
		return publication, fmt.Errorf("flush installation state candidate: %w", err)
	}
	if expected != nil {
		check, checkID, checkErr := p.openFixedFile(name)
		if checkErr != nil {
			return publication, errors.Join(ErrInstallStateChanged, checkErr)
		}
		var digestErr error
		if expectedDigest != "" {
			var checkBody []byte
			checkBody, digestErr = readInstallControlWindowsFile(check)
			if digestErr == nil && installStateSHA256(checkBody) != expectedDigest {
				digestErr = ErrInstallStateChanged
			}
		}
		checkCloseErr := windows.CloseHandle(check)
		if checkID != *expected || digestErr != nil || checkCloseErr != nil {
			return publication, errors.Join(ErrInstallStateChanged, digestErr, checkCloseErr)
		}
	}
	flags := uint32(windows.FILE_RENAME_POSIX_SEMANTICS)
	if expected != nil {
		flags |= windows.FILE_RENAME_REPLACE_IF_EXISTS
	}
	newID, idErr := identityFromHandle(temp)
	if idErr != nil {
		return publication, idErr
	}
	if err = p.deps.rename(temp, p.root, name, flags); err != nil {
		if isWindowsExist(err) || isWindowsNotFound(err) {
			return publication, errors.Join(ErrInstallStateChanged, err)
		}
		return publication, fmt.Errorf("replace installation state: %w", err)
	}
	tempPublished = true
	publication.Published = true
	if retainState {
		old := p.state
		p.state = temp
		p.stateID = newID
		temp = 0
		if closeErr := windows.CloseHandle(old); closeErr != nil {
			return publication, fmt.Errorf("close predecessor installation state: %w", closeErr)
		}
	}
	flushTarget := temp
	if retainState {
		flushTarget = p.state
	}
	if err = p.deps.flushFile(flushTarget); err != nil {
		return publication, fmt.Errorf("flush published installation state: %w", err)
	}
	if err = p.deps.syncVolume(p.root); err != nil {
		return publication, fmt.Errorf("sync installation control volume: %w", err)
	}
	check, checkID, checkErr := p.openFixedFile(name)
	if checkErr != nil {
		return publication, errors.Join(ErrInstallStateChanged, checkErr)
	}
	checkBody, readErr := readInstallControlWindowsFile(check)
	closeErr := windows.CloseHandle(check)
	if checkID != newID || installStateSHA256(checkBody) != installStateSHA256(body) {
		return publication, errors.Join(ErrInstallStateChanged, readErr, closeErr)
	}
	if readErr != nil || closeErr != nil {
		return publication, errors.Join(readErr, closeErr)
	}
	publication.Durable = true
	return publication, nil
}

func (p *windowsInstallControl) readMatchingState(ctx context.Context, expected string) ([]byte, error) {
	b, err := p.readState(ctx)
	if err != nil {
		return nil, err
	}
	if installStateSHA256(b) != expected {
		return nil, ErrInstallStateChanged
	}
	return b, nil
}

func (p *windowsInstallControl) verifyNamespace() error {
	check, err := openInstallControlWindowsDirectoryPath(p.programDataPath)
	if err != nil {
		return ErrInstallStateChanged
	}
	id, idErr := identityFromHandle(check)
	closeErr := windows.CloseHandle(check)
	if idErr != nil || closeErr != nil || id != p.programDataID {
		return errors.Join(ErrInstallStateChanged, idErr, closeErr)
	}
	if err = verifyInstallControlWindowsNamedDirectory(p.programData, "Mihari", p.mihariID, p.deps); err != nil {
		return err
	}
	if err = verifyInstallControlWindowsNamedDirectory(p.mihari, installControlDirName, p.rootID, p.deps); err != nil {
		return err
	}
	state, id, err := p.openFixedFile(installStateName)
	if err != nil {
		return errors.Join(ErrInstallStateChanged, err)
	}
	closeErr = windows.CloseHandle(state)
	if id != p.stateID || closeErr != nil {
		return errors.Join(ErrInstallStateChanged, closeErr)
	}
	return nil
}

func (p *windowsInstallControl) checkDirectory(parent windows.Handle, name string, h windows.Handle, out *fileIdentity) error {
	if err := rejectReparse(h, name); err != nil {
		return err
	}
	attr, err := handleAttributes(h)
	if err != nil || attr&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return errors.Join(ErrUnsafeComponent, err)
	}
	if err = p.deps.checkSecurity(h, true); err != nil {
		return fmt.Errorf("check %s security: %w", name, err)
	}
	id, err := identityFromHandle(h)
	if err != nil {
		return err
	}
	if err = verifyInstallControlWindowsNamedDirectory(parent, name, id, p.deps); err != nil {
		return err
	}
	*out = id
	return nil
}

func (p *windowsInstallControl) openFixedFile(name string) (windows.Handle, fileIdentity, error) {
	h, err := openRelative(p.root, name, installControlWindowsFileAccess, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		windows.FILE_ATTRIBUTE_NORMAL, nil)
	if err != nil {
		return 0, fileIdentity{}, err
	}
	id, err := validateInstallControlWindowsFile(h, name, p.deps)
	if err != nil {
		_ = windows.CloseHandle(h)
		return 0, fileIdentity{}, err
	}
	return h, id, nil
}

func (p *windowsInstallControl) createTempFile(prefix string) (windows.Handle, string, error) {
	for i := 0; i < 100; i++ {
		name, err := randomTempName(prefix + "*")
		if err != nil {
			return 0, "", err
		}
		sd, err := p.deps.descriptor(false)
		if err != nil {
			return 0, "", err
		}
		h, err := openRelative(p.root, name, installControlWindowsFileAccess, windows.FILE_CREATE,
			windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
			windows.FILE_ATTRIBUTE_NORMAL, sd)
		runtime.KeepAlive(sd)
		if isWindowsExist(err) {
			continue
		}
		if err != nil {
			return 0, "", err
		}
		if err = p.deps.hardenSecurity(h, false); err != nil {
			return 0, "", errors.Join(err, deleteInstallControlWindowsHandle(h))
		}
		if _, err = validateInstallControlWindowsFile(h, name, p.deps); err != nil {
			return 0, "", errors.Join(err, deleteInstallControlWindowsHandle(h))
		}
		return h, name, nil
	}
	return 0, "", fmt.Errorf("create installation state candidate: exhausted names")
}

func (p *windowsInstallControl) acquireLock(name string) (*windowsInstallControlLock, error) {
	if err := p.verifyNamespace(); err != nil {
		return nil, err
	}
	h, id, err := p.openFixedFile(name)
	if err != nil {
		return nil, err
	}
	parent, err := dupHandle(p.root)
	if err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	l := &windowsInstallControlLock{deps: p.deps, file: h, parent: parent, name: name, fileID: id, parentID: p.rootID}
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err = windows.LockFileEx(h, flags, 0, 1, 0, &l.overlapped); err != nil {
		_ = windows.CloseHandle(parent)
		_ = windows.CloseHandle(h)
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrInstallControlBusy
		}
		return nil, fmt.Errorf("lock %s: %w", name, err)
	}
	l.held = true
	return l, nil
}

func (l *windowsInstallControlLock) checkHeld() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || !l.held || l.file == 0 || l.parent == 0 {
		return os.ErrClosed
	}
	if err := verifyInstallControlWindowsDirectory(l.parent, installControlDirName, l.deps); err != nil {
		return errors.Join(ErrInstallStateChanged, err)
	}
	parentID, err := identityFromHandle(l.parent)
	if err != nil || parentID != l.parentID {
		return errors.Join(ErrInstallStateChanged, err)
	}
	fileID, err := validateInstallControlWindowsFile(l.file, l.name, l.deps)
	if err != nil || fileID != l.fileID {
		return errors.Join(ErrInstallStateChanged, err)
	}
	h, err := openRelative(l.parent, l.name, windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.SYNCHRONIZE, windows.FILE_OPEN,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, nil)
	if err != nil {
		return errors.Join(ErrInstallStateChanged, err)
	}
	id, idErr := validateInstallControlWindowsFile(h, l.name, l.deps)
	closeErr := windows.CloseHandle(h)
	if id != l.fileID || idErr != nil || closeErr != nil {
		return errors.Join(ErrInstallStateChanged, idErr, closeErr)
	}
	return nil
}

func (l *windowsInstallControlLock) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	var errs []error
	if l.held && l.file != 0 {
		errs = append(errs, windows.UnlockFileEx(l.file, 0, 1, 0, &l.overlapped))
		l.held = false
	}
	if l.file != 0 {
		errs = append(errs, windows.CloseHandle(l.file))
		l.file = 0
	}
	if l.parent != 0 {
		errs = append(errs, windows.CloseHandle(l.parent))
		l.parent = 0
	}
	return errors.Join(errs...)
}

func openInstallControlWindowsDirectoryPath(path string) (windows.Handle, error) {
	return openNTPath(path, installControlWindowsDirAccess, windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		windows.FILE_ATTRIBUTE_DIRECTORY, nil)
}

func openInstallControlWindowsDirectory(parent windows.Handle, name string) (windows.Handle, error) {
	return openRelative(parent, name, installControlWindowsDirAccess, windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		windows.FILE_ATTRIBUTE_DIRECTORY, nil)
}

func openOrCreateInstallControlWindowsDirectory(parent windows.Handle, name string, deps installControlWindowsDeps) (windows.Handle, error) {
	h, err := openInstallControlWindowsDirectory(parent, name)
	if err == nil {
		if err = verifyInstallControlWindowsDirectory(h, name, deps); err != nil {
			_ = windows.CloseHandle(h)
			return 0, err
		}
		return h, nil
	}
	if !isWindowsNotFound(err) {
		return 0, err
	}
	sd, err := deps.descriptor(true)
	if err != nil {
		return 0, err
	}
	h, err = openRelative(parent, name, installControlWindowsDirAccess|windows.DELETE, windows.FILE_CREATE,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		windows.FILE_ATTRIBUTE_DIRECTORY, sd)
	runtime.KeepAlive(sd)
	if isWindowsExist(err) {
		h, err = openInstallControlWindowsDirectory(parent, name)
		if err != nil {
			return 0, err
		}
		if err = verifyInstallControlWindowsDirectory(h, name, deps); err != nil {
			_ = windows.CloseHandle(h)
			return 0, err
		}
		return h, nil
	}
	if err != nil {
		return 0, err
	}
	if err = deps.hardenSecurity(h, true); err != nil {
		_ = windows.CloseHandle(h)
		return 0, err
	}
	if err = verifyInstallControlWindowsDirectory(h, name, deps); err != nil {
		_ = windows.CloseHandle(h)
		return 0, err
	}
	return h, nil
}

func createInstallControlWindowsTempDir(parent windows.Handle, deps installControlWindowsDeps) (windows.Handle, string, error) {
	for i := 0; i < 100; i++ {
		name, err := randomTempName(".install-control-*")
		if err != nil {
			return 0, "", err
		}
		sd, err := deps.descriptor(true)
		if err != nil {
			return 0, "", err
		}
		h, err := openRelative(parent, name, installControlWindowsDirAccess|windows.DELETE, windows.FILE_CREATE,
			windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
			windows.FILE_ATTRIBUTE_DIRECTORY, sd)
		runtime.KeepAlive(sd)
		if isWindowsExist(err) {
			continue
		}
		if err != nil {
			return 0, "", err
		}
		if err = deps.hardenSecurity(h, true); err != nil {
			return 0, "", errors.Join(err, cleanupInstallControlWindowsTemp(h))
		}
		if err = verifyInstallControlWindowsDirectory(h, name, deps); err != nil {
			return 0, "", errors.Join(err, cleanupInstallControlWindowsTemp(h))
		}
		return h, name, nil
	}
	return 0, "", fmt.Errorf("create installation control directory: exhausted names")
}

func createInstallControlWindowsFile(parent windows.Handle, name string, body []byte, deps installControlWindowsDeps) (err error) {
	sd, err := deps.descriptor(false)
	if err != nil {
		return err
	}
	h, err := openRelative(parent, name, installControlWindowsFileAccess, windows.FILE_CREATE,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		windows.FILE_ATTRIBUTE_NORMAL, sd)
	runtime.KeepAlive(sd)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(h)) }()
	if err = deps.hardenSecurity(h, false); err != nil {
		return err
	}
	if _, err = validateInstallControlWindowsFile(h, name, deps); err != nil {
		return err
	}
	if err = writeInstallControlWindowsFile(h, body); err != nil {
		return err
	}
	return deps.flushFile(h)
}

func verifyInstallControlWindowsDirectory(h windows.Handle, name string, deps installControlWindowsDeps) error {
	if err := rejectReparse(h, name); err != nil {
		return err
	}
	attr, err := handleAttributes(h)
	if err != nil || attr&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return errors.Join(ErrUnsafeComponent, err)
	}
	return deps.checkSecurity(h, true)
}

func verifyInstallControlWindowsNamedDirectory(parent windows.Handle, name string, expected fileIdentity, deps installControlWindowsDeps) (err error) {
	h, err := openInstallControlWindowsDirectory(parent, name)
	if err != nil {
		return errors.Join(ErrInstallStateChanged, err)
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(h)) }()
	if err = verifyInstallControlWindowsDirectory(h, name, deps); err != nil {
		return err
	}
	id, err := identityFromHandle(h)
	if err != nil || id != expected {
		return errors.Join(ErrInstallStateChanged, err)
	}
	return nil
}

func validateInstallControlWindowsFile(h windows.Handle, name string, deps installControlWindowsDeps) (fileIdentity, error) {
	if err := rejectReparse(h, name); err != nil {
		return fileIdentity{}, err
	}
	attr, err := handleAttributes(h)
	if err != nil || attr&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return fileIdentity{}, errors.Join(ErrUnsafeComponent, err)
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil || info.NumberOfLinks != 1 {
		return fileIdentity{}, errors.Join(ErrUnsafeComponent, err)
	}
	if err = deps.checkSecurity(h, false); err != nil {
		return fileIdentity{}, fmt.Errorf("check %s security: %w", name, err)
	}
	return identityFromHandle(h)
}

func writeInstallControlWindowsFile(h windows.Handle, body []byte) error {
	dup, err := dupHandle(h)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(dup), "install-state")
	n, writeErr := f.Write(body)
	if writeErr == nil && n != len(body) {
		writeErr = io.ErrShortWrite
	}
	return errors.Join(writeErr, f.Close())
}

func readInstallControlWindowsFile(h windows.Handle) ([]byte, error) {
	dup, err := dupHandle(h)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(dup), "install-state")
	b := make([]byte, installStateMaxBytes+1)
	n, readErr := f.ReadAt(b, 0)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	closeErr := f.Close()
	if n > installStateMaxBytes {
		return nil, errors.Join(ErrInstallStateTooLarge, readErr, closeErr)
	}
	return b[:n], errors.Join(readErr, closeErr)
}

func cleanupInstallControlWindowsTemp(h windows.Handle) error {
	if h == 0 {
		return nil
	}
	list, err := openListingHandle(h)
	if err == nil {
		entries, readErr := readWindowsDirents(list)
		err = errors.Join(readErr, windows.CloseHandle(list))
		for _, entry := range entries {
			child, openErr := openRelative(h, entry.name, windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
				windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, nil)
			if openErr != nil {
				err = errors.Join(err, openErr)
				continue
			}
			err = errors.Join(err, deleteInstallControlWindowsHandle(child))
		}
	}
	err = errors.Join(err, markWindowsHandleForDeletion(h), windows.CloseHandle(h))
	return err
}

func deleteInstallControlWindowsHandle(h windows.Handle) error {
	if h == 0 {
		return nil
	}
	return errors.Join(markWindowsHandleForDeletion(h), windows.CloseHandle(h))
}

func installControlWindowsSDDL(directory bool) string {
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	return "O:BAD:P(A;" + inherit + ";FA;;;SY)(A;" + inherit + ";FA;;;BA)"
}

func hardenInstallControlWindowsSecurity(h windows.Handle, directory bool) error {
	sd, err := windows.SecurityDescriptorFromString(installControlWindowsSDDL(directory))
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return errors.Join(errors.New("installation control owner is absent"), err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return errors.Join(errors.New("installation control DACL is absent"), err)
	}
	err = windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner, nil, dacl, nil)
	runtime.KeepAlive(sd)
	return err
}

func checkInstallControlWindowsSecurity(h windows.Handle, directory bool) error {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	return validateInstallControlWindowsSecurityDescriptor(sd, directory)
}

func validateInstallControlWindowsSecurityDescriptor(sd *windows.SECURITY_DESCRIPTOR, directory bool) error {
	if sd == nil {
		return errors.New("installation control security descriptor is absent")
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(system) && !owner.Equals(admins) {
		return errors.Join(errors.New("installation control owner is not SYSTEM or Administrators"), err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return errors.Join(errors.New("installation control DACL is not protected"), err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return errors.Join(errors.New("installation control DACL is absent"), err)
	}
	var sawSystem, sawAdmins bool
	wantFlags := uint8(0)
	if directory {
		wantFlags = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
			ace.Header.AceFlags != wantFlags || uint32(ace.Mask) != privateFileAllAccess {
			return errors.Join(errors.New("installation control DACL contains an unexpected ACE"), err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		switch {
		case sid.Equals(system):
			sawSystem = true
		case sid.Equals(admins):
			sawAdmins = true
		default:
			return errors.New("installation control DACL grants an unexpected principal")
		}
	}
	if !sawSystem || !sawAdmins || dacl.AceCount != 2 {
		return errors.New("installation control DACL must grant only SYSTEM and Administrators")
	}
	return nil
}

func checkInstallControlWindowsVolume(h windows.Handle) error {
	fs := make([]uint16, 32)
	if err := windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))); err != nil {
		return err
	}
	if !strings.EqualFold(windows.UTF16ToString(fs), "NTFS") {
		return fmt.Errorf("unsupported installation control filesystem %q", windows.UTF16ToString(fs))
	}
	volume, err := openInstallControlWindowsVolume(h)
	if err != nil {
		return fmt.Errorf("open installation control volume for durability: %w", err)
	}
	return errors.Join(windows.FlushFileBuffers(volume), windows.CloseHandle(volume))
}

func syncInstallControlWindowsVolume(h windows.Handle) error {
	volume, err := openInstallControlWindowsVolume(h)
	if err != nil {
		return err
	}
	return errors.Join(windows.FlushFileBuffers(volume), windows.CloseHandle(volume))
}

func openInstallControlWindowsVolume(h windows.Handle) (windows.Handle, error) {
	rootSerial, rootFS, err := installControlWindowsVolumeIdentity(h)
	if err != nil {
		return 0, err
	}
	path, err := finalPathFromHandle(h)
	if err != nil {
		return 0, err
	}
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	mount := make([]uint16, windows.MAX_PATH+1)
	if err = windows.GetVolumePathName(pathPtr, &mount[0], uint32(len(mount))); err != nil {
		return 0, err
	}
	guid := make([]uint16, windows.MAX_PATH+1)
	if err = windows.GetVolumeNameForVolumeMountPoint(&mount[0], &guid[0], uint32(len(guid))); err != nil {
		return 0, err
	}
	volumePath := strings.TrimRight(windows.UTF16ToString(guid), `\`)
	volumePtr, err := windows.UTF16PtrFromString(volumePath)
	if err != nil {
		return 0, err
	}
	volume, err := windows.CreateFile(volumePtr, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0, err
	}
	volumeSerial, volumeFS, verifyErr := installControlWindowsVolumeIdentity(volume)
	if verifyErr != nil || volumeSerial != rootSerial || !strings.EqualFold(volumeFS, rootFS) {
		return 0, errors.Join(errors.New("installation control volume identity changed"), verifyErr, windows.CloseHandle(volume))
	}
	return volume, nil
}

func installControlWindowsVolumeIdentity(h windows.Handle) (uint32, string, error) {
	fs := make([]uint16, 32)
	var serial uint32
	if err := windows.GetVolumeInformationByHandle(h, nil, 0, &serial, nil, nil, &fs[0], uint32(len(fs))); err != nil {
		return 0, "", err
	}
	return serial, windows.UTF16ToString(fs), nil
}
