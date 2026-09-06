//go:build linux || darwin

package subscription

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/mihari-proxy/mihari/internal/platform"
)

type unixProviderFiles struct {
	data                 *platform.TrustedRoot
	path, identity, boot string
}

// NewProviderStore borrows a root-owned private D capability. The daemon or
// installer retains its lifecycle lease and D until all resource users close.
// Recover must finish before any core recovery, validation or execution.
func NewProviderStore(ctx context.Context, data *platform.TrustedRoot) (*ProviderStore, error) {
	if data == nil {
		return nil, os.ErrInvalid
	}
	p, id, owner, mode, err := data.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if owner != 0 || mode != 0700 || p == "" {
		return nil, os.ErrPermission
	}
	boot, err := providerBootIdentity()
	if err != nil {
		return nil, err
	}
	return &ProviderStore{files: &unixProviderFiles{data: data, path: p, identity: id, boot: boot}}, nil
}

func (f *unixProviderFiles) parent(ctx context.Context, path string, create bool) (result *platform.TrustedRoot, name string, err error) {
	if !providerAllowedPath(path) {
		return nil, "", os.ErrInvalid
	}
	p, id, owner, mode, err := f.data.Snapshot(ctx)
	if err != nil {
		return nil, "", err
	}
	if p != f.path || id != f.identity || owner != 0 || mode != 0700 {
		return nil, "", os.ErrPermission
	}
	parts := strings.Split(path, "/")
	current := f.data
	owned := false
	for _, part := range parts[:len(parts)-1] {
		next, e := current.OpenDir(ctx, part, platform.RootPolicy{Owner: 0, Mode: 0700, AllowCreate: create})
		if owned {
			e = errors.Join(e, current.Close())
		}
		if e != nil {
			if next != nil {
				e = errors.Join(e, next.Close())
			}
			return nil, "", e
		}
		current = next
		owned = true
	}
	return current, parts[len(parts)-1], nil
}

func providerAllowedPath(path string) bool {
	if path == providerJournalPath {
		return true
	}
	parts := strings.Split(path, "/")
	if len(parts) == 4 && parts[0] == "staging" && parts[1] == "providers" && profileIDPattern.MatchString(parts[2]) {
		switch parts[3] {
		case "candidate", "transaction-id", "source":
			return true
		}
	}
	if strings.HasPrefix(path, "runtime/core-home/providers/") {
		name := strings.TrimPrefix(path, "runtime/core-home/providers/")
		if at := strings.Index(name, ".old-"); at >= 0 {
			if !profileIDPattern.MatchString(name[at+5:]) {
				return false
			}
			name = name[:at]
		}
		for _, format := range []string{"yaml", "text"} {
			ext := ".yaml"
			if format == "text" {
				ext = ".txt"
			}
			id := strings.TrimSuffix(name, ext)
			p, err := providerResourcePath(id, format)
			if err == nil && p == "runtime/core-home/providers/"+name {
				return true
			}
		}
	}
	for _, kind := range []GeoResourceKind{GeoCountryMMDB, GeoASNMMDB, GeoIPDAT, GeoSiteDAT} {
		name, err := GeoResourcePath(kind)
		if err == nil && path == "runtime/core-home/"+name {
			return true
		}
	}
	return false
}

type providerContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r providerContextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	if len(b) > 32768 {
		b = b[:32768]
	}
	return r.r.Read(b)
}
func readProviderFile(ctx context.Context, r io.Reader, limit int64) ([]byte, error) {
	if limit < 0 || limit > maxGeoResourceBytes {
		return nil, os.ErrInvalid
	}
	b, err := io.ReadAll(io.LimitReader(providerContextReader{ctx: ctx, r: r}, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, dataError("resource exceeds size limit")
	}
	return b, nil
}
func (f *unixProviderFiles) read(ctx context.Context, path string, limit int64) (b []byte, err error) {
	p, n, err := f.parent(ctx, path, false)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	file, _, err := p.OpenFile(ctx, n, 0600)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	return readProviderFile(ctx, file, limit)
}
func (f *unixProviderFiles) inspect(ctx context.Context, path string) (object providerObject, err error) {
	p, n, err := f.parent(ctx, path, false)
	if errors.Is(err, os.ErrNotExist) {
		return object, nil
	}
	if err != nil {
		return object, err
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	file, id, err := p.OpenFile(ctx, n, 0600)
	if errors.Is(err, os.ErrNotExist) {
		return object, nil
	}
	if err != nil {
		return object, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	b, err := readProviderFile(ctx, file, maxGeoResourceBytes)
	if err != nil {
		return object, err
	}
	return providerObject{Present: true, Identity: f.identity + "/" + id.Key(), SHA256: providerDigest(b), BootID: f.boot}, nil
}

// expected reopens through the held parent and verifies both inode and bytes;
// serialized mutation ownership is held by the caller until the operation ends.
func (f *unixProviderFiles) expected(ctx context.Context, p *platform.TrustedRoot, n string, want providerObject) (id *platform.FileIdentity, err error) {
	file, got, err := p.OpenFile(ctx, n, 0600)
	if errors.Is(err, os.ErrNotExist) && !want.Present {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	b, err := readProviderFile(ctx, file, maxGeoResourceBytes)
	if err != nil {
		return nil, err
	}
	observed := providerObject{Present: true, Identity: f.identity + "/" + got.Key(), SHA256: providerDigest(b), BootID: f.boot}
	if observed != want {
		return nil, platform.ErrIdentityMismatch
	}
	return &got, nil
}
func (f *unixProviderFiles) write(ctx context.Context, path string, b []byte, want providerObject) (err error) {
	p, n, err := f.parent(ctx, path, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	id, err := f.expected(ctx, p, n, want)
	if err != nil {
		return err
	}
	return p.WriteFile(ctx, n, b, 0600, id)
}
func (f *unixProviderFiles) remove(ctx context.Context, path string, want providerObject) (err error) {
	p, n, err := f.parent(ctx, path, false)
	if errors.Is(err, os.ErrNotExist) && !want.Present {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	id, err := f.expected(ctx, p, n, want)
	if err != nil {
		return err
	}
	if id == nil {
		return nil
	}
	return p.RemoveFile(ctx, n, 0600, *id)
}
func (f *unixProviderFiles) move(ctx context.Context, from string, source providerObject, to string, target providerObject) (err error) {
	p, n, err := f.parent(ctx, from, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	q, m, err := f.parent(ctx, to, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, q.Close()) }()
	sid, err := f.expected(ctx, p, n, source)
	if err != nil {
		return err
	}
	if sid == nil {
		return os.ErrNotExist
	}
	tid, err := f.expected(ctx, q, m, target)
	if err != nil {
		return err
	}
	return p.MoveFileTo(ctx, n, *sid, q, m, 0600, tid)
}

func (f *unixProviderFiles) transactions(ctx context.Context) (result []string, err error) {
	p, _, err := f.parent(ctx, providerJournalPath, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	names, err := p.ReadNames(ctx)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if name == "commit.json" {
			continue
		}
		if !profileIDPattern.MatchString(name) {
			return nil, dataError("unknown provider staging entry")
		}
		result = append(result, name)
	}
	return result, nil
}

func (f *unixProviderFiles) removeTransaction(ctx context.Context, tx string) (err error) {
	p, _, err := f.parent(ctx, providerJournalPath, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	err = p.RemoveEmptyDir(ctx, tx)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
