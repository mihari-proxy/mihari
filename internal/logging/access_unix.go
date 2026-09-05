//go:build !windows

package logging

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/platform"
)

// Unix uses the existing private-file ownership policy; Windows ACL migration
// does not enumerate or change other log sequences here.
func repairExistingLogAccess(ctx context.Context, _ *platform.PrivateFS, _ string) error {
	return ctx.Err()
}
