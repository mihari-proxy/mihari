//go:build linux || darwin

package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/update"
)

type unixBinaryTarget struct {
	verifiedCandidate      []byte
	lease                  *platform.OwnedBinaryLease
	parent, stage          *platform.TrustedRoot
	currentID, candidateID platform.FileIdentity
	currentName, stageName string
}

func openUnixBinaryTarget(ctx context.Context, binary string) (target *unixBinaryTarget, err error) {
	if os.Geteuid() != 0 {
		return nil, os.ErrPermission
	}
	lease, err := platform.AcquireBinaryLease(ctx, filepath.Dir(binary))
	if err != nil {
		return nil, err
	}
	target = &unixBinaryTarget{lease: lease, currentName: filepath.Base(binary)}
	owned := target
	defer func() {
		if err != nil {
			err = errors.Join(err, owned.Close())
		}
	}()
	target.parent, err = platform.OpenTrustedParent(ctx, filepath.Dir(binary), 0)
	if err != nil {
		return nil, err
	}
	file, id, err := target.parent.OpenFile(ctx, target.currentName, 0755)
	if err != nil {
		return nil, err
	}
	target.currentID = id
	if err = file.Close(); err != nil {
		return nil, err
	}

	return target, nil
}

func (t *unixBinaryTarget) Stage(ctx context.Context, req InstallRequest) (err error) {
	if err = cleanupUnixBinaryStages(ctx, t.parent); err != nil {
		return err
	}
	t.stageName = ".mihari-update-" + (&InstallTransaction{}).newTransactionID()
	t.stage, err = t.parent.OpenDir(ctx, t.stageName, platform.RootPolicy{Owner: 0, Mode: 0700, AllowCreate: true})
	if err != nil {
		return err
	}

	raw := t.verifiedCandidate
	digest := req.ArtifactSHA256
	if raw == nil {
		digest, err = (update.OfficialReleaseSource{}).Checksum(ctx, req.ReleaseTag, "mihari-"+runtime.GOOS+"-"+runtime.GOARCH)
		if err != nil {
			return err
		}
		raw, err = readHostFile(req.Binary, migrationBinaryMax)
		if err != nil {
			return err
		}
	}
	if sha256HexBytes(raw) != digest {
		return migrateState("untrusted install binary")
	}
	if err = t.stage.WriteFile(ctx, "candidate", raw, 0755, nil); err != nil {
		return err
	}
	file, id, err := t.stage.OpenFile(ctx, "candidate", 0755)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	got, err := hashReader(io.LimitReader(file, migrationBinaryMax+1), migrationBinaryMax+1)
	if err != nil {
		return err
	}
	if got != digest {
		return migrateState("candidate identity changed")
	}
	t.candidateID = id
	_, stageID, _, _, err := t.stage.Snapshot(ctx)
	if err != nil {
		return err
	}
	marker, err := json.Marshal(binaryStageMarker{Schema: binaryStageSchema, StageIdentity: stageID, CandidateIdentity: id.Key(), SHA256: digest})
	if err != nil {
		return err
	}
	return t.stage.WriteFile(ctx, "identity.json", marker, 0600, nil)
}

func (t *unixBinaryTarget) Publish(ctx context.Context) (bool, error) {
	path, _, _, _, err := t.parent.Snapshot(ctx)
	if err != nil {
		return false, err
	}
	if err = t.lease.Validate(ctx, path); err != nil {
		return false, err
	}
	return t.stage.MoveFileToPublished(ctx, "candidate", t.candidateID, t.parent, t.currentName, 0755, &t.currentID)
}

func (t *unixBinaryTarget) Close() (err error) {
	if t == nil {
		return nil
	}
	if t.stage != nil {
		file, id, readErr := t.stage.OpenFile(context.Background(), "candidate", 0755)
		if readErr == nil {
			err = errors.Join(err, file.Close(), t.stage.RemoveFile(context.Background(), "candidate", 0755, id))
		} else if !errors.Is(readErr, os.ErrNotExist) {
			err = errors.Join(err, readErr)
		}
		marker, markerID, markerErr := t.stage.OpenFile(context.Background(), "identity.json", 0600)
		if markerErr == nil {
			err = errors.Join(err, marker.Close(), t.stage.RemoveFile(context.Background(), "identity.json", 0600, markerID))
		} else if !errors.Is(markerErr, os.ErrNotExist) {
			err = errors.Join(err, markerErr)
		}
		err = errors.Join(err, t.stage.Close())
		t.stage = nil
		if t.parent != nil {
			err = errors.Join(err, t.parent.RemoveEmptyDir(context.Background(), t.stageName))
		}
	}
	if t.parent != nil {
		err = errors.Join(err, t.parent.Close())
		t.parent = nil
	}
	if t.lease != nil {
		err = errors.Join(err, t.lease.Close())
		t.lease = nil
	}
	return err
}
