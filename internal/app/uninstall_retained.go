package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cleanUninstallTarget retains one classified PATH command and its ancestors.
// Every deletion in a retained root is confined to a directory handle.
func cleanUninstallTarget(ctx context.Context, target UninstallTarget, retained string, remove func(string) error) (removed bool, err error) {
	if retained == "" {
		return true, removeUninstallTarget(target, remove)
	}
	relative, inside, err := retainedCommandRelative(target.Path, retained)
	if err != nil {
		return false, err
	}
	if !inside {
		return true, removeUninstallTarget(target, remove)
	}
	info, err := os.Lstat(target.Path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if err = refuseInvalidUninstallRoot(target, info); err != nil {
		return false, err
	}
	root, err := os.OpenRoot(target.Path)
	if err != nil {
		return false, err
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close uninstall root: %w", closeErr))
		}
	}()
	opened, err := root.Stat(".")
	if err != nil {
		return false, err
	}
	if !os.SameFile(info, opened) {
		return false, errors.New("uninstall root changed before cleanup")
	}
	if relative == "." {
		return false, nil
	}
	return false, cleanRetainedDirectory(ctx, root, ".", relative)
}

func retainedCommandRelative(root, command string) (string, bool, error) {
	relative, err := filepath.Rel(root, command)
	if err == nil && uninstallRelativeInside(relative) {
		return relative, true, nil
	}
	// Resolve existing directory aliases (not the command leaf, which may be
	// a symlink) so Windows short paths cannot bypass the retention decision.
	actualRoot, err := filepath.EvalSymlinks(root)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	actualParent, err := filepath.EvalSymlinks(filepath.Dir(command))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	relative, err = filepath.Rel(actualRoot, filepath.Join(actualParent, filepath.Base(command)))
	if err != nil {
		return "", false, nil
	}
	return relative, uninstallRelativeInside(relative), nil
}

func uninstallRelativeInside(relative string) bool {
	return !filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func cleanRetainedDirectory(ctx context.Context, root *os.Root, directory, retained string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := root.Open(directory)
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err = errors.Join(readErr, closeErr); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(directory, entry.Name())
		if sameUninstallPath(path, retained) {
			continue
		}
		info, err := root.Lstat(path)
		if err != nil {
			return err
		}
		below, relErr := filepath.Rel(path, retained)
		if relErr == nil && uninstallRelativeInside(below) {
			// Never follow a retained-path ancestor that became a link or reparse
			// point. Keeping that node also keeps the command path reachable.
			if !info.IsDir() || uninstallRootLinkReason(info) != "" {
				continue
			}
			if err := cleanRetainedDirectory(ctx, root, path, retained); err != nil {
				return err
			}
			continue
		}
		if uninstallRootLinkReason(info) != "" {
			err = root.Remove(path)
		} else {
			err = root.RemoveAll(path)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
