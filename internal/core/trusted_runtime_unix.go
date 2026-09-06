//go:build linux || darwin

package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
)

// NewTrustedExecution borrows the root-owned data capability and an optional
// test executor. The daemon closes all capabilities before releasing its lease.
func NewTrustedExecution(ctx context.Context, data *platform.TrustedRoot, executor VerifiedExecutor) (*TrustedExecution, error) {
	s, e := NewProvenanceStore(ctx, data)
	if e != nil {
		return nil, e
	}
	home, _, e := openCoreParent(ctx, data, "runtime/core-home/tmp/placeholder", true)
	if e != nil {
		return nil, e
	}
	if e = home.Close(); e != nil {
		return nil, e
	}
	return &TrustedExecution{store: s, files: &unixConfigFiles{data: data}, executor: executor}, nil
}

type unixConfigFiles struct{ data *platform.TrustedRoot }

func (f *unixConfigFiles) prepare(ctx context.Context, b []byte) (*ConfigCapability, error) {
	var id [16]byte
	if _, e := rand.Read(id[:]); e != nil {
		return nil, e
	}
	relative := "staging/config-" + hex.EncodeToString(id[:]) + ".yaml"
	p, n, e := openCoreParent(ctx, f.data, relative, true)
	if e != nil {
		return nil, e
	}
	e = p.WriteFile(ctx, n, b, 0600, nil)
	e = errors.Join(e, p.Close())
	if e != nil {
		return nil, e
	}
	return BindGeneratedConfig(ctx, f.data, relative, sha256.Sum256(b))
}
func (f *unixConfigFiles) read(ctx context.Context) (b []byte, err error) {
	p, n, e := openCoreParent(ctx, f.data, "runtime/config.yaml", false)
	if e != nil {
		return nil, e
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	file, _, e := p.OpenFile(ctx, n, 0600)
	if e != nil {
		return nil, e
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	return readCoreFile(ctx, file, 32<<20)
}
func (f *unixConfigFiles) write(ctx context.Context, b []byte) (*ConfigCapability, error) {
	p, n, e := openCoreParent(ctx, f.data, "runtime/config.yaml", true)
	if e != nil {
		return nil, e
	}
	defer func() { _ = p.Close() }() // Read-only capability: no pending writes; closure cannot change the operation result.
	var expected *platform.FileIdentity
	file, id, e := p.OpenFile(ctx, n, 0600)
	if e == nil {
		expected = &id
		if e = file.Close(); e != nil {
			return nil, e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	if e = p.WriteFile(ctx, n, b, 0600, expected); e != nil {
		return nil, e
	}
	return f.bind(ctx, sha256.Sum256(b))
}
func (f *unixConfigFiles) bind(ctx context.Context, hash [32]byte) (*ConfigCapability, error) {
	return BindGeneratedConfig(ctx, f.data, "runtime/config.yaml", hash)
}
func (f *unixConfigFiles) remove(ctx context.Context, c *ConfigCapability) error {
	if c == nil || c.committed {
		return os.ErrInvalid
	}
	native, ok := c.file.(*unixVerifiedFile)
	if !ok {
		return os.ErrInvalid
	}
	if _, e := native.verify(ctx); e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		return e
	}
	return native.parent.RemoveFile(ctx, native.name, 0600, native.id)
}
