//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

type unixInstallControl struct {
	root     *TrustedRoot
	state    *os.File
	stateID  FileIdentity
	validate func(context.Context) error
}

type unixInstallControlLock struct {
	root *TrustedRoot
	lock *rootLock
}

// OpenInstallControlReadOnly opens fixed K without creating files or
// directories. It can inspect operation.lock and hold startup.lock.
func OpenInstallControlReadOnly(ctx context.Context) (_ *InstallControl, err error) {
	base, err := OpenTrustedRoot(ctx, platformLayoutDefaults("").BaseDir, RootPolicy{Owner: 0, Mode: 0711})
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, base.Close()) }()
	return openUnixInstallControlAt(ctx, base, true, nil)
}

// OpenInstallControlForUpdate opens fixed K using evidence from the already
// held global install lease. The lease must remain valid through mutations.
func OpenInstallControlForUpdate(ctx context.Context, lease *OwnedInstallLease) (_ *InstallControl, err error) {
	finish, err := beginInstallControlUnixLease(ctx, lease)
	if err != nil {
		return nil, err
	}
	defer finish()
	base, validate, err := installControlUnixBaseFromHeldLease(ctx, lease)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, base.Close()) }()
	control, err := openUnixInstallControlAt(ctx, base, false, validate)
	if err == nil {
		control.borrow = func(ctx context.Context) (func(), error) { return beginInstallControlUnixLease(ctx, lease) }
	}
	return control, err
}

// InitializeInstallControlForUpdate atomically publishes K with both permanent
// locks and the caller-supplied initial raw state.
func InitializeInstallControlForUpdate(ctx context.Context, lease *OwnedInstallLease, initial []byte) (_ *InstallControl, publication InstallPublication, err error) {
	finish, err := beginInstallControlUnixLease(ctx, lease)
	if err != nil {
		return nil, publication, err
	}
	defer finish()
	base, validate, err := installControlUnixBaseFromHeldLease(ctx, lease)
	if err != nil {
		return nil, publication, err
	}
	defer func() { err = errors.Join(err, base.Close()) }()
	control, publication, err := initializeUnixInstallControlAt(ctx, base, initial, validate)
	if err == nil {
		control.borrow = func(ctx context.Context) (func(), error) { return beginInstallControlUnixLease(ctx, lease) }
	}
	return control, publication, err
}

func beginInstallControlUnixLease(ctx context.Context, lease *OwnedInstallLease) (func(), error) {
	if lease == nil || lease.state == nil {
		return nil, os.ErrClosed
	}
	state := lease.state
	finish, err := state.begin(ctx)
	if err != nil {
		return nil, err
	}
	if !state.global || state.owner != 0 {
		finish()
		return nil, os.ErrPermission
	}
	if err = state.validate(state.layout); err != nil {
		finish()
		return nil, err
	}
	return finish, nil
}

// The caller holds the lease lifecycle guard throughout use of validate.
func installControlUnixBaseFromHeldLease(ctx context.Context, lease *OwnedInstallLease) (*TrustedRoot, func(context.Context) error, error) {
	if lease == nil || lease.state == nil {
		return nil, nil, os.ErrClosed
	}
	layout := lease.state.layout
	validate := func(checkCtx context.Context) error {
		if err := checkCtx.Err(); err != nil {
			return err
		}
		return lease.state.validate(layout)
	}
	if err := validate(ctx); err != nil {
		return nil, nil, err
	}
	basePath := platformLayoutDefaults("").BaseDir
	for _, root := range lease.state.roots {
		if root.path == basePath {
			cloned, err := cloneTrustedRoot(ctx, root)
			return cloned, validate, err
		}
	}
	return nil, nil, os.ErrPermission
}

func openUnixInstallControlAt(ctx context.Context, base *TrustedRoot, readOnly bool, validate func(context.Context) error) (_ *InstallControl, err error) {
	if base == nil {
		return nil, os.ErrInvalid
	}
	if validate != nil {
		if err = validate(ctx); err != nil {
			return nil, err
		}
	}
	root, err := base.OpenDir(ctx, installControlDirName, RootPolicy{Owner: base.policy.Owner, Mode: 0700})
	if err != nil {
		return nil, err
	}
	p := &unixInstallControl{root: root, validate: validate}
	defer func() {
		if err != nil {
			err = errors.Join(err, p.close())
		}
	}()
	for _, name := range []string{installOperationLockName, installStartupLockName} {
		file, _, openErr := root.OpenFile(ctx, name, 0600)
		if errors.Is(openErr, unix.ENOENT) {
			return nil, fmt.Errorf("%w: missing %s", ErrInstallControlUninitialized, name)
		}
		if openErr != nil {
			return nil, openErr
		}
		if closeErr := file.Close(); closeErr != nil {
			return nil, closeErr
		}
	}
	p.state, p.stateID, err = root.OpenFile(ctx, installStateName, 0600)
	if errors.Is(err, unix.ENOENT) {
		return nil, fmt.Errorf("%w: missing %s", ErrInstallControlUninitialized, installStateName)
	}
	if err != nil {
		return nil, err
	}
	return newInstallControl(p, readOnly), nil
}

func initializeUnixInstallControlAt(ctx context.Context, base *TrustedRoot, initial []byte, validate func(context.Context) error) (_ *InstallControl, publication InstallPublication, err error) {
	if base == nil {
		return nil, publication, os.ErrInvalid
	}
	if len(initial) > installStateMaxBytes {
		return nil, publication, ErrInstallStateTooLarge
	}
	if validate != nil {
		if err = validate(ctx); err != nil {
			return nil, publication, err
		}
	}
	if existing, openErr := base.OpenDir(ctx, installControlDirName, RootPolicy{Owner: base.policy.Owner, Mode: 0700}); openErr == nil {
		if closeErr := existing.Close(); closeErr != nil {
			return nil, publication, closeErr
		}
		control, openErr := openUnixInstallControlAt(ctx, base, false, validate)
		return control, publication, openErr
	} else if !errors.Is(openErr, unix.ENOENT) {
		return nil, publication, openErr
	}
	var temp *TrustedRoot
	var tempName string
	for i := 0; i < 100; i++ {
		tempName, err = randomTempName(".install-control-*")
		if err != nil {
			return nil, publication, err
		}
		temp, err = base.OpenDir(ctx, tempName, RootPolicy{Owner: base.policy.Owner, Mode: 0700, AllowCreate: true})
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		break
	}
	if err != nil {
		return nil, publication, err
	}
	published := false
	defer func() {
		if published {
			err = errors.Join(err, temp.Close())
		} else {
			err = errors.Join(err, cleanupInstallControlUnixTemp(ctx, base, temp, tempName))
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
		if err = temp.WriteFile(ctx, item.name, item.body, 0600, nil); err != nil {
			return nil, publication, err
		}
	}
	if err = temp.Sync(ctx); err != nil {
		return nil, publication, err
	}
	published, err = base.MoveDirTo(ctx, tempName, temp, base, installControlDirName)
	if err != nil && !published && errors.Is(err, unix.EEXIST) {
		err = nil
		control, openErr := openUnixInstallControlAt(ctx, base, false, validate)
		return control, publication, openErr
	}
	publication.Published = published
	if err != nil {
		return nil, publication, err
	}
	control, err := openUnixInstallControlAt(ctx, base, false, validate)
	if err != nil {
		return nil, publication, err
	}
	state, digest, readErr := control.ReadState(ctx)
	if readErr != nil || digest != installStateSHA256(initial) || string(state) != string(initial) {
		_ = control.Close()
		return nil, publication, errors.Join(ErrInstallStateChanged, readErr)
	}
	publication.Durable = true
	return control, publication, nil
}

func (p *unixInstallControl) readState(ctx context.Context) ([]byte, error) {
	if err := p.validateControl(ctx); err != nil {
		return nil, err
	}
	return readInstallControlUnixFile(p.state)
}

func (p *unixInstallControl) archiveState(ctx context.Context, expected string) error {
	b, err := p.readMatchingState(ctx, expected)
	if err != nil {
		return err
	}
	var previous *FileIdentity
	file, id, openErr := p.root.OpenFile(ctx, installPreviousStateName, 0600)
	if openErr == nil {
		previous = &id
		if err = file.Close(); err != nil {
			return err
		}
	} else if !errors.Is(openErr, unix.ENOENT) {
		return openErr
	}
	_, err = p.root.WriteFileWithIdentity(ctx, installPreviousStateName, b, 0600, previous)
	return err
}

func (p *unixInstallControl) publishState(ctx context.Context, previous string, next []byte) (publication InstallPublication, err error) {
	if _, err = p.readMatchingState(ctx, previous); err != nil {
		return publication, err
	}
	identity, writeErr := p.root.WriteFileWithIdentity(ctx, installStateName, next, 0600, &p.stateID)
	if identity == nil {
		return publication, writeErr
	}
	publication.Published = true
	newState, newID, openErr := p.root.OpenFile(ctx, installStateName, 0600)
	if openErr != nil || newID != *identity {
		if newState != nil {
			_ = newState.Close()
		}
		return publication, errors.Join(writeErr, ErrInstallStateChanged, openErr)
	}
	old := p.state
	p.state, p.stateID = newState, newID
	closeErr := old.Close()
	if writeErr != nil || closeErr != nil {
		return publication, errors.Join(writeErr, closeErr)
	}
	if err = p.confirmState(ctx, installStateSHA256(next)); err != nil {
		return publication, err
	}
	publication.Durable = true
	return publication, nil
}

func (p *unixInstallControl) confirmState(ctx context.Context, expected string) error {
	if _, err := p.readMatchingState(ctx, expected); err != nil {
		return err
	}
	if err := p.state.Sync(); err != nil {
		return fmt.Errorf("sync held installation state: %w", err)
	}
	if err := p.root.Sync(ctx); err != nil {
		return fmt.Errorf("sync installation control directory: %w", err)
	}
	_, err := p.readMatchingState(ctx, expected)
	return err
}

func (p *unixInstallControl) probeOperation(ctx context.Context) (bool, error) {
	l, err := acquireInstallControlUnixLock(ctx, p.root, installOperationLockName)
	if errors.Is(err, ErrInstallControlBusy) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, l.close()
}

func (p *unixInstallControl) lockOperation(ctx context.Context) (installControlLockPlatform, error) {
	if p.validate != nil {
		if err := p.validate(ctx); err != nil {
			return nil, err
		}
	}
	return acquireInstallControlUnixLock(ctx, p.root, installOperationLockName)
}

func (p *unixInstallControl) lockStartup(ctx context.Context) (installControlLockPlatform, error) {
	return acquireInstallControlUnixLock(ctx, p.root, installStartupLockName)
}

func (p *unixInstallControl) close() error {
	var errs []error
	if p.state != nil {
		errs = append(errs, p.state.Close())
		p.state = nil
	}
	if p.root != nil {
		errs = append(errs, p.root.Close())
		p.root = nil
	}
	return errors.Join(errs...)
}

func (p *unixInstallControl) validateControl(ctx context.Context) error {
	if p.root == nil || p.state == nil {
		return os.ErrClosed
	}
	if err := p.root.verify(); err != nil {
		return err
	}
	file, id, err := p.root.OpenFile(ctx, installStateName, 0600)
	if err != nil {
		return errors.Join(ErrInstallStateChanged, err)
	}
	closeErr := file.Close()
	if id != p.stateID || closeErr != nil {
		return errors.Join(ErrInstallStateChanged, closeErr)
	}
	if p.validate != nil {
		return p.validate(ctx)
	}
	return nil
}

func (p *unixInstallControl) readMatchingState(ctx context.Context, expected string) ([]byte, error) {
	b, err := p.readState(ctx)
	if err != nil {
		return nil, err
	}
	if installStateSHA256(b) != expected {
		return nil, ErrInstallStateChanged
	}
	return b, nil
}

func acquireInstallControlUnixLock(ctx context.Context, root *TrustedRoot, name string) (_ *unixInstallControlLock, err error) {
	owned, err := cloneTrustedRoot(ctx, root)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, owned.Close())
		}
	}()
	finish, err := owned.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	parent := owned.chain[len(owned.chain)-1].fd
	fd, err := owned.backend.openFile(parent, name, unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, fmt.Errorf("%w: missing %s", ErrInstallControlUninitialized, name)
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, owned.backend.close(fd))
		}
	}()
	n, err := owned.checkFile(fd, 0600)
	if err != nil {
		return nil, err
	}
	if err = owned.checkFileName(parent, name, n); err != nil {
		return nil, err
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrInstallControlBusy
		}
		return nil, err
	}
	lock := &rootLock{root: owned, fd: fd, name: name, node: n}
	return &unixInstallControlLock{root: owned, lock: lock}, nil
}

func (l *unixInstallControlLock) checkHeld() error {
	if l == nil || l.lock == nil || l.root == nil {
		return os.ErrClosed
	}
	return l.lock.validate()
}

func (l *unixInstallControlLock) close() error {
	if l == nil {
		return nil
	}
	var errs []error
	if l.lock != nil {
		errs = append(errs, l.lock.close())
		l.lock = nil
	}
	if l.root != nil {
		errs = append(errs, l.root.Close())
		l.root = nil
	}
	return errors.Join(errs...)
}

func readInstallControlUnixFile(file *os.File) ([]byte, error) {
	if file == nil {
		return nil, os.ErrClosed
	}
	b := make([]byte, installStateMaxBytes+1)
	n, err := file.ReadAt(b, 0)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if n > installStateMaxBytes {
		return nil, errors.Join(ErrInstallStateTooLarge, err)
	}
	return b[:n], err
}

func cleanupInstallControlUnixTemp(_ context.Context, base, temp *TrustedRoot, tempName string) error {
	if temp == nil {
		return nil
	}
	var errs []error
	for _, name := range []string{installOperationLockName, installStartupLockName, installStateName} {
		file, id, err := temp.OpenFile(context.Background(), name, 0600)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, file.Close(), temp.RemoveFile(context.Background(), name, 0600, id))
	}
	errs = append(errs, temp.Close())
	if joined := errors.Join(errs...); joined != nil {
		return joined
	}
	if err := base.RemoveEmptyDir(context.Background(), tempName); err != nil && !errors.Is(err, unix.ENOENT) {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
