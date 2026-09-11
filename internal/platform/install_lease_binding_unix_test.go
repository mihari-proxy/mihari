//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

type bindingFailureBackend struct {
	leasePrivilegedModel
	fail *bool
}

func (b bindingFailureBackend) checkFS(fd int) error {
	if *b.fail {
		return os.ErrPermission
	}
	return b.leasePrivilegedModel.checkFS(fd)
}

type bindingSyncBackend struct {
	leasePrivilegedModel
	fail *bool
}

func (b bindingSyncBackend) sync(fd int) error {
	if err := b.leasePrivilegedModel.sync(fd); err != nil {
		return err
	}
	*b.fail = true
	return nil
}

func TestInstallLease_BindFailureRestoresPreviousScope(t *testing.T) {
	base, privatePath := leaseTempDir(t), leaseTempDir(t)
	if err := os.Chmod(base, 0711); err != nil {
		t.Fatal(err)
	}
	defaults := platformLayoutDefaults("")
	defaults.BaseDir = base
	previous := ResolvedLayout{Mode: SystemMode, BaseDir: base, Data: NewPaths(filepath.Join(base, "data")), ControlEndpoint: filepath.Join(base, "control.sock")}
	next := ResolvedLayout{Mode: PrivateMode, BaseDir: privatePath, Data: NewPaths(privatePath), ControlEndpoint: filepath.Join(privatePath, "control.sock")}
	fail := false
	open := func(ctx context.Context, path string, policy RootPolicy, parent bool) (*TrustedRoot, error) {
		fd, err := unix.Open(path, trustedDirFlags, 0)
		if err != nil {
			return nil, err
		}
		var backend trustedBackend = bindingFailureBackend{fail: &fail}
		if path == privatePath {
			backend = bindingSyncBackend{fail: &fail}
		}
		node, err := backend.stat(fd)
		if err != nil {
			return nil, errors.Join(err, backend.close(fd))
		}
		return &TrustedRoot{backend: backend, policy: policy, path: path, parentOnly: parent, chain: []trustedLink{{fd: fd, node: node, application: true}}}, nil
	}
	l, err := acquireInstallLease(context.Background(), previous, 0, defaults, open)
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, l.Close)
	err = l.bindInstallLayout(context.Background(), next, defaults, open)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("post-acquisition verification did not fail: %v", err)
	}
	if l.state.layout != previous || len(l.state.roots) != 1 || len(l.state.locks) != 1 {
		t.Fatal("failed bind retained a partial private scope")
	}
	fail = false
	if err := l.Validate(context.Background(), previous, true); err != nil {
		t.Fatalf("previous lease scope was not preserved: %v", err)
	}
	root, err := open(context.Background(), privatePath, RootPolicy{Mode: 0700}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, root.Close)
	lock, err := acquireRootLock(context.Background(), root, "install.lock")
	if err != nil {
		t.Fatalf("failed bind leaked the private install lock: %v", err)
	}
	assertTestClose(t, lock.close)
}
