//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func cleanupBinaryUpdates(ctx context.Context, binary string) (resultErr error) {
	// Binary-only Unix stages are root-owned; ordinary clients have no authority.
	if os.Geteuid() != 0 {
		return nil
	}
	dir := filepath.Dir(binary)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	found := false
	for _, entry := range entries {
		if id, ok := strings.CutPrefix(entry.Name(), ".mihari-update-"); ok && validTransactionID(id) {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	// Never delay startup indefinitely behind an in-progress binary-only update.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	lease, err := platform.AcquireBinaryLease(ctx, dir)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, lease.Close()) }()
	parent, err := platform.OpenTrustedParent(ctx, dir, 0)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, parent.Close()) }()
	if err := lease.Validate(ctx, dir); err != nil {
		return err
	}
	return cleanupUnixBinaryStages(ctx, parent)
}
