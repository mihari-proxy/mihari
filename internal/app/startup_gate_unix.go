//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
)

// InspectUnixActivation reads the ordinary startup gate without taking an
// install lock or creating B/D. Installer-started activated children can run
// while their parent still owns the install transaction.
func InspectUnixActivation(ctx context.Context, layout platform.ResolvedLayout) (phase string, present bool, err error) {
	return inspectUnixActivation(ctx, layout, false)
}

// InspectUnixServiceActivation requires root and the selected instance's target
// authority in the fixed system journal. The marker itself grants no authority.
func InspectUnixServiceActivation(ctx context.Context, layout platform.ResolvedLayout) (phase string, present bool, err error) {
	return inspectUnixActivation(ctx, layout, true)
}

func inspectUnixActivation(ctx context.Context, layout platform.ResolvedLayout, service bool) (phase string, present bool, err error) {
	owner := uint32(os.Geteuid())
	defaults := platform.LayoutDefaults{}
	if service {
		defaults = platform.SystemLayoutDefaults()
	}
	journalRoot, mode, err := startupJournalScope(layout, service, owner, defaults)
	if err != nil {
		return "", false, err
	}
	if mode == 0711 {
		owner = 0
	}
	root, err := platform.OpenTrustedRoot(ctx, journalRoot, platform.RootPolicy{Owner: owner, Mode: mode})
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	file, _, err := root.OpenFile(ctx, installJournalFileName, 0600)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	journal, err := DecodeJournal(file)
	if err != nil {
		return "", true, err
	}
	phase, err = startupJournalPhase(journal, layout)
	if err != nil {
		return phase, true, err
	}
	if service || os.Geteuid() == 0 {
		binary, binaryErr := os.Executable()
		if binaryErr != nil {
			return "", true, binaryErr
		}
		hash, hashErr := trustedValidationBinaryHash(ctx, binary)
		if hashErr != nil {
			return "", true, hashErr
		}
		if hash != journal.CandidateHash {
			return "", true, installBusy("daemon binary does not match activation")
		}
	}
	return phase, true, nil
}

// RunUnixStartup selects an already activated daemon or the greenfield
// validation/bootstrap lifecycle. Ordinary startup never reacquires install.
func RunUnixStartup(ctx context.Context, layout platform.ResolvedLayout, discover func(context.Context) (bool, error), run func(context.Context, string) error) (err error) {
	defer func() { err = ClassifyUnixLocalError(err) }()
	return runUnixStartup(ctx, os.Geteuid() == 0, func(ctx context.Context) (string, bool, error) { return InspectUnixActivation(ctx, layout) }, func(ctx context.Context) error { return RunUnixForeground(ctx, layout, discover, run) }, run)
}

// RunUnixSystemService starts only an activated installed target. It never
// acquires an install lease or falls back to private foreground bootstrap.
func RunUnixSystemService(ctx context.Context, layout platform.ResolvedLayout, run func(context.Context, string) error) (err error) {
	defer func() { err = ClassifyUnixLocalError(err) }()
	return runUnixServiceStartup(ctx, func(ctx context.Context) (string, bool, error) { return InspectUnixServiceActivation(ctx, layout) }, run)
}
