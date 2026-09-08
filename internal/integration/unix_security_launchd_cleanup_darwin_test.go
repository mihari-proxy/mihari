//go:build darwin && unix_security

package integration

import (
	"context"
	"errors"
	"os"
	"testing"
)

// confirmBootoutCleanup protects this hosted test fixture's teardown. These
// tests validate the harness contract, not a production repair implementation.
func confirmBootoutCleanup(ctx context.Context, pgid int, wait func(context.Context, int) error) error {
	// A zero intent can still have a live writer publishing its group after
	// bootout. Keep the ledger for a later recovery pass instead of certifying it.
	if pgid <= 1 {
		return os.ErrPermission
	}
	return wait(ctx, pgid)
}

func TestSecurityLaunchdCleanupRejectsUnpublishedProcessGroup(t *testing.T) {
	wait := func(context.Context, int) error {
		t.Fatal("unknown group reached the native observer")
		return nil
	}
	if err := confirmBootoutCleanup(context.Background(), 0, wait); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unpublished process group was accepted as cleaned up: %v", err)
	}
}

func TestSecurityLaunchdCleanupPropagatesGroupProof(t *testing.T) {
	want := errors.New("group is still present")
	calls := 0
	err := confirmBootoutCleanup(context.Background(), 42, func(_ context.Context, pgid int) error {
		calls++
		if pgid != 42 {
			t.Fatalf("observed group=%d", pgid)
		}
		return want
	})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("group proof err=%v calls=%d", err, calls)
	}
}
