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

func TestChannelLease_PrivateMaintenanceOnlyOwnsP(t *testing.T) {
	root := leaseTempDir(t)
	defaults := platformLayoutDefaults("")
	layout := ResolvedLayout{Mode: PrivateMode, BaseDir: root, Data: NewPaths(root), ControlEndpoint: filepath.Join(root, "control.sock")}
	calls := 0
	open := func(ctx context.Context, path string, p RootPolicy, parent bool) (*TrustedRoot, error) {
		if path == defaults.BaseDir && os.Geteuid() == 0 {
			if p.AllowCreate || !parent {
				t.Fatal("root private maintenance tried to create machine B")
			}
			return nil, os.ErrNotExist
		}
		if path != root {
			t.Fatalf("private maintenance accessed machine B: %s", path)
		}
		calls++
		return leaseFixtureOpen(ctx, path, p, parent)
	}
	first, err := acquireChannelLease(context.Background(), layout, uint32(os.Geteuid()), defaults, open)
	if err != nil {
		t.Fatal(err)
	}
	defer assertTestClose(t, first.Close)
	second, err := acquireChannelLease(context.Background(), layout, uint32(os.Geteuid()), defaults, open)
	if second != nil {
		assertTestClose(t, second.Close)
	}
	if !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("channel and installer lock did not serialize: %v", err)
	}
	if calls != 2 {
		t.Fatalf("opens=%d", calls)
	}
	if _, err := os.Stat(filepath.Join(root, "install.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "data")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("channel created D: %v", err)
	}
}

func TestChannelLease_PrivateSystemAliasRejectedBeforeLock(t *testing.T) {
	root := leaseTempDir(t)
	defaults := platformLayoutDefaults("")
	defaults.BaseDir = filepath.Join(root, "unrelated-spelling")
	privatePath := filepath.Join(root, "private-spelling")
	layout := ResolvedLayout{Mode: PrivateMode, BaseDir: privatePath, Data: NewPaths(privatePath), ControlEndpoint: filepath.Join(privatePath, "control.sock")}
	open := func(ctx context.Context, path string, policy RootPolicy, parent bool) (*TrustedRoot, error) {
		if path == defaults.BaseDir && (policy.AllowCreate || !parent) {
			t.Fatal("alias comparison tried to create machine B")
		}
		fd, err := unix.Open(root, trustedDirFlags, 0)
		if err != nil {
			return nil, err
		}
		backend := leasePrivilegedModel{}
		node, err := backend.stat(fd)
		if err != nil {
			return nil, errors.Join(err, backend.close(fd))
		}
		return &TrustedRoot{backend: backend, policy: policy, path: path, parentOnly: parent, chain: []trustedLink{{fd: fd, node: node, application: true}}}, nil
	}
	l, err := acquireChannelLease(context.Background(), layout, 0, defaults, open)
	if l != nil {
		assertTestClose(t, l.Close)
	}
	if !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("private alias of B accepted: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "install.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("alias acquired install.lock before rejection: %v", err)
	}
}

func TestChannelLease_OrdinarySystemDeniedBeforeIO(t *testing.T) {
	defaults := platformLayoutDefaults("")
	layout, err := ResolveLayout(LayoutInput{EUID: 1000}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	_, err = acquireChannelLease(context.Background(), layout, 1000, defaults, func(context.Context, string, RootPolicy, bool) (*TrustedRoot, error) {
		t.Fatal("ordinary system maintenance performed IO")
		return nil, nil
	})
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("permission=%v", err)
	}
}
