//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/update"
)

// The caller must have accepted raw through the existing offline digest authority.
// Copy those exact bytes into the selected trusted parent's private staging; never
// execute the request's source path or infer a version from its untrusted tag.
func verifyOfflineReplacementTag(ctx context.Context, parentPath string, raw []byte, tag string) (err error) {
	parent, err := platform.OpenTrustedParent(ctx, parentPath, 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	name := ".mihari-version-" + (&InstallTransaction{}).newTransactionID()
	stage, err := parent.OpenDir(ctx, name, platform.RootPolicy{Owner: 0, Mode: 0700, AllowCreate: true})
	if err != nil {
		return err
	}
	var candidateID *platform.FileIdentity
	defer func() {
		cleanup := context.WithoutCancel(ctx)
		if candidateID != nil {
			err = errors.Join(err, stage.RemoveFile(cleanup, "candidate", 0755, *candidateID))
		}
		err = errors.Join(err, stage.Close(), parent.RemoveEmptyDir(cleanup, name))
	}()
	candidateID, err = stage.WriteFileWithIdentity(ctx, "candidate", raw, 0755, nil)
	if err != nil {
		return err
	}
	target, err := update.ObserveReplacementTarget(ctx, "candidate", filepath.Join(parentPath, name, "candidate"), nil)
	if err != nil {
		return err
	}
	if target.SHA256 != sha256HexBytes(raw) || target.Version == "" || target.Version != tag {
		return migrateData("offline candidate version does not match release tag")
	}
	return nil
}
