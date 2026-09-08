//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

const (
	windowsRuntimeStateName     = "runtime-windows.json"
	windowsRuntimeTempPrefix    = ".runtime-tmp-"
	windowsRuntimeStateMaxBytes = 4 << 10
)

// ErrWindowsRuntimeStateTooLarge reports a raw Windows runtime record beyond
// its fixed 4 KiB bound.
var ErrWindowsRuntimeStateTooLarge = errors.New("windows runtime state exceeds 4 KiB")

// WindowsRuntimeControl is a closeable capability over the fixed Windows
// runtime record inside the installation-control directory. Do not copy it.
type WindowsRuntimeControl struct {
	capabilityLifetime
	install   *InstallControl
	backend   *windowsInstallControl
	gate      *InstallControlLock
	readOnly  bool
	runtime   windows.Handle
	runtimeID fileIdentity
}

// OpenWindowsRuntimeControlReadOnly opens the fixed runtime record store
// without creating or repairing any object.
func OpenWindowsRuntimeControlReadOnly(ctx context.Context) (*WindowsRuntimeControl, error) {
	return openWindowsRuntimeControl(ctx, true, nil, defaultInstallControlWindowsDeps())
}

// OpenWindowsRuntimeControlForStartup opens the fixed runtime record store for
// a daemon that already holds the matching installation startup gate.
func OpenWindowsRuntimeControlForStartup(ctx context.Context, gate *InstallControlLock) (*WindowsRuntimeControl, error) {
	return openWindowsRuntimeControl(ctx, false, gate, defaultInstallControlWindowsDeps())
}

func openWindowsRuntimeControl(ctx context.Context, readOnly bool, gate *InstallControlLock, deps installControlWindowsDeps) (_ *WindowsRuntimeControl, err error) {
	install, err := openWindowsInstallControl(ctx, readOnly, deps)
	if err != nil {
		return nil, err
	}
	control := &WindowsRuntimeControl{
		install:  install,
		backend:  install.platform.(*windowsInstallControl),
		gate:     gate,
		readOnly: readOnly,
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, control.Close())
		}
	}()
	if !readOnly {
		if err = control.withStartupGate(func() error { return nil }); err != nil {
			return nil, err
		}
	}
	if err = control.discoverRuntime(ctx); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return control, nil
}

// Read returns the exact runtime record bytes and their lowercase SHA-256
// digest. A missing runtime record reports os.ErrNotExist.
func (c *WindowsRuntimeControl) Read(ctx context.Context) ([]byte, string, error) {
	finish, err := c.beginRuntime(ctx)
	if err != nil {
		return nil, "", err
	}
	defer finish()
	raw, err := c.readCurrent(ctx)
	if err != nil {
		return nil, "", err
	}
	return raw, installStateSHA256(raw), nil
}

// Publish conditionally creates or replaces the runtime record.
func (c *WindowsRuntimeControl) Publish(ctx context.Context, previousSHA string, next []byte) (publication InstallPublication, err error) {
	finish, err := c.beginRuntime(ctx)
	if err != nil {
		return publication, err
	}
	defer finish()
	if c.readOnly {
		return publication, ErrInstallControlReadOnly
	}
	if !validInstallStateSHA256(previousSHA, true) {
		return publication, fmt.Errorf("invalid Windows runtime state digest: %w", os.ErrInvalid)
	}
	if len(next) > windowsRuntimeStateMaxBytes {
		return publication, ErrWindowsRuntimeStateTooLarge
	}
	next = append([]byte(nil), next...)
	err = c.withStartupGate(func() error {
		publication, err = c.publishHeld(ctx, previousSHA, next)
		return err
	})
	return publication, err
}

// Confirm proves durability of the matching runtime record without changing it.
func (c *WindowsRuntimeControl) Confirm(ctx context.Context, expectedSHA string) error {
	finish, err := c.beginRuntime(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if c.readOnly {
		return ErrInstallControlReadOnly
	}
	if !validInstallStateSHA256(expectedSHA, false) {
		return fmt.Errorf("invalid expected Windows runtime state digest: %w", os.ErrInvalid)
	}
	return c.withStartupGate(func() error {
		if _, err = c.readMatching(ctx, expectedSHA); err != nil {
			return err
		}
		if err = c.backend.deps.flushFile(c.runtime); err != nil {
			return fmt.Errorf("flush held Windows runtime state: %w", err)
		}
		if err = c.backend.deps.syncVolume(c.backend.root); err != nil {
			return fmt.Errorf("sync Windows runtime state volume: %w", err)
		}
		_, err = c.readMatching(ctx, expectedSHA)
		return err
	})
}

// Close releases this store's handles. It does not close the borrowed startup
// gate supplied to OpenWindowsRuntimeControlForStartup.
func (c *WindowsRuntimeControl) Close() error {
	if c == nil {
		return nil
	}
	return c.closeWith(func() error {
		var errs []error
		if c.runtime != 0 {
			errs = append(errs, windows.CloseHandle(c.runtime))
			c.runtime = 0
		}
		if c.install != nil {
			errs = append(errs, c.install.Close())
		}
		return errors.Join(errs...)
	})
}

func (c *WindowsRuntimeControl) beginRuntime(ctx context.Context) (func(), error) {
	if c == nil || c.install == nil || c.backend == nil {
		return nil, os.ErrClosed
	}
	return c.begin(ctx)
}

// withStartupGate keeps the public gate mutex held across the complete I/O
// operation so InstallControlLock.Close linearizes before or after publication.
// The runtime control borrows the gate and never assumes ownership of it.
func (c *WindowsRuntimeControl) withStartupGate(fn func() error) error {
	if c.gate == nil {
		return os.ErrClosed
	}
	c.gate.mu.Lock()
	defer c.gate.mu.Unlock()
	if c.gate.closed || c.gate.platform == nil {
		return os.ErrClosed
	}
	if c.gate.kind != installControlStartupLock {
		return os.ErrPermission
	}
	gate, ok := c.gate.platform.(*windowsInstallControlLock)
	if !ok {
		return ErrInstallStateChanged
	}
	if c.backend == nil || gate.parentID != c.backend.rootID {
		return ErrInstallStateChanged
	}
	if err := gate.checkHeld(); err != nil {
		return err
	}
	return fn()
}

func (c *WindowsRuntimeControl) discoverRuntime(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.backend.verifyNamespace(); err != nil {
		return err
	}
	h, id, err := c.backend.openFixedFile(windowsRuntimeStateName)
	if isWindowsNotFound(err) {
		return os.ErrNotExist
	}
	if err != nil {
		return fmt.Errorf("open Windows runtime state: %w", err)
	}
	c.runtime = h
	c.runtimeID = id
	return nil
}

func (c *WindowsRuntimeControl) readCurrent(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.backend.verifyNamespace(); err != nil {
		return nil, err
	}
	if c.runtime == 0 {
		if err := c.discoverRuntime(ctx); err != nil {
			return nil, err
		}
	}
	retainedID, err := validateInstallControlWindowsFile(c.runtime, windowsRuntimeStateName, c.backend.deps)
	if err != nil || retainedID != c.runtimeID {
		return nil, errors.Join(ErrInstallStateChanged, err)
	}
	current, currentID, err := c.backend.openFixedFile(windowsRuntimeStateName)
	if err != nil {
		return nil, errors.Join(ErrInstallStateChanged, err)
	}
	closeErr := windows.CloseHandle(current)
	if currentID != c.runtimeID || closeErr != nil {
		return nil, errors.Join(ErrInstallStateChanged, closeErr)
	}
	return readWindowsRuntimeFile(c.runtime)
}

func (c *WindowsRuntimeControl) readMatching(ctx context.Context, expectedSHA string) ([]byte, error) {
	raw, err := c.readCurrent(ctx)
	if err != nil {
		return nil, err
	}
	if installStateSHA256(raw) != expectedSHA {
		return nil, ErrInstallStateChanged
	}
	return raw, nil
}

func (c *WindowsRuntimeControl) publishHeld(ctx context.Context, previousSHA string, next []byte) (publication InstallPublication, err error) {
	if err = ctx.Err(); err != nil {
		return publication, err
	}
	var expected *fileIdentity
	if c.runtime == 0 {
		if discoverErr := c.discoverRuntime(ctx); discoverErr != nil && !errors.Is(discoverErr, os.ErrNotExist) {
			return publication, discoverErr
		}
	}
	if c.runtime == 0 {
		if previousSHA != "" {
			return publication, ErrInstallStateChanged
		}
	} else {
		if previousSHA == "" {
			return publication, ErrInstallStateChanged
		}
		if _, err = c.readMatching(ctx, previousSHA); err != nil {
			return publication, err
		}
		id := c.runtimeID
		expected = &id
	}

	temp, _, err := c.backend.createTempFile(windowsRuntimeTempPrefix)
	if err != nil {
		return publication, err
	}
	defer func() {
		if temp != 0 {
			err = errors.Join(err, deleteInstallControlWindowsHandle(temp))
		}
	}()
	if err = writeInstallControlWindowsFile(temp, next); err != nil {
		return publication, err
	}
	if err = c.backend.deps.flushFile(temp); err != nil {
		return publication, fmt.Errorf("flush Windows runtime state candidate: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return publication, err
	}
	if err = c.backend.verifyNamespace(); err != nil {
		return publication, err
	}
	if err = c.recheckPredecessor(expected, previousSHA); err != nil {
		return publication, err
	}
	newID, err := identityFromHandle(temp)
	if err != nil {
		return publication, err
	}
	flags := uint32(windows.FILE_RENAME_POSIX_SEMANTICS)
	if expected != nil {
		flags |= windows.FILE_RENAME_REPLACE_IF_EXISTS
	}
	if err = c.backend.deps.rename(temp, c.backend.root, windowsRuntimeStateName, flags); err != nil {
		if isWindowsExist(err) || isWindowsNotFound(err) {
			return publication, errors.Join(ErrInstallStateChanged, err)
		}
		return publication, fmt.Errorf("publish Windows runtime state: %w", err)
	}
	publication.Published = true
	old := c.runtime
	c.runtime = temp
	c.runtimeID = newID
	temp = 0
	if old != 0 {
		if err = windows.CloseHandle(old); err != nil {
			return publication, fmt.Errorf("close predecessor Windows runtime state: %w", err)
		}
	}
	if err = c.backend.deps.flushFile(c.runtime); err != nil {
		return publication, fmt.Errorf("flush published Windows runtime state: %w", err)
	}
	if err = c.backend.deps.syncVolume(c.backend.root); err != nil {
		return publication, fmt.Errorf("sync Windows runtime state volume: %w", err)
	}
	if _, err = c.readMatching(ctx, installStateSHA256(next)); err != nil {
		return publication, err
	}
	publication.Durable = true
	return publication, nil
}

func (c *WindowsRuntimeControl) recheckPredecessor(expected *fileIdentity, expectedSHA string) error {
	current, currentID, err := c.backend.openFixedFile(windowsRuntimeStateName)
	if expected == nil {
		if err == nil {
			return errors.Join(ErrInstallStateChanged, windows.CloseHandle(current))
		}
		if isWindowsNotFound(err) {
			return nil
		}
		return err
	}
	if err != nil {
		return errors.Join(ErrInstallStateChanged, err)
	}
	raw, readErr := readWindowsRuntimeFile(current)
	closeErr := windows.CloseHandle(current)
	if currentID != *expected || readErr != nil || installStateSHA256(raw) != expectedSHA || closeErr != nil {
		return errors.Join(ErrInstallStateChanged, readErr, closeErr)
	}
	return nil
}

func readWindowsRuntimeFile(h windows.Handle) ([]byte, error) {
	dup, err := dupHandle(h)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(dup), windowsRuntimeStateName)
	raw := make([]byte, windowsRuntimeStateMaxBytes+1)
	n, readErr := f.ReadAt(raw, 0)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	closeErr := f.Close()
	if n > windowsRuntimeStateMaxBytes {
		return nil, errors.Join(ErrWindowsRuntimeStateTooLarge, readErr, closeErr)
	}
	return raw[:n], errors.Join(readErr, closeErr)
}
