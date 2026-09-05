package logging

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mihari-proxy/mihari/internal/platform"
)

type snapshotHandle struct {
	name string
	size int64
	file *os.File
}

func snapshotSource(
	ctx context.Context,
	fs *platform.PrivateFS,
	basePath string,
	enterMutex func(string) func(),
	openLock func(*platform.PrivateFS, string) (platform.AdvisoryLock, error),
	openSnapshot func(*platform.PrivateFS, string, platform.FileIdentity) (*os.File, error),
) (handles []snapshotHandle, retErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if fs == nil {
		return nil, fmt.Errorf("snapshot private fs is required")
	}
	if basePath == "" {
		return nil, fmt.Errorf("snapshot base path is required")
	}
	if enterMutex == nil {
		enterMutex = func(string) func() { return func() {} }
	}
	if openLock == nil {
		openLock = platform.OpenAdvisoryLock
	}
	if openSnapshot == nil {
		openSnapshot = platform.OpenSnapshot
	}

	leaveMutex := enterMutex(basePath)
	if leaveMutex == nil {
		leaveMutex = func() {}
	}
	defer leaveMutex()

	lock, err := openLock(fs, basePath+".lock")
	if err != nil {
		return nil, fmt.Errorf("open snapshot lock: %w", err)
	}
	locked := false
	defer func() {
		var releaseErr error
		if locked {
			releaseErr = lock.Unlock()
		}
		releaseErr = errors.Join(releaseErr, lock.Close())
		if releaseErr != nil {
			retErr = errors.Join(retErr, releaseErr)
			if closeErr := closeSnapshots(handles); closeErr != nil {
				retErr = errors.Join(retErr, closeErr)
			}
			handles = nil
		}
	}()
	if err := lock.Lock(ctx, platform.LockShared); err != nil {
		return nil, fmt.Errorf("lock snapshot source: %w", err)
	}
	locked = true

	entries, err := fs.ReadDir(filepath.Dir(basePath))
	if err != nil {
		return nil, fmt.Errorf("list snapshot source: %w", err)
	}
	base := filepath.Base(basePath)
	type candidate struct {
		entry platform.FileEntry
		order int
	}
	var candidates []candidate
	for _, entry := range entries {
		order, matched := snapshotOrder(base, entry.Name)
		if !matched {
			continue
		}
		if !entry.Mode.IsRegular() {
			return nil, fmt.Errorf("snapshot source %q is not a regular file", entry.Name)
		}
		candidates = append(candidates, candidate{entry: entry, order: order})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].order > candidates[j].order })

	for _, candidate := range candidates {
		path := filepath.Join(filepath.Dir(basePath), candidate.entry.Name)
		file, err := openSnapshot(fs, path, candidate.entry.Identity)
		if err != nil {
			err = errors.Join(err, closeSnapshots(handles))
			handles = nil
			return nil, fmt.Errorf("open snapshot source %q: %w", candidate.entry.Name, err)
		}
		info, err := file.Stat()
		if err != nil {
			err = errors.Join(err, file.Close(), closeSnapshots(handles))
			handles = nil
			return nil, fmt.Errorf("stat snapshot source %q: %w", candidate.entry.Name, err)
		}
		handles = append(handles, snapshotHandle{name: candidate.entry.Name, size: info.Size(), file: file})
	}
	return handles, nil
}

func snapshotOrder(base, name string) (int, bool) {
	if name == base {
		return 0, true
	}
	if len(name) != len(base)+2 || name[:len(base)+1] != base+"." {
		return 0, false
	}
	suffix := name[len(name)-1]
	if suffix < '1' || suffix > '9' {
		return 0, false
	}
	return int(suffix - '0'), true
}

func closeSnapshots(handles []snapshotHandle) error {
	var err error
	for _, handle := range handles {
		if handle.file != nil {
			err = errors.Join(err, handle.file.Close())
		}
	}
	return err
}
