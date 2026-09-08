//go:build windows

package platform

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	windowsBootSessionParentPath  = `SOFTWARE\Mihari\InstallControl\BootSessions`
	windowsBootSessionSDDL        = "O:BAD:P(A;;GA;;;SY)(A;;GA;;;BA)"
	windowsRegistryOptionVolatile = 1
	windowsRegistryCreatedNewKey  = 1
	windowsRegistryOpenedExisting = 2
)

var (
	// ErrWindowsBootSessionInvalid indicates that the protected boot-session
	// namespace does not contain exactly zero or one valid child as required.
	ErrWindowsBootSessionInvalid   = errors.New("windows boot-session registry state is invalid")
	errWindowsBootRegistryNotFound = errors.New("windows boot-session registry parent not found")

	advapi32BootSession                                  = windows.NewLazySystemDLL("advapi32.dll")
	procRegCreateKeyExW                                  = advapi32BootSession.NewProc("RegCreateKeyExW")
	nativeBootSessionRegistry windowsBootSessionRegistry = windowsBootSessionRegistryNative{}
)

type windowsRegistryHandle uintptr

type windowsBootRegistryChild struct {
	name      string
	protected bool
}

// WindowsStartupGate is a borrowed proof that startup.lock is currently held.
// InstallControlLock implements it only for a live startup-lock capability.
type WindowsStartupGate interface {
	CheckWindowsStartupGate() error
}

// WindowsBootSessionProvider reads or initializes the protected machine boot
// session namespace.
type WindowsBootSessionProvider struct {
	registry windowsBootSessionRegistry
	random   io.Reader
}

// NewWindowsBootSessionProvider returns the native HKLM provider.
func NewWindowsBootSessionProvider() *WindowsBootSessionProvider {
	return newWindowsBootSessionProvider(nativeBootSessionRegistry, rand.Reader)
}

func newWindowsBootSessionProvider(registry windowsBootSessionRegistry, random io.Reader) *WindowsBootSessionProvider {
	return &WindowsBootSessionProvider{registry: registry, random: random}
}

// Read returns the existing protected boot id without creating registry state.
func (p *WindowsBootSessionProvider) Read(ctx context.Context) (id string, present bool, err error) {
	if p == nil || p.registry == nil {
		return "", false, fmt.Errorf("windows boot-session provider is nil")
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	parent, err := p.registry.openParentRead(ctx)
	if errors.Is(err, errWindowsBootRegistryNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("open windows boot-session parent: %w", err)
	}
	defer func() {
		err = errors.Join(err, p.registry.closeKey(parent))
	}()
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if err := p.registry.verifyProtectedParent(ctx, parent); err != nil {
		return "", false, fmt.Errorf("verify windows boot-session parent: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	children, err := p.registry.listChildren(ctx, parent)
	if err != nil {
		return "", false, fmt.Errorf("list windows boot sessions: %w", err)
	}
	return validateWindowsBootSessionChildren(children)
}

// Ensure returns the single protected boot id, creating one volatile child
// only while the caller's borrowed startup gate remains valid.
func (p *WindowsBootSessionProvider) Ensure(ctx context.Context, gate WindowsStartupGate) (id string, err error) {
	if p == nil || p.registry == nil || p.random == nil {
		return "", fmt.Errorf("windows boot-session provider is nil")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if gate == nil {
		return "", fmt.Errorf("windows startup gate is nil")
	}
	if err := gate.CheckWindowsStartupGate(); err != nil {
		return "", fmt.Errorf("check windows startup gate: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	parent, err := p.registry.openOrCreateProtectedParent(ctx)
	if err != nil {
		return "", fmt.Errorf("open protected windows boot-session parent: %w", err)
	}
	defer func() {
		err = errors.Join(err, p.registry.closeKey(parent))
	}()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := p.registry.verifyProtectedParent(ctx, parent); err != nil {
		return "", fmt.Errorf("verify windows boot-session parent: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	children, err := p.registry.listChildren(ctx, parent)
	if err != nil {
		return "", fmt.Errorf("list windows boot sessions: %w", err)
	}
	id, present, err := validateWindowsBootSessionChildren(children)
	if err != nil || present {
		return id, err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := gate.CheckWindowsStartupGate(); err != nil {
		return "", fmt.Errorf("recheck windows startup gate: %w", err)
	}
	var randomID [16]byte
	if _, err := io.ReadFull(p.random, randomID[:]); err != nil {
		return "", fmt.Errorf("generate windows boot-session id: %w", err)
	}
	id = hex.EncodeToString(randomID[:])
	if err := ctx.Err(); err != nil {
		return "", err
	}
	child, collision, err := p.registry.createVolatileProtectedChild(ctx, parent, id)
	if err != nil {
		return "", fmt.Errorf("create volatile windows boot session: %w", err)
	}
	closeChildErr := p.registry.closeKey(child)
	if collision {
		return "", errors.Join(ErrWindowsBootSessionInvalid, closeChildErr)
	}
	if closeChildErr != nil {
		return "", fmt.Errorf("close volatile windows boot session: %w", closeChildErr)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	children, err = p.registry.listChildren(ctx, parent)
	if err != nil {
		return "", fmt.Errorf("verify created windows boot session: %w", err)
	}
	verifiedID, present, err := validateWindowsBootSessionChildren(children)
	if err != nil || !present || verifiedID != id {
		return "", errors.Join(ErrWindowsBootSessionInvalid, err)
	}
	return id, nil
}

func validateWindowsBootSessionChildren(children []windowsBootRegistryChild) (string, bool, error) {
	if len(children) == 0 {
		return "", false, nil
	}
	if len(children) != 1 || !isLowerHex32(children[0].name) || !children[0].protected {
		return "", false, ErrWindowsBootSessionInvalid
	}
	return children[0].name, true, nil
}

type windowsBootSessionRegistry interface {
	openParentRead(context.Context) (windowsRegistryHandle, error)
	openOrCreateProtectedParent(context.Context) (windowsRegistryHandle, error)
	verifyProtectedParent(context.Context, windowsRegistryHandle) error
	listChildren(context.Context, windowsRegistryHandle) ([]windowsBootRegistryChild, error)
	createVolatileProtectedChild(context.Context, windowsRegistryHandle, string) (windowsRegistryHandle, bool, error)
	closeKey(windowsRegistryHandle) error
}

type windowsBootSessionRegistryNative struct{}

func (windowsBootSessionRegistryNative) openParentRead(ctx context.Context) (windowsRegistryHandle, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, windowsBootSessionParentPath, registry.READ|registry.WOW64_64KEY)
	if errors.Is(err, registry.ErrNotExist) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		return 0, errors.Join(errWindowsBootRegistryNotFound, err)
	}
	return windowsRegistryHandle(key), err
}

func (windowsBootSessionRegistryNative) openOrCreateProtectedParent(ctx context.Context) (windowsRegistryHandle, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	handle, _, err := createWindowsRegistryKey(registry.LOCAL_MACHINE, windowsBootSessionParentPath, false)
	return handle, err
}

func (windowsBootSessionRegistryNative) verifyProtectedParent(ctx context.Context, handle windowsRegistryHandle) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	protected, err := windowsRegistryKeyHasProtectedPolicy(windows.Handle(handle))
	if err != nil {
		return err
	}
	if !protected {
		return fmt.Errorf("windows boot-session registry key has unsafe ACL")
	}
	return nil
}

func (windowsBootSessionRegistryNative) listChildren(ctx context.Context, parent windowsRegistryHandle) ([]windowsBootRegistryChild, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names, err := registry.Key(parent).ReadSubKeyNames(-1)
	if err != nil {
		return nil, err
	}
	children := make([]windowsBootRegistryChild, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key, err := registry.OpenKey(registry.Key(parent), name, registry.READ|registry.WOW64_64KEY)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, key.Close())
		}
		protected, policyErr := windowsRegistryKeyHasProtectedPolicy(windows.Handle(key))
		closeErr := key.Close()
		if policyErr != nil || closeErr != nil {
			return nil, errors.Join(policyErr, closeErr)
		}
		children = append(children, windowsBootRegistryChild{name: name, protected: protected})
	}
	return children, nil
}

func (windowsBootSessionRegistryNative) createVolatileProtectedChild(ctx context.Context, parent windowsRegistryHandle, name string) (windowsRegistryHandle, bool, error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	return createWindowsRegistryKey(registry.Key(parent), name, true)
}

func (windowsBootSessionRegistryNative) closeKey(handle windowsRegistryHandle) error {
	return registry.Key(handle).Close()
}

func createWindowsRegistryKey(parent registry.Key, path string, volatile bool) (windowsRegistryHandle, bool, error) {
	sd, err := windows.SecurityDescriptorFromString(windowsBootSessionSDDL)
	if err != nil {
		return 0, false, err
	}
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, false, err
	}
	attributes := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
		InheritHandle:      0,
	}
	options := uintptr(0)
	if volatile {
		options = windowsRegistryOptionVolatile
	}
	var handle windows.Handle
	var disposition uint32
	status, _, _ := procRegCreateKeyExW.Call(
		uintptr(parent),
		uintptr(unsafe.Pointer(pathPointer)),
		0,
		0,
		options,
		registry.ALL_ACCESS|registry.WOW64_64KEY,
		uintptr(unsafe.Pointer(&attributes)),
		uintptr(unsafe.Pointer(&handle)),
		uintptr(unsafe.Pointer(&disposition)),
	)
	runtime.KeepAlive(sd)
	runtime.KeepAlive(pathPointer)
	if status != 0 {
		return 0, false, syscallError(status)
	}
	if disposition != windowsRegistryCreatedNewKey && disposition != windowsRegistryOpenedExisting {
		return windowsRegistryHandle(handle), false, fmt.Errorf("unexpected registry create disposition %d", disposition)
	}
	return windowsRegistryHandle(handle), disposition == windowsRegistryOpenedExisting, nil
}

func windowsRegistryKeyHasProtectedPolicy(handle windows.Handle) (bool, error) {
	sd, err := windows.GetSecurityInfo(handle, windows.SE_REGISTRY_KEY, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	return windowsRuntimeProtectedPolicy(sd, registry.ALL_ACCESS)
}

func syscallError(status uintptr) error {
	return syscall.Errno(status)
}
