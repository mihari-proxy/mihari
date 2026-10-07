package core

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func (s *fileUpdateStore) removeEmptyCleanupDirectory(ctx context.Context, tx string) (resultErr error) {
	path, _, err := fileUpdatePath(UpdateCleanup, tx)
	if err != nil {
		return err
	}
	r, err := s.root(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, r.Close()) }()
	if err := updateFileParent(r, path, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	dir := filepath.Dir(path)
	f, err := r.Open(dir)
	if err != nil {
		return err
	}
	info, statErr := f.Stat()
	entries, readErr := f.ReadDir(1)
	closeErr := f.Close()
	if err := errors.Join(statErr, closeErr); err != nil {
		return err
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return readErr
	}
	if len(entries) != 0 {
		return nil
	} // Unknown or interrupted material is retained.
	current, err := r.Lstat(dir)
	if err != nil {
		return err
	}
	if !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, current) {
		return os.ErrPermission
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Remove is nonrecursive: a new file arriving in this directory stops it.
	if err := r.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncUpdateDirectory(r, filepath.Dir(dir))
}

func (s *fileUpdateStore) syncCleanupRecord(ctx context.Context, tx string) (resultErr error) {
	path, _, err := fileUpdatePath(UpdateCleanup, tx)
	if err != nil {
		return err
	}
	r, err := s.root(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, r.Close()) }()
	if err := updateFileParent(r, path, false); err != nil {
		return err
	}
	return syncUpdateDirectory(r, filepath.Dir(path))
}

func (s *fileUpdateStore) cleanupTransactions(ctx context.Context) (names []string, resultErr error) {
	r, err := s.root(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, r.Close()) }()
	path := filepath.Join("staging", "core", "update")
	if err := updateFileParent(r, filepath.Join(path, "entry"), false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	dir, err := r.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, dir.Close()) }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && validTransaction(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}
