//go:build linux || darwin

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/mihari-proxy/mihari/internal/platform"
	"golang.org/x/sys/unix"
)

func openMigrationCapability(ctx context.Context, path string, create bool) (migrationCapability, error) {
	if create {
		return nil, os.ErrPermission
	}
	return openReadOnlyMigrationRoot(ctx, path)
}

// PrepareUnixMigration opens no-follow roots and runs preserve-function prepare.
func PrepareUnixMigration(ctx context.Context, source, target, staging string, req InstallRequest) (err error) {
	src, err := openReadOnlyMigrationRoot(ctx, source)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, src.Close()) }()
	stg, err := openTrustedMigrationRoot(ctx, staging, uint32(os.Geteuid()), true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stg.Close()) }()
	var tgt migrationCapability
	if target != "" {
		tgt, err = openTrustedMigrationRoot(ctx, target, uint32(os.Geteuid()), true)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, tgt.Close()) }()
	}
	req.Source = source
	_, err = prepareMigration(ctx, migrationOptions{
		Source: src, Target: tgt, Staging: stg, Request: req, Trust: compiledInstallerTrust(),
	})
	return err
}

type trustedCap struct {
	root   *platform.TrustedRoot
	path   string
	dev    string
	ino    string
	mount  string
	owner  uint32
	create bool
}

// ProbeUnixMigrationRoot opens and closes a no-follow TrustedRoot at path.
func ProbeUnixMigrationRoot(ctx context.Context, path string, owner uint32) error {
	cap, err := openTrustedMigrationRoot(ctx, path, owner, false)
	if err != nil {
		return err
	}
	return cap.Close()
}

func openTrustedMigrationRoot(ctx context.Context, path string, owner uint32, create bool) (migrationCapability, error) {
	policy := platform.RootPolicy{Owner: owner, Mode: 0700, AllowCreate: create}
	root, err := platform.OpenTrustedRoot(ctx, path, policy)
	if err != nil {
		return nil, err
	}
	display, identity, _, _, err := root.Snapshot(ctx)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	cap := &trustedCap{root: root, path: display, owner: owner, create: create}
	cap.dev, cap.ino = splitIdentity(identity)
	_, cap.mount, _ = strings.Cut(identity, "@")
	return cap, nil
}

func (c *trustedCap) Path() string { return c.path }

func (c *trustedCap) Identity() (string, string, string) { return c.dev, c.ino, c.mount }

func (c *trustedCap) List(ctx context.Context, rel string) ([]migrationEntry, error) {
	dir, owned, err := c.openDir(ctx, rel, false)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = dir.Close() }()
	}
	names, err := dir.ReadNames(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]migrationEntry, 0, len(names))
	for _, name := range names {
		if name == "." || name == ".." {
			continue
		}
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

func (c *trustedCap) Stat(ctx context.Context, rel string) (migrationEntry, error) {
	parentRel, name := splitRel(rel)
	dir, owned, err := c.openDir(ctx, parentRel, false)
	if err != nil {
		return migrationEntry{}, err
	}
	if owned {
		defer func() { _ = dir.Close() }()
	}
	file, id, err := dir.OpenFile(ctx, name, 0600)
	if err != nil {
		child, dirErr := dir.OpenDir(ctx, name, platform.RootPolicy{Owner: c.owner, Mode: 0700})
		if dirErr != nil {
			if isHardlink(err) || isHardlink(dirErr) {
				return migrationEntry{Name: name, Kind: "file", Nlink: 2}, nil
			}
			if isNestedMount(err) || isNestedMount(dirErr) {
				return migrationEntry{Name: name, NestedMount: true, Kind: "dir", Dir: true}, nil
			}
			return migrationEntry{}, err
		}
		defer func() { _ = child.Close() }()
		_, identity, _, _, snapErr := child.Snapshot(ctx)
		if snapErr != nil {
			return migrationEntry{}, snapErr
		}
		dev, ino := splitIdentity(identity)
		return migrationEntry{Name: name, Dir: true, Kind: "dir", Nlink: 1, Dev: dev, Ino: ino, Mount: identity}, nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return migrationEntry{}, err
	}
	sum, err := hashReader(file, info.Size()+1)
	if err != nil {
		return migrationEntry{}, err
	}
	mtime := info.ModTime().UnixNano()
	dev, ino := splitIdentity(id.Key())
	return migrationEntry{
		Name: name, Size: info.Size(), Nlink: 1, Hash: sum, Kind: "file",
		Dev: dev, Ino: ino, Mount: id.Key(), Mtime: mtime, Ctime: mtime,
	}, nil
}

func (c *trustedCap) ReadFile(ctx context.Context, rel string, max int64) ([]byte, error) {
	parentRel, name := splitRel(rel)
	dir, owned, err := c.openDir(ctx, parentRel, false)
	if err != nil {
		return nil, err
	}
	if owned {
		defer func() { _ = dir.Close() }()
	}
	file, _, err := dir.OpenFile(ctx, name, 0600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if max <= 0 {
		max = migrationBusinessMax
	}
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errMigrationOversize
	}
	return data, nil
}

func (c *trustedCap) WriteFile(ctx context.Context, rel string, data []byte) error {
	parentRel, name := splitRel(rel)
	dir, owned, err := c.openDir(ctx, parentRel, true)
	if err != nil {
		return err
	}
	if owned {
		defer func() { _ = dir.Close() }()
	}
	if !c.create {
		return os.ErrPermission
	}
	var expected *platform.FileIdentity
	old, id, openErr := dir.OpenFile(ctx, name, 0600)
	if openErr == nil {
		if err := old.Close(); err != nil {
			return err
		}
		expected = &id
	} else if !errors.Is(openErr, os.ErrNotExist) {
		return openErr
	}
	return dir.WriteFile(ctx, name, data, 0600, expected)
}

func (c *trustedCap) Mkdir(ctx context.Context, rel string) error {
	dir, owned, err := c.openDir(ctx, rel, true)
	if err != nil {
		return err
	}
	if owned {
		return dir.Close()
	}
	return nil
}

func (c *trustedCap) CopyFile(ctx context.Context, srcRel string, dst migrationCapability, dstRel string, max int64) (string, error) {
	data, err := c.ReadFile(ctx, srcRel, max)
	if err != nil {
		return "", err
	}
	if err := dst.WriteFile(ctx, dstRel, data); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (c *trustedCap) Close() error {
	if c == nil || c.root == nil {
		return nil
	}
	return c.root.Close()
}

func (c *trustedCap) openDir(ctx context.Context, rel string, create bool) (*platform.TrustedRoot, bool, error) {
	rel = filepathToSlash(rel)
	cur := c.root
	if rel == "" || rel == "." {
		return cur, false, nil
	}
	owned := false
	for _, name := range strings.Split(rel, "/") {
		if name == "" {
			continue
		}
		next, err := cur.OpenDir(ctx, name, platform.RootPolicy{Owner: c.owner, Mode: 0700, AllowCreate: create})
		if owned {
			_ = cur.Close()
		}
		if err != nil {
			return nil, false, err
		}
		cur = next
		owned = true
	}
	return cur, true, nil
}

func splitRel(rel string) (string, string) {
	rel = filepathToSlash(rel)
	i := strings.LastIndex(rel, "/")
	if i < 0 {
		return ".", rel
	}
	return rel[:i], rel[i+1:]
}

func filepathToSlash(rel string) string {
	return strings.ReplaceAll(rel, "\\", "/")
}

func splitIdentity(identity string) (string, string) {
	if i := strings.IndexByte(identity, '@'); i >= 0 {
		identity = identity[:i]
	}
	parts := strings.Split(identity, ":")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return identity, identity
}

func hashReader(r io.Reader, max int64) (string, error) {
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(r, max))
	if err != nil {
		return "", err
	}
	if written >= max {
		return "", errMigrationOversize
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func isHardlink(err error) bool {
	return err != nil && (errors.Is(err, platform.ErrUnsafeComponent) || strings.Contains(strings.ToLower(err.Error()), "link"))
}

func isNestedMount(err error) bool {
	return err != nil && (errors.Is(err, unix.EXDEV) || strings.Contains(strings.ToLower(err.Error()), "mount"))
}
