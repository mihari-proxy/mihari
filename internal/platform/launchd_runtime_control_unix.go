//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const (
	launchdRuntimeDirName   = "launchd-runtime"
	launchdRuntimeLockName  = "start.lock"
	launchdRuntimeStateName = "state.json"
)

type unixLaunchdRuntimeControl struct {
	store *unixInstallControl
}

func (p *unixLaunchdRuntimeControl) readState(ctx context.Context) ([]byte, error) {
	return p.store.readState(ctx)
}
func (p *unixLaunchdRuntimeControl) publishState(ctx context.Context, previous string, next []byte) (InstallPublication, error) {
	return p.store.publishState(ctx, previous, next)
}
func (p *unixLaunchdRuntimeControl) confirmState(ctx context.Context, expected string) error {
	return p.store.confirmState(ctx, expected)
}
func (p *unixLaunchdRuntimeControl) lockStartup(ctx context.Context) (installControlLockPlatform, error) {
	return acquireInstallControlUnixLock(ctx, p.store.root, launchdRuntimeLockName)
}
func (p *unixLaunchdRuntimeControl) close() error {
	if p == nil || p.store == nil {
		return nil
	}
	return p.store.close()
}

func openUnixLaunchdRuntimeControlAt(ctx context.Context, base *TrustedRoot) (_ *LaunchdRuntimeControl, err error) {
	if base == nil {
		return nil, os.ErrInvalid
	}
	root, err := base.OpenDir(ctx, launchdRuntimeDirName, RootPolicy{Owner: base.policy.Owner, Mode: 0700})
	if err != nil {
		return nil, err
	}
	store := &unixInstallControl{root: root}
	p := &unixLaunchdRuntimeControl{store: store}
	defer func() {
		if err != nil {
			err = errors.Join(err, p.close())
		}
	}()
	lock, _, openErr := root.OpenFile(ctx, launchdRuntimeLockName, 0600)
	if errors.Is(openErr, unix.ENOENT) {
		return nil, fmt.Errorf("%w: missing %s", ErrLaunchdRuntimeUninitialized, launchdRuntimeLockName)
	}
	if openErr != nil {
		return nil, openErr
	}
	if err = lock.Close(); err != nil {
		return nil, err
	}
	store.state, store.stateID, err = root.OpenFile(ctx, launchdRuntimeStateName, 0600)
	if errors.Is(err, unix.ENOENT) {
		return nil, fmt.Errorf("%w: missing %s", ErrLaunchdRuntimeUninitialized, launchdRuntimeStateName)
	}
	if err != nil {
		return nil, err
	}
	return newLaunchdRuntimeControl(p), nil
}

func initializeUnixLaunchdRuntimeControlAt(ctx context.Context, base *TrustedRoot, initial []byte, validate func(context.Context) error) (_ *LaunchdRuntimeControl, publication InstallPublication, err error) {
	if base == nil {
		return nil, publication, os.ErrInvalid
	}
	if len(initial) > launchdRuntimeStateMaxBytes {
		return nil, publication, ErrLaunchdRuntimeStateTooLarge
	}
	if validate != nil {
		if err = validate(ctx); err != nil {
			return nil, publication, err
		}
	}
	if existing, openErr := base.OpenDir(ctx, launchdRuntimeDirName, RootPolicy{Owner: base.policy.Owner, Mode: 0700}); openErr == nil {
		if closeErr := existing.Close(); closeErr != nil {
			return nil, publication, closeErr
		}
		control, openErr := openUnixLaunchdRuntimeControlAt(ctx, base)
		return control, publication, openErr
	} else if !errors.Is(openErr, unix.ENOENT) {
		return nil, publication, openErr
	}

	var temp *TrustedRoot
	var tempName string
	for i := 0; i < 100; i++ {
		tempName, err = randomTempName(".launchd-runtime-*")
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
			err = errors.Join(err, cleanupLaunchdRuntimeUnixTemp(base, temp, tempName))
		}
	}()
	for _, item := range []struct {
		name string
		body []byte
	}{
		{name: launchdRuntimeLockName},
		{name: launchdRuntimeStateName, body: initial},
	} {
		if err = temp.WriteFile(ctx, item.name, item.body, 0600, nil); err != nil {
			return nil, publication, err
		}
	}
	if err = temp.Sync(ctx); err != nil {
		return nil, publication, err
	}
	if validate != nil {
		if err = validate(ctx); err != nil {
			return nil, publication, err
		}
	}
	published, err = base.MoveDirTo(ctx, tempName, temp, base, launchdRuntimeDirName)
	publication.Published = published
	if err != nil && !published && errors.Is(err, unix.EEXIST) {
		err = nil
		control, openErr := openUnixLaunchdRuntimeControlAt(ctx, base)
		return control, publication, openErr
	}
	if err != nil {
		return nil, publication, err
	}
	if validate != nil {
		if err = validate(ctx); err != nil {
			return nil, publication, err
		}
	}
	control, err := openUnixLaunchdRuntimeControlAt(ctx, base)
	if err != nil {
		return nil, publication, err
	}
	state, digest, readErr := control.Read(ctx)
	if readErr != nil || digest != installStateSHA256(initial) || string(state) != string(initial) {
		_ = control.Close()
		return nil, publication, errors.Join(ErrInstallStateChanged, readErr)
	}
	if err = control.ConfirmState(ctx, digest); err != nil {
		_ = control.Close()
		return nil, publication, err
	}
	publication.Durable = true
	return control, publication, nil
}

func cleanupLaunchdRuntimeUnixTemp(base, temp *TrustedRoot, tempName string) error {
	if temp == nil {
		return nil
	}
	var errs []error
	for _, name := range []string{launchdRuntimeLockName, launchdRuntimeStateName} {
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
