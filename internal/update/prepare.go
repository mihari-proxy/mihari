package update

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

// PreparedUpdate owns a verified download until Apply or Close consumes it.
// Fields describe a candidate, not authority for privileged installation.
type PreparedUpdate struct {
	Version, CandidatePath, SHA256, Channel string
	Available, Ahead                        bool
	cleanup                                 func() error
}

// Close releases the prepared candidate. It is safe to call repeatedly.
func (p *PreparedUpdate) Close() error {
	if p.cleanup != nil {
		return p.cleanup()
	}
	return nil
}

// Prepare downloads and verifies a fixed release without replacing binaryPath.
func (u SelfUpdater) Prepare(ctx context.Context, binaryPath, currentVersion, channel string) (PreparedUpdate, error) {
	ch, err := normalizeChannel(channel)
	if err != nil {
		return PreparedUpdate{}, err
	}
	release, err := u.latestRelease(ctx, ch)
	if err != nil {
		return PreparedUpdate{}, err
	}
	available, ahead := classifyUpdate(currentVersion, release.TagName)
	result := PreparedUpdate{Version: release.TagName, Channel: ch, Available: available, Ahead: ahead}
	if !available {
		return result, nil
	}
	tag := release.TagName
	if _, ok := parseCanonicalTag(tag); !ok {
		return PreparedUpdate{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid fixed release tag"}
	}
	release, err = u.releaseByTag(ctx, tag)
	if err != nil {
		return PreparedUpdate{}, err
	}
	if release.TagName != tag || release.Draft {
		return PreparedUpdate{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "fixed release identity changed"}
	}
	asset, err := SelectSelfAsset(release, u.targetOS(), u.targetArch())
	if err != nil {
		return PreparedUpdate{}, err
	}
	if asset.Size < 0 || asset.Size > maxSelfBinarySize {
		return PreparedUpdate{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihari asset is too large"}
	}
	checksums, err := selectChecksumAsset(release)
	if err != nil {
		return PreparedUpdate{}, err
	}
	expected, err := u.fetchExpectedChecksum(ctx, checksums, asset.Name)
	if err != nil {
		return PreparedUpdate{}, err
	}
	workspace, err := os.MkdirTemp("", "mihari-prepare-")
	if err != nil {
		return PreparedUpdate{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "create update workspace"}
	}
	var once sync.Once
	var closeErr error
	result.cleanup = func() error { once.Do(func() { closeErr = os.RemoveAll(workspace) }); return closeErr }
	result.CandidatePath = filepath.Join(workspace, "candidate")
	// Prepare produces inert bytes. Privileged Apply independently verifies and
	// recopies them through no-follow descriptors before any executable publication.
	if u.openCandidate == nil {
		u.openCandidate = func(path string) (io.WriteCloser, error) {
			return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		}
	}
	if err = u.download(ctx, asset, expected, result.CandidatePath); err != nil {
		return PreparedUpdate{}, errors.Join(err, result.Close())
	}
	result.SHA256 = hex.EncodeToString(expected[:])
	return result, nil
}
