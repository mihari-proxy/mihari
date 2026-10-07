package app

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mihari-proxy/mihari/internal/platform"
)

// removeUninstallCommandFile unlinks one PATH command entry. It must not
// follow a symlink or remove a directory. Tests replace it to force a failure.
var removeUninstallCommandFile = os.Remove

var errInstalledProgramMissing = errors.New("installed Mihari program is missing")
var errCommandNotComparable = errors.New("command file is not comparable")

type uninstallCommandPlan struct {
	path   string
	remove bool
	leave  error
}

func uninstallCommandPath() (string, error) {
	raw := os.Getenv("MIHARI_BIN")
	dir := strings.TrimSpace(raw)
	if dir == "" {
		dir = defaultUninstallCommandDirectory()
		raw = dir
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("MIHARI_BIN must be an absolute path: %s", raw)
	}
	return filepath.Join(dir, platform.InstalledBinaryName()), nil
}

func classifyUninstallCommand(installRoot string) (uninstallCommandPlan, error) {
	path, err := uninstallCommandPath()
	if err != nil {
		return uninstallCommandPlan{}, err
	}
	plan := uninstallCommandPlan{path: path}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return plan, nil
	}
	if err != nil {
		return uninstallCommandPlan{}, fmt.Errorf("inspect command file %s: %w", path, err)
	}
	if info.IsDir() {
		plan.leave = fmt.Errorf("leaving %s: command path is not a file", path)
		return plan, nil
	}
	followed, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			plan.leave = fmt.Errorf("leaving %s: command file does not match the installed Mihari program", path)
			return plan, nil
		}
		return uninstallCommandPlan{}, fmt.Errorf("inspect command file %s: %w", path, err)
	}
	if !followed.Mode().IsRegular() {
		plan.leave = fmt.Errorf("leaving %s: command path is not a file", path)
		return plan, nil
	}
	match, err := commandFileMatchesInstalled(filepath.Join(installRoot, platform.InstalledBinaryName()), path)
	if err != nil {
		if errors.Is(err, errInstalledProgramMissing) {
			plan.leave = fmt.Errorf("leaving %s: installed Mihari program is missing, so the command file was left in place", path)
			return plan, nil
		}
		if errors.Is(err, errCommandNotComparable) {
			plan.leave = fmt.Errorf("leaving %s: command file does not match the installed Mihari program", path)
			return plan, nil
		}
		return uninstallCommandPlan{}, err
	}
	if !match {
		plan.leave = fmt.Errorf("leaving %s: command file does not match the installed Mihari program", path)
		return plan, nil
	}
	plan.remove = true
	return plan, nil
}

func commandFileMatchesInstalled(installed, command string) (bool, error) {
	info, err := os.Lstat(installed)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		return false, errInstalledProgramMissing
	}
	if err != nil {
		return false, fmt.Errorf("inspect installed Mihari program %s: %w", installed, err)
	}
	left, err := hashCommandFile(installed)
	if errors.Is(err, os.ErrNotExist) {
		return false, errInstalledProgramMissing
	}
	if err != nil {
		return false, fmt.Errorf("read installed Mihari program %s: %w", installed, err)
	}
	right, err := hashCommandFile(command)
	if errors.Is(err, os.ErrNotExist) {
		return false, errCommandNotComparable
	}
	if err != nil {
		return false, fmt.Errorf("read command file %s: %w", command, err)
	}
	return bytes.Equal(left, right), nil
}

func hashCommandFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return nil, err
	}
	return sum.Sum(nil), nil
}

// removeClassifiedUninstallCommand unlinks a byte-matched command entry.
// Unix removal failure is returned as stop. Windows removal failure and a
// byte mismatch are returned as deferred so the service and directories still
// go away.
func removeClassifiedUninstallCommand(plan uninstallCommandPlan, progress func(string)) (deferred error, stop error) {
	if plan.remove {
		if err := removeUninstallCommandFile(plan.path); err != nil {
			removeErr := fmt.Errorf("remove %s: %w", plan.path, err)
			if !commandRemoveFailureIsFatal {
				removeErr = fmt.Errorf("%w; run Mihari from a separate copy and retry the uninstall", removeErr)
			}
			reportUninstallProgress(progress, removeErr.Error())
			if commandRemoveFailureIsFatal {
				return nil, removeErr
			}
			return removeErr, nil
		}
		reportUninstallProgress(progress, "Removed "+plan.path)
		return nil, nil
	}
	if plan.leave != nil {
		reportUninstallProgress(progress, plan.leave.Error())
		return plan.leave, nil
	}
	return nil, nil
}
