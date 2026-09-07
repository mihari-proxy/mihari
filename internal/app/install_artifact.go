package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	migrationMaxFiles      = 10000
	migrationMaxBytes      = 2 << 30
	migrationMaxDepth      = 16
	migrationBusinessMax   = 256 << 20
	migrationBinaryMax     = 256 << 20
	migrationBundleComp    = 1 << 30
	migrationBundleExpand  = 2 << 30
	migrationBundleFiles   = 10000
	migrationSettingsMax   = 1 << 20
	migrationCatalogMax    = 1 << 20
	migrationCacheDocMax   = 16 << 20
	migrationCacheSumMax   = 256 << 20
	migrationOnboardingMax = 64 << 10
	migrationTUIMax        = 1 << 20
)

var (
	migrationIgnoreTop = map[string]bool{
		"logs": true, "logs-export": true, "staging": true, "locks": true,
	}
	migrationAllowedTop = map[string]bool{
		"mihari.yaml": true, "onboarding.json": true, "control.token": true, "mihari-channel": true,
		"subscriptions": true, "runtime": true, "preferences": true, "bin": true, "geoip": true, "web": true,
	}
)

// preparedMigration is the unexported prepare result. Only ApplyLocked may consume it.
type preparedMigration struct {
	source, target, staging migrationCapability
	files                   map[string]preparedFile
	hashes                  map[string]string
	runtimeYAML             []byte
	settings                []byte
	activeID                string
	coreHash                string
	obs                     map[string]sourceObservation
	afterStop               func()
	cleanupOnce             sync.Once
	cleanupFn               func()
	art                     InstallArtifacts
}

type preparedFile struct {
	rel  string
	hash string
	size int64
}

type migrationTrust struct {
	core   map[string]struct{}
	geo    map[string]struct{}
	panel  map[string][]byte
	binary map[string]struct{}
	bundle map[string]struct{}
}

func (t migrationTrust) acceptsCore(hash string) bool {
	_, ok := t.core[hash]
	return ok
}

func (t migrationTrust) acceptsGeo(hash string) bool {
	_, ok := t.geo[hash]
	return ok
}

func (t migrationTrust) acceptsBinary(hash string) bool {
	_, ok := t.binary[hash]
	return ok
}

func (t migrationTrust) acceptsBundle(hash string) bool {
	_, ok := t.bundle[hash]
	return ok
}

func (t migrationTrust) panelZip(panel, build string) ([]byte, bool) {
	raw, ok := t.panel[panel+"/"+build]
	return raw, ok
}

type migrationEntry struct {
	Name        string
	Dir         bool
	Size        int64
	Nlink       int
	Hash        string
	Dev         string
	Ino         string
	Mount       string
	Kind        string
	NestedMount bool
	Mtime       int64
	Ctime       int64
}

type migrationCapability interface {
	Path() string
	Identity() (dev, ino, mount string)
	List(ctx context.Context, rel string) ([]migrationEntry, error)
	Stat(ctx context.Context, rel string) (migrationEntry, error)
	ReadFile(ctx context.Context, rel string, max int64) ([]byte, error)
	WriteFile(ctx context.Context, rel string, data []byte) error
	Mkdir(ctx context.Context, rel string) error
	CopyFile(ctx context.Context, srcRel string, dst migrationCapability, dstRel string, max int64) (string, error)
	Close() error
}

type nodeFlags struct {
	nlink       int
	nestedMount bool
	device      bool
	symlink     bool
	hardlink    bool
}

type dirCap struct {
	dir      string
	dev      string
	ino      string
	mount    string
	flags    map[string]nodeFlags
	writes   int
	writeRel []string
	mu       sync.Mutex
}

func openDirCap(dir string) *dirCap {
	return &dirCap{
		dir:   dir,
		dev:   "testdev",
		ino:   sha256Hex(dir)[:16],
		mount: "testmnt",
		flags: map[string]nodeFlags{},
	}
}

func (c *dirCap) setFlags(rel string, flags nodeFlags) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.flags[filepath.ToSlash(rel)] = flags
}

func (c *dirCap) Path() string { return c.dir }

func (c *dirCap) Identity() (string, string, string) { return c.dev, c.ino, c.mount }

func (c *dirCap) flag(rel string) nodeFlags {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.flags[filepath.ToSlash(rel)]
}

func (c *dirCap) osPath(rel string) string {
	rel = filepath.ToSlash(rel)
	if rel == "" || rel == "." {
		return c.dir
	}
	return filepath.Join(append([]string{c.dir}, strings.Split(rel, "/")...)...)
}

func (c *dirCap) List(ctx context.Context, rel string) ([]migrationEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(c.osPath(rel))
	if err != nil {
		return nil, err
	}
	out := make([]migrationEntry, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}
		child := name
		if rel != "" && rel != "." {
			child = filepath.ToSlash(rel) + "/" + name
		}
		info, err := c.Stat(ctx, child)
		if err != nil {
			return nil, err
		}
		info.Name = name
		out = append(out, info)
	}
	return out, nil
}

func (c *dirCap) Stat(ctx context.Context, rel string) (migrationEntry, error) {
	if err := ctx.Err(); err != nil {
		return migrationEntry{}, err
	}
	rel = filepath.ToSlash(rel)
	info, err := os.Lstat(c.osPath(rel))
	if err != nil {
		return migrationEntry{}, err
	}
	entry := migrationEntry{
		Name:  filepath.Base(rel),
		Dir:   info.IsDir(),
		Size:  info.Size(),
		Nlink: 1,
		Dev:   c.dev,
		Ino:   sha256Hex(c.dir + ":" + rel)[:12],
		Mount: c.mount,
		Kind:  "file",
	}
	if info.IsDir() {
		entry.Kind = "dir"
	}
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		entry.Kind = "symlink"
		entry.Dir = false
	}
	if mode&os.ModeDevice != 0 || mode&os.ModeCharDevice != 0 {
		entry.Kind = "device"
		entry.Dir = false
	}
	if mode&os.ModeNamedPipe != 0 {
		entry.Kind = "fifo"
		entry.Dir = false
	}
	if mode&os.ModeSocket != 0 {
		entry.Kind = "socket"
		entry.Dir = false
	}
	flags := c.flag(rel)
	if flags.nlink > 0 {
		entry.Nlink = flags.nlink
	}
	if flags.hardlink {
		entry.Nlink = 2
	}
	if flags.nestedMount {
		entry.NestedMount = true
	}
	if flags.device {
		entry.Kind = "device"
		entry.Dir = false
	}
	if flags.symlink {
		entry.Kind = "symlink"
		entry.Dir = false
	}
	if !entry.Dir && entry.Kind == "file" {
		sum, err := hashFile(c.osPath(rel), info.Size()+1)
		if err != nil {
			return migrationEntry{}, err
		}
		entry.Hash = sum
	}
	entry.Mtime = info.ModTime().UnixNano()
	entry.Ctime = entry.Mtime
	return entry, nil
}

func (c *dirCap) ReadFile(ctx context.Context, rel string, max int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(c.osPath(rel))
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

func (c *dirCap) WriteFile(ctx context.Context, rel string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path := c.osPath(rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	c.mu.Lock()
	c.writes++
	c.writeRel = append(c.writeRel, filepath.ToSlash(rel))
	c.mu.Unlock()
	return os.WriteFile(path, data, 0o600)
}

func (c *dirCap) Mkdir(ctx context.Context, rel string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.MkdirAll(c.osPath(rel), 0o700)
}

func (c *dirCap) CopyFile(ctx context.Context, srcRel string, dst migrationCapability, dstRel string, max int64) (string, error) {
	data, err := c.ReadFile(ctx, srcRel, max)
	if err != nil {
		return "", err
	}
	if err := dst.WriteFile(ctx, dstRel, data); err != nil {
		return "", err
	}
	return sha256HexBytes(data), nil
}

func (c *dirCap) Close() error { return nil }

func (p *preparedMigration) artifacts() InstallArtifacts {
	if p == nil {
		return InstallArtifacts{}
	}
	return p.art
}

func (p *preparedMigration) cleanup() {
	if p == nil {
		return
	}
	p.cleanupOnce.Do(func() {
		if p.cleanupFn != nil {
			p.cleanupFn()
		}
		if p.staging != nil {
			_ = p.staging.Close()
		}
		if p.source != nil {
			_ = p.source.Close()
		}
		if p.target != nil {
			_ = p.target.Close()
		}
	})
}

func (p *preparedMigration) hasRel(rel string) bool {
	if p == nil {
		return false
	}
	_, ok := p.files[filepath.ToSlash(rel)]
	return ok
}

func hashFile(path string, max int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, max))
	if err != nil {
		return "", err
	}
	if written >= max {
		return "", errMigrationOversize
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

var errMigrationOversize = errors.New("migration source exceeds size or file limits")
