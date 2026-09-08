package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
)

const launchdRuntimeStateMaxBytes = 4 << 10

var (
	// ErrLaunchdRuntimeUninitialized reports an existing runtime directory whose
	// fixed state or permanent lock file is absent.
	ErrLaunchdRuntimeUninitialized = errors.New("launchd runtime control is uninitialized")
	// ErrLaunchdRuntimeStateTooLarge reports a raw runtime record above 4 KiB.
	ErrLaunchdRuntimeStateTooLarge = errors.New("launchd runtime state exceeds 4 KiB")
	// ErrLaunchdRuntimeNotDurable prevents the app-store adapter from accepting
	// a publication whose namespace visibility was not followed by durability.
	ErrLaunchdRuntimeNotDurable = errors.New("launchd runtime publication is not durable")
)

type launchdRuntimeControlPlatform interface {
	readState(context.Context) ([]byte, error)
	publishState(context.Context, string, []byte) (InstallPublication, error)
	confirmState(context.Context, string) error
	lockStartup(context.Context) (installControlLockPlatform, error)
	close() error
}

// LaunchdRuntimeControl is a closeable capability over the fixed global
// launchd-runtime record and its permanent startup gate. Do not copy after use.
type LaunchdRuntimeControl struct {
	capabilityLifetime
	platform launchdRuntimeControlPlatform
	session  *launchdRuntimeSession
}

// LaunchdRuntimeGate is a held permanent start.lock capability. Closing it
// releases the lock without deleting the file. Do not copy after use.
type LaunchdRuntimeGate struct {
	mu       sync.Mutex
	platform installControlLockPlatform
	session  *launchdRuntimeSession
	closed   bool
	closeErr error
}

type launchdRuntimeSession struct {
	mu   sync.Mutex
	held bool
	gate installControlLockPlatform
}

func newLaunchdRuntimeControl(platform launchdRuntimeControlPlatform) *LaunchdRuntimeControl {
	return &LaunchdRuntimeControl{platform: platform, session: &launchdRuntimeSession{}}
}

// Read returns the raw state bytes and their lowercase SHA-256 digest.
func (c *LaunchdRuntimeControl) Read(ctx context.Context) ([]byte, string, error) {
	finish, err := c.beginControl(ctx)
	if err != nil {
		return nil, "", err
	}
	defer finish()
	b, err := c.platform.readState(ctx)
	if err != nil {
		return nil, "", err
	}
	if len(b) > launchdRuntimeStateMaxBytes {
		return nil, "", ErrLaunchdRuntimeStateTooLarge
	}
	return b, installStateSHA256(b), nil
}

// Publish implements the app runtime-store adapter and succeeds only after a
// conditional publication is proven durable.
func (c *LaunchdRuntimeControl) Publish(ctx context.Context, previousSHA256 string, next []byte) error {
	publication, err := c.PublishState(ctx, previousSHA256, next)
	if err != nil {
		return err
	}
	if !publication.Published || !publication.Durable {
		return ErrLaunchdRuntimeNotDurable
	}
	return nil
}

// PublishState conditionally replaces state.json. The caller must hold this
// control's own live start.lock capability.
func (c *LaunchdRuntimeControl) PublishState(ctx context.Context, previousSHA256 string, next []byte) (InstallPublication, error) {
	finish, err := c.beginControl(ctx)
	if err != nil {
		return InstallPublication{}, err
	}
	defer finish()
	if !validInstallStateSHA256(previousSHA256, false) {
		return InstallPublication{}, fmt.Errorf("invalid launchd runtime state digest: %w", os.ErrInvalid)
	}
	if len(next) > launchdRuntimeStateMaxBytes {
		return InstallPublication{}, ErrLaunchdRuntimeStateTooLarge
	}
	if c.session == nil {
		return InstallPublication{}, os.ErrClosed
	}
	c.session.mu.Lock()
	defer c.session.mu.Unlock()
	if !c.session.held || c.session.gate == nil {
		return InstallPublication{}, ErrInstallControlBusy
	}
	if err = c.session.gate.checkHeld(); err != nil {
		return InstallPublication{}, err
	}
	current, err := c.platform.readState(ctx)
	if err != nil {
		return InstallPublication{}, err
	}
	if len(current) > launchdRuntimeStateMaxBytes {
		return InstallPublication{}, ErrLaunchdRuntimeStateTooLarge
	}
	if installStateSHA256(current) != previousSHA256 {
		return InstallPublication{}, ErrInstallStateChanged
	}
	return c.platform.publishState(ctx, previousSHA256, append([]byte(nil), next...))
}

// ConfirmState syncs and re-reads the matching held state without modifying it.
func (c *LaunchdRuntimeControl) ConfirmState(ctx context.Context, expectedSHA256 string) error {
	if !validInstallStateSHA256(expectedSHA256, false) {
		return fmt.Errorf("invalid launchd runtime state digest: %w", os.ErrInvalid)
	}
	state, digest, err := c.Read(ctx)
	if err != nil {
		return err
	}
	if digest != expectedSHA256 || len(state) > launchdRuntimeStateMaxBytes {
		return ErrInstallStateChanged
	}
	finish, err := c.beginControl(ctx)
	if err != nil {
		return err
	}
	defer finish()
	return c.platform.confirmState(ctx, expectedSHA256)
}

// LockStartup takes the permanent nonblocking launchd runtime startup gate.
func (c *LaunchdRuntimeControl) LockStartup(ctx context.Context) (*LaunchdRuntimeGate, error) {
	finish, err := c.beginControl(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	if c.session == nil {
		return nil, os.ErrClosed
	}
	c.session.mu.Lock()
	defer c.session.mu.Unlock()
	if c.session.held {
		return nil, ErrInstallControlBusy
	}
	gate, err := c.platform.lockStartup(ctx)
	if err != nil {
		return nil, err
	}
	c.session.held, c.session.gate = true, gate
	return &LaunchdRuntimeGate{platform: gate, session: c.session}, nil
}

// Close releases resources owned by the control. A returned gate remains
// caller-owned and held until its own Close.
func (c *LaunchdRuntimeControl) Close() error {
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

// Close releases the held start.lock and is idempotent.
func (g *LaunchdRuntimeGate) Close() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return g.closeErr
	}
	if g.session != nil {
		g.session.mu.Lock()
		defer g.session.mu.Unlock()
	}
	g.closed = true
	if g.platform != nil {
		g.closeErr = g.platform.close()
	}
	if g.session != nil && g.session.gate == g.platform {
		g.session.held, g.session.gate = false, nil
	}
	return g.closeErr
}

func (c *LaunchdRuntimeControl) beginControl(ctx context.Context) (func(), error) {
	if c == nil || c.platform == nil {
		return nil, os.ErrClosed
	}
	return c.begin(ctx)
}
