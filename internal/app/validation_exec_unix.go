//go:build linux || darwin

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ConfigureUnixValidation selects the real anonymous-pipe child and captures
// the installer identity. It does not select a layout or install a service.
func ConfigureUnixValidation(ctx context.Context, x *InstallTransaction) error {
	if os.Geteuid() != 0 {
		return errValidationNotRoot
	}
	id, err := identifyValidationProcess(ctx, os.Getpid())
	if err != nil {
		return err
	}
	x.EUID = 0
	x.ParentIdentity = id
	x.Validation = unixValidationChild{}
	x.NewPipe = func() (ValidationLease, ValidationLease) {
		p, c, err := newUnixValidationPipes()
		if err != nil {
			return nil, nil
		}
		return p, c
	}
	return nil
}

type unixValidationChild struct{}

func (unixValidationChild) Start(ctx context.Context, r ValidationStart) (ValidationSession, error) {
	if os.Geteuid() != 0 {
		return nil, errValidationNotRoot
	}
	pipe, ok := r.Child.(*unixPipeLease)
	if !ok || pipe.file == nil {
		return nil, errMissingValidationPipe
	}
	identity, err := identifyValidationProcess(ctx, os.Getpid())
	if err != nil {
		return nil, err
	}
	if !SameProcessStart(identity, r.Handshake.ParentIdentity) {
		return nil, errValidationHandshake
	}
	binary := filepath.Join(r.Journal.InstallPath, "mihari")
	hash, err := trustedValidationBinaryHash(ctx, binary)
	if err != nil {
		return nil, err
	}
	if hash != r.Journal.CandidateHash {
		return nil, errValidationHandshake
	}
	cmd := exec.Command(binary, "daemon", "--install-validation", r.Journal.TransactionID)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	cmd.ExtraFiles = []*os.File{pipe.file}
	closeDefaults, err := configureValidationTestDefaults(cmd)
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, errors.Join(installBusy("start validation daemon"), closeDefaults())
	}
	if err := closeDefaults(); err != nil {
		_ = cmd.Process.Kill()
		return nil, errors.Join(err, cmd.Wait())
	}
	// Parent must not retain the child's endpoint or parent death would not EOF.
	_ = r.Child.Close()
	s := newProcessValidationSession(r.Lease, cmd.Wait, func(ctx context.Context, id ProcessStartIdentity) error {
		return signalValidationProcess(ctx, id, unix.SIGKILL)
	})
	s.identity, err = identifyValidationProcess(ctx, cmd.Process.Pid)
	if err != nil || !validProcessStart(s.identity) {
		_ = s.Close()
		_ = s.WaitLockRelease(context.WithoutCancel(ctx))
		return nil, errValidationHandshake
	}
	return s, nil
}

func trustedValidationBinaryHash(ctx context.Context, path string) (hashValue string, resultErr error) {
	root, err := platform.OpenTrustedRoot(ctx, filepath.Dir(path), platform.RootPolicy{Owner: 0, Mode: 0755})
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	file, _, err := root.OpenFile(ctx, filepath.Base(path), 0755)
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, migrationBinaryMax+1))
	if err != nil || n > migrationBinaryMax {
		return "", errValidationHandshake
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func signalValidationProcess(ctx context.Context, id ProcessStartIdentity, signal unix.Signal) error {
	live, err := identifyValidationProcess(ctx, id.PID)
	if err != nil {
		return err
	}
	if !SameProcessStart(id, live) {
		return nil
	}
	if err := unix.Kill(id.PID, signal); err != nil && !errors.Is(err, unix.ESRCH) {
		return err
	}
	return nil
}

func (unixValidationChild) ReapValidation(ctx context.Context, id ProcessStartIdentity, j InstallJournal) error {
	return recoverValidationProcess(ctx, id, j, unixValidationRecovery{})
}

type unixValidationRecovery struct{}

func (unixValidationRecovery) Identify(ctx context.Context, pid int) (ProcessStartIdentity, error) {
	return identifyValidationProcess(ctx, pid)
}
func (unixValidationRecovery) WaitLocks(ctx context.Context, j InstallJournal) error {
	return waitValidationLocks(ctx, j)
}
func (unixValidationRecovery) StopAndWait(ctx context.Context, id ProcessStartIdentity) error {
	for _, signal := range []unix.Signal{unix.SIGTERM, unix.SIGKILL} {
		live, err := identifyValidationProcess(ctx, id.PID)
		if err != nil {
			return err
		}
		if !SameProcessStart(id, live) {
			return nil
		}
		if err := signalValidationProcess(ctx, id, signal); err != nil {
			return err
		}
		timer := time.NewTimer(5 * time.Second)
		ticker := time.NewTicker(20 * time.Millisecond)
		exited := false
		for !exited {
			select {
			case <-ctx.Done():
				timer.Stop()
				ticker.Stop()
				return ctx.Err()
			case <-timer.C:
				exited = true
			case <-ticker.C:
				live, err = identifyValidationProcess(ctx, id.PID)
				if err != nil {
					timer.Stop()
					ticker.Stop()
					return err
				}
				if !SameProcessStart(id, live) {
					timer.Stop()
					ticker.Stop()
					return nil
				}
			}
		}
		timer.Stop()
		ticker.Stop()
	}
	return installBusy("validation process has not exited")
}
func waitValidationLocks(ctx context.Context, j InstallJournal) error {
	layout, err := validationLayout(j)
	if err != nil {
		return err
	}
	lease, err := platform.AcquireDaemonLease(ctx, layout)
	if err != nil {
		return err
	}
	return lease.Close()
}
