//go:build windows

package logging

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mihari-proxy/mihari/internal/platform"
)

type logAccessFS interface {
	ReadDir(string) ([]platform.FileEntry, error)
	RepairAccessChecked(string, platform.FileIdentity) error
}

// repairExistingLogAccess converges the fixed log namespace while holding the
// starting writer's lock, before opening its active log. An elevated updated TUI
// can repair all service sequences even when the service started first with the
// legacy Administrators-only policy.
func repairExistingLogAccess(ctx context.Context, fs logAccessFS, dir string) error {
	// Other sequences can rotate while only this writer's lock is held.
	// Re-enumerate changed identities; never apply an old listing to a replacement.
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := fs.ReadDir(dir)
		if err != nil {
			return err
		}
		changed := false
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !managedLogAccessName(entry.Name) || !entry.Mode.IsRegular() {
				continue
			}
			if err := fs.RepairAccessChecked(filepath.Join(dir, entry.Name), entry.Identity); err != nil {
				if errors.Is(err, platform.ErrIdentityMismatch) || errors.Is(err, os.ErrNotExist) {
					changed = true
					continue
				}
				// Fail closed: a namespace with unresolved ACLs must not be reported
				// as safely migrated. Earlier repairs remain applied; no content is lost.
				return fmt.Errorf("repair log access for %s: %w", entry.Name, err)
			}
		}
		if !changed {
			return nil
		}
	}
	return fmt.Errorf("log files kept changing during access repair")
}

func managedLogAccessName(name string) bool {
	for _, base := range []string{"mihari-daemon.log", "mihari-tui.log", "mihomo.log"} {
		if name == base || name == base+".lock" {
			return true
		}
		if _, ok := archiveSuffix(base, name); ok {
			return true
		}
	}
	return false
}
