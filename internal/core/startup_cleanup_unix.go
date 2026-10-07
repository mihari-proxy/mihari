//go:build linux || darwin

package core

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
)

func (s *unixProvenanceStore) removeEmptyCleanupDirectory(ctx context.Context, tx string) (resultErr error) {
	if !validTransaction(tx) {
		return os.ErrInvalid
	}
	if err := s.check(ctx); err != nil {
		return err
	}
	parent, _, err := openCoreParent(ctx, s.data, "staging/core/update/entry", false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, parent.Close()) }()
	dir, err := parent.OpenDir(ctx, tx, platform.RootPolicy{Owner: 0, Mode: 0700})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	names, err := dir.ReadNames(ctx)
	if err := errors.Join(err, dir.Close()); err != nil {
		return err
	}
	if len(names) != 0 {
		return nil
	}
	return parent.RemoveEmptyDir(ctx, tx)
}

func (s *unixProvenanceStore) syncCleanupRecord(ctx context.Context, tx string) (resultErr error) {
	parent, _, _, err := s.parent(ctx, UpdateCleanup, tx, false)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, parent.Close()) }()
	return parent.Sync(ctx)
}

func (s *unixProvenanceStore) cleanupTransactions(ctx context.Context) (names []string, resultErr error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	parent, _, err := openCoreParent(ctx, s.data, "staging/core/update/entry", false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, parent.Close()) }()
	entries, err := parent.ReadNames(ctx)
	if err != nil {
		return nil, err
	}
	for _, name := range entries {
		if validTransaction(name) {
			names = append(names, name)
		}
	}
	return names, nil
}
