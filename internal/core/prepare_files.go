package core

import (
	"context"
	"fmt"
	"path/filepath"
)

func (i Installer) prepareFileUpdate(ctx context.Context, target ReleaseTarget, request InstallRequest) (prepared PreparedCore, resultErr error) {
	store, ok := i.Updates.(*fileUpdateStore)
	if !ok {
		return nil, dataFailure("ordinary core update store unavailable")
	}
	rel, _, err := fileUpdatePath(InstalledBinary, "")
	if err != nil {
		return nil, err
	}
	if filepath.Clean(request.BinaryPath) != filepath.Join(store.path, rel) || filepath.Clean(request.DataDir) != store.path || filepath.Clean(request.ConfigPath) != filepath.Join(store.path, "runtime", "config.yaml") {
		return nil, dataFailure("core update paths disagree with data root")
	}
	goos, arch := store.target()
	if goos != target.goos || arch != target.goarch {
		return nil, dataFailure("core candidate platform mismatch")
	}
	archive, err := i.readTargetArchive(ctx, target)
	if err != nil {
		return nil, err
	}
	binary, err := archiveBinary(archive, target.asset.Name)
	if err != nil {
		return nil, err
	}
	candidate, err := i.stageUpdateCandidate(ctx, store, target, binary)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			candidate.Cleanup()
		}
	}()
	p := candidate.protected
	rel, _, err = fileUpdatePath(UpdateCandidate, p.transaction)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(store.path, rel)
	check := func() error {
		actual, err := store.Inspect(ctx, UpdateCandidate, p.transaction)
		if err != nil {
			return err
		}
		if !sameObject(actual, p.binary) {
			return dataFailure("validated core candidate changed")
		}
		return nil
	}
	if err = check(); err != nil {
		return nil, err
	}
	version, err := DetectVersion(ctx, i.Runner, path)
	if err != nil {
		return nil, err
	}
	if version != candidate.version {
		return nil, dataFailureCause("mihomo candidate version does not match selected release", fmt.Errorf("selected %s, candidate reported %s", candidate.version, version))
	}
	if err = check(); err != nil {
		return nil, err
	}
	if err = ValidateConfig(ctx, i.Runner, path, request.DataDir, request.ConfigPath); err != nil {
		return nil, err
	}
	if err = check(); err != nil {
		return nil, err
	}
	return candidate, nil
}
