package platform

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sync"
)

const (
	installControlDirName    = "install-control"
	installStateName         = "state.json"
	installPreviousStateName = "previous-state.json"
	installOperationLockName = "operation.lock"
	installStartupLockName   = "startup.lock"
	installStateMaxBytes     = 64 << 10
)

var (
	// ErrInstallControlReadOnly reports a mutation attempted through an
	// inspection-only installation-control capability.
	ErrInstallControlReadOnly = errors.New("installation control is read-only")
	// ErrInstallControlUninitialized reports an existing control directory
	// whose fixed state or lock files are absent.
	ErrInstallControlUninitialized = errors.New("installation control is uninitialized")
	// ErrInstallStateChanged reports that the name-bound state no longer has
	// the identity or digest observed by the caller.
	ErrInstallStateChanged = errors.New("installation state changed")
	// ErrInstallStateTooLarge reports a raw state document beyond the fixed
	// installation-record bound.
	ErrInstallStateTooLarge = errors.New("installation state exceeds 64 KiB")
	// ErrInstallControlBusy reports that the permanent operation or startup
	// lock is already held.
	ErrInstallControlBusy = errors.New("installation control lock is already held")
)

// InstallPublication separates namespace visibility from proven durability.
// Callers must not treat Published as Durable.
type InstallPublication struct {
	Published bool
	Durable   bool
}

type installControlPlatform interface {
	readState(context.Context) ([]byte, error)
	archiveState(context.Context, string) error
	publishState(context.Context, string, []byte) (InstallPublication, error)
	confirmState(context.Context, string) error
	probeOperation(context.Context) (bool, error)
	lockOperation(context.Context) (installControlLockPlatform, error)
	lockStartup(context.Context) (installControlLockPlatform, error)
	close() error
}

type installControlLockPlatform interface {
	checkHeld() error
	close() error
}

type installControlLockKind uint8

const (
	installControlOperationLock installControlLockKind = iota + 1
	installControlStartupLock
)

// InstallControl is a closeable capability over the fixed machine
// installation-control directory. Do not copy after first use.
type InstallControl struct {
	capabilityLifetime
	platform installControlPlatform
	readOnly bool
	session  *installControlSession
	// borrow retains an outer platform lease through each complete operation.
	borrow func(context.Context) (func(), error)
}

// InstallControlLock is a held permanent-file lock. Closing it releases the
// lock but never deletes its fixed lock file. Do not copy after first use.
type InstallControlLock struct {
	mu       sync.Mutex
	kind     installControlLockKind
	platform installControlLockPlatform
	session  *installControlSession
	closed   bool
	closeErr error
}

type installControlSession struct {
	mu            sync.Mutex
	operationHeld bool
	operation     installControlLockPlatform
}

func newInstallControl(platform installControlPlatform, readOnly bool) *InstallControl {
	return &InstallControl{platform: platform, readOnly: readOnly, session: &installControlSession{}}
}

// ReadState reads the raw state bytes and their lowercase SHA-256 digest.
func (c *InstallControl) ReadState(ctx context.Context) ([]byte, string, error) {
	finish, err := c.beginControl(ctx)
	if err != nil {
		return nil, "", err
	}
	defer finish()
	b, err := c.platform.readState(ctx)
	if err != nil {
		return nil, "", err
	}
	if len(b) > installStateMaxBytes {
		return nil, "", ErrInstallStateTooLarge
	}
	return b, installStateSHA256(b), nil
}

// ArchiveState stores exactly the currently held state as the single bounded
// previous-state record. It never restores or changes state.json.
func (c *InstallControl) ArchiveState(ctx context.Context, expectedSHA256 string) error {
	finish, err := c.beginMutation(ctx, expectedSHA256, false)
	if err != nil {
		return err
	}
	defer finish()
	return c.platform.archiveState(ctx, expectedSHA256)
}

// PublishState conditionally replaces state.json when previousSHA256 still
// identifies the name-bound predecessor. Initial absence is handled only by
// atomic control-directory initialization.
func (c *InstallControl) PublishState(ctx context.Context, previousSHA256 string, next []byte) (InstallPublication, error) {
	finish, err := c.beginMutation(ctx, previousSHA256, false)
	if err != nil {
		return InstallPublication{}, err
	}
	defer finish()
	if len(next) > installStateMaxBytes {
		return InstallPublication{}, ErrInstallStateTooLarge
	}
	return c.platform.publishState(ctx, previousSHA256, append([]byte(nil), next...))
}

// ConfirmState proves durability of the matching version held by this
// capability and then re-reads its digest. It does not modify state.json.
func (c *InstallControl) ConfirmState(ctx context.Context, expectedSHA256 string) error {
	finish, err := c.beginControl(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if !validInstallStateSHA256(expectedSHA256, false) {
		return fmt.Errorf("invalid expected state digest: %w", os.ErrInvalid)
	}
	return c.platform.confirmState(ctx, expectedSHA256)
}

// ProbeOperation reports whether another process holds operation.lock. The
// probe opens only the permanent existing file and releases its test lock.
func (c *InstallControl) ProbeOperation(ctx context.Context) (bool, error) {
	finish, err := c.beginControl(ctx)
	if err != nil {
		return false, err
	}
	defer finish()
	return c.platform.probeOperation(ctx)
}

// LockOperation takes the permanent machine installation-operation lock.
func (c *InstallControl) LockOperation(ctx context.Context) (*InstallControlLock, error) {
	return c.lock(ctx, installControlOperationLock)
}

// LockStartup takes the permanent startup gate.
func (c *InstallControl) LockStartup(ctx context.Context) (*InstallControlLock, error) {
	return c.lock(ctx, installControlStartupLock)
}

func (c *InstallControl) lock(ctx context.Context, kind installControlLockKind) (*InstallControlLock, error) {
	finish, err := c.beginControl(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	if c.readOnly && kind == installControlOperationLock {
		return nil, ErrInstallControlReadOnly
	}
	if c.session == nil {
		return nil, os.ErrClosed
	}
	c.session.mu.Lock()
	defer c.session.mu.Unlock()
	if kind == installControlOperationLock && c.session.operationHeld {
		return nil, ErrInstallControlBusy
	}
	var l installControlLockPlatform
	switch kind {
	case installControlOperationLock:
		l, err = c.platform.lockOperation(ctx)
	case installControlStartupLock:
		l, err = c.platform.lockStartup(ctx)
	default:
		err = os.ErrInvalid
	}
	if err != nil {
		return nil, err
	}
	if kind == installControlOperationLock {
		c.session.operationHeld = true
		c.session.operation = l
	}
	return &InstallControlLock{kind: kind, platform: l, session: c.session}, nil
}

// Close releases the directory, held state, and any platform resources owned
// by the control capability. Independently returned locks remain caller-owned.
func (c *InstallControl) Close() error {
	if c == nil {
		return nil
	}
	return c.closeWith(func() error {
		if c.platform == nil {
			return nil
		}
		return c.platform.close()
	})
}

// Close releases the held byte-range lock. It is idempotent.
func (l *InstallControlLock) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return l.closeErr
	}
	if l.kind == installControlOperationLock && l.session != nil {
		l.session.mu.Lock()
		defer l.session.mu.Unlock()
	}
	l.closed = true
	if l.platform != nil {
		l.closeErr = l.platform.close()
	}
	if l.kind == installControlOperationLock && l.session != nil {
		l.session.operationHeld = false
		l.session.operation = nil
	}
	return l.closeErr
}

// CheckWindowsStartupGate verifies that this is a live, held startup.lock
// capability. Windows service-state backends use it before registry mutation.
func (l *InstallControlLock) CheckWindowsStartupGate() error {
	if l == nil {
		return os.ErrClosed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.platform == nil {
		return os.ErrClosed
	}
	if l.kind != installControlStartupLock {
		return os.ErrPermission
	}
	return l.platform.checkHeld()
}

func (c *InstallControl) beginControl(ctx context.Context) (func(), error) {
	if c == nil || c.platform == nil {
		return nil, os.ErrClosed
	}
	finish, err := c.begin(ctx)
	if err != nil {
		return nil, err
	}
	if c.borrow == nil {
		return finish, nil
	}
	release, err := c.borrow(ctx)
	if err != nil {
		finish()
		return nil, err
	}
	return func() { release(); finish() }, nil
}

func (c *InstallControl) beginMutation(ctx context.Context, digest string, allowEmpty bool) (func(), error) {
	finish, err := c.beginControl(ctx)
	if err != nil {
		return nil, err
	}
	if c.readOnly {
		finish()
		return nil, ErrInstallControlReadOnly
	}
	if !validInstallStateSHA256(digest, allowEmpty) {
		finish()
		return nil, fmt.Errorf("invalid installation state digest: %w", os.ErrInvalid)
	}
	if c.session == nil {
		finish()
		return nil, os.ErrClosed
	}
	c.session.mu.Lock()
	if !c.session.operationHeld || c.session.operation == nil {
		c.session.mu.Unlock()
		finish()
		return nil, ErrInstallControlBusy
	}
	if err = c.session.operation.checkHeld(); err != nil {
		c.session.mu.Unlock()
		finish()
		return nil, err
	}
	return func() {
		c.session.mu.Unlock()
		finish()
	}, nil
}

func installStateSHA256(b []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func validInstallStateSHA256(s string, allowEmpty bool) bool {
	if s == "" {
		return allowEmpty
	}
	if len(s) != sha256.Size*2 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
