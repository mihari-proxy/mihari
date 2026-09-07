//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestChannelLease_PrivateMaintenanceOnlyOwnsP(t *testing.T) {
	root := leaseTempDir(t)
	defaults := platformLayoutDefaults("")
	layout := ResolvedLayout{Mode: PrivateMode, BaseDir: root, Data: NewPaths(root), ControlEndpoint: filepath.Join(root, "control.sock")}
	calls := 0
	open := func(ctx context.Context, path string, p RootPolicy, parent bool) (*TrustedRoot, error) {
		calls++
		if path != root {
			t.Fatalf("private maintenance accessed machine B: %s", path)
		}
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
