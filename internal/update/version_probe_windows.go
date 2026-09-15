package update

import (
	"context"
	"errors"
	"os/exec"

	"github.com/mihari-proxy/mihari/internal/platform"
)

// A user probe binds the observation policy and child process to one held token.
type userVersionProbe interface {
	Observe(context.Context, string) (platform.ReplacementFile, error)
	Run(*exec.Cmd) error
	Close() error
}

func observeUserReplacementTarget(ctx context.Context, target ReplacementTarget) (ReplacementTarget, error) {
	return observeUserReplacementTargetWith(ctx, target, func() (userVersionProbe, error) {
		return platform.OpenWindowsUserVersionProbe()
	})
}

func observeUserReplacementTargetWith(ctx context.Context, target ReplacementTarget, open func() (userVersionProbe, error)) (out ReplacementTarget, err error) {
	if err = ctx.Err(); err != nil {
		return ReplacementTarget{}, err
	}
	probe, err := open()
	if err != nil {
		// Inability to drop privileges is unknown compatibility, never permission
		// to run the user-writable target with the updater's elevated token.
		target.probeErr = err
		return target, ctx.Err()
	}
	defer func() { err = errors.Join(err, probe.Close()) }()
	observe := func(ctx context.Context, path string) (platform.ReplacementFile, error) {
		file, err := probe.Observe(ctx, path)
		if err != nil {
			return file, err
		}
		if file.Path != target.Path || file.FileID != target.FileID || file.SHA256 != target.SHA256 || !file.Exists {
			return platform.ReplacementFile{}, replacementChanged()
		}
		return file, nil
	}
	return observeReplacementTarget(ctx, target.Roles[0], target.Path, userVersionRunner{probe}, observe)
}

type userVersionRunner struct{ probe userVersionProbe }

func (r userVersionRunner) RunVersion(ctx context.Context, executable, dir string) ([]byte, error) {
	return runVersionProbe(ctx, executable, dir, r.probe.Run)
}
