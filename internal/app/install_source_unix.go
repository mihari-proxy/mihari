//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/mihari-proxy/mihari/internal/platform"
)

type readOnlyMigrationCap struct {
	source                *platform.ReadOnlySource
	path, dev, ino, mount string
}

func openReadOnlyMigrationRoot(ctx context.Context, path string) (_ migrationCapability, err error) {
	root, err := platform.OpenReadOnlySource(ctx, path)
	if err != nil {
		return nil, err
	}
	display, id, err := root.Snapshot(ctx)
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	dev, ino := splitIdentity(id)
	_, mount, _ := strings.Cut(id, "@")
	return &readOnlyMigrationCap{source: root, path: display, dev: dev, ino: ino, mount: mount}, nil
}
func (c *readOnlyMigrationCap) Path() string                       { return c.path }
func (c *readOnlyMigrationCap) Identity() (string, string, string) { return c.dev, c.ino, c.mount }
func (c *readOnlyMigrationCap) Close() error                       { return c.source.Close() }
func (c *readOnlyMigrationCap) List(ctx context.Context, rel string) ([]migrationEntry, error) {
	names, err := c.source.ListNames(ctx, rel)
	if err != nil {
		return nil, err
	}
	out := make([]migrationEntry, 0, len(names))
	for _, name := range names {
		child := name
		if rel != "" && rel != "." {
			child = rel + "/" + name
		}
		entry, err := c.Stat(ctx, child)
		if err != nil {
			return nil, err
		}
		entry.Name = name
		out = append(out, entry)
	}
	return out, nil
}
func (c *readOnlyMigrationCap) Stat(ctx context.Context, rel string) (migrationEntry, error) {
	entry, err := c.source.Stat(ctx, rel, migrationBinaryMax)
	if err != nil {
		if errors.Is(err, os.ErrInvalid) && entry.Kind == "file" && entry.Size > migrationBinaryMax {
			return migrationEntry{}, errMigrationOversize
		}
		return migrationEntry{}, err
	}
	dev, ino := splitIdentity(entry.Identity)
	if entry.Kind == "dir" {
		entry.Nlink = 1
	}
	return migrationEntry{Name: entry.Name, Kind: entry.Kind, Dir: entry.Kind == "dir", Size: entry.Size, Nlink: int(entry.Nlink), Hash: entry.SHA256, Dev: dev, Ino: ino, Mount: entry.Mount, Mtime: entry.Mtime, Ctime: entry.Ctime}, nil
}
func (c *readOnlyMigrationCap) ReadFile(ctx context.Context, rel string, max int64) ([]byte, error) {
	entry, raw, err := c.source.Read(ctx, rel, max)
	if err != nil {
		if errors.Is(err, os.ErrInvalid) && entry.Kind == "file" && entry.Size > max {
			return nil, errMigrationOversize
		}
		return nil, err
	}
	if entry.Kind != "file" {
		return nil, os.ErrInvalid
	}
	return raw, nil
}
func (c *readOnlyMigrationCap) Mkdir(context.Context, string) error { return os.ErrPermission }
func (c *readOnlyMigrationCap) WriteFile(context.Context, string, []byte) error {
	return os.ErrPermission
}
func (c *readOnlyMigrationCap) CopyFile(ctx context.Context, src string, dst migrationCapability, target string, max int64) (string, error) {
	raw, err := c.ReadFile(ctx, src, max)
	if err != nil {
		return "", err
	}
	if err := dst.WriteFile(ctx, target, raw); err != nil {
		return "", err
	}
	return sha256HexBytes(raw), nil
}
