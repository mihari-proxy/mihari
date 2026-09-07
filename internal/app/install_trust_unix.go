//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"os"

	"github.com/mihari-proxy/mihari/internal/platform"
)

// loadUnixOfflineTrust accepts a constructor-selected trusted location, never a
// request/source/adjacent checksum path. Absence permits the official TLS path.
func loadUnixOfflineTrust(ctx context.Context, path string) (trust migrationTrust, err error) {
	trust = compiledInstallerTrust()
	root, err := platform.OpenTrustedParent(ctx, path, 0)
	if errors.Is(err, os.ErrNotExist) {
		return trust, nil
	}
	if err != nil {
		return trust, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	read := func(ctx context.Context, name string, limit int64) (raw []byte, err error) {
		file, _, err := root.OpenFile(ctx, name, 0644)
		if err != nil {
			return nil, err
		}
		defer func() { err = errors.Join(err, file.Close()) }()
		return readInstallFile(ctx, file, limit)
	}
	raw, err := read(ctx, "manifest.json", MaxInstallJournalBytes)
	if err != nil {
		return trust, err
	}
	return decodeOfflineTrust(ctx, raw, read)
}
