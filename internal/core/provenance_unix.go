//go:build linux || darwin

package core

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type unixProvenanceStore struct {
	gate                 executionGate
	data                 *platform.TrustedRoot
	path, identity, boot string
}

// NewProvenanceStore borrows a root-owned 0700 D capability. The caller keeps D
// and its lifecycle lease alive until all store/config/core capabilities close.
func NewProvenanceStore(ctx context.Context, data *platform.TrustedRoot) (ProvenanceStore, error) {
	path, id, owner, mode, e := data.Snapshot(ctx)
	if e != nil {
		return nil, e
	}
	if owner != 0 || mode != 0700 || path == "" {
		return nil, os.ErrPermission
	}
	boot, e := coreBootIdentity()
	if e != nil {
		return nil, e
	}
	return &unixProvenanceStore{data: data, path: path, identity: id, boot: boot}, nil
}
func (s *unixProvenanceStore) coreStore() storeBackend { return s }
func (s *unixProvenanceStore) location() string        { return s.path }
func (s *unixProvenanceStore) bindingIdentity() string { return s.identity }
func (s *unixProvenanceStore) check(ctx context.Context) error {
	p, id, owner, mode, e := s.data.Snapshot(ctx)
	if e != nil {
		return e
	}
	if p != s.path || id != s.identity || owner != 0 || mode != 0700 {
		return os.ErrPermission
	}
	return nil
}
func rolePath(r ProvenanceRole, tx string) (string, uint32, error) {
	switch r {
	case InstalledBinary:
		return "bin/mihomo", 0700, nil
	case InstalledReceipt:
		return "bin/mihomo.provenance.json", 0600, nil
	case PairJournal:
		return "staging/core/provenance-commit.json", 0600, nil
	}
	if !validTransaction(tx) {
		return "", 0, os.ErrInvalid
	}
	var name string
	mode := uint32(0600)
	switch r {
	case CandidateBinary:
		name = "candidate-binary"
		mode = 0700
	case CandidateReceipt:
		name = "candidate-receipt.json"
	case BackupBinary:
		name = "backup-binary"
	case RestoreBinary:
		name = "restore-binary"
		mode = 0700
	case RestoreReceipt:
		name = "restore-receipt.json"
	case QuarantineBinary:
		name = "quarantine-binary"
		mode = 0700
	case QuarantineReceipt:
		name = "quarantine-receipt.json"
	case BackupReceipt:
		name = "backup-receipt.json"
	case TransactionMarker:
		name = "transaction-id"
	default:
		return "", 0, os.ErrInvalid
	}
	return "staging/core/" + tx + "/" + name, mode, nil
}
func openCoreParent(ctx context.Context, data *platform.TrustedRoot, relative string, create bool) (*platform.TrustedRoot, string, error) {
	if relative == "" || strings.ContainsAny(relative, "\\\x00") || strings.HasPrefix(relative, "/") {
		return nil, "", os.ErrInvalid
	}
	parts := strings.Split(relative, "/")
	if len(parts) < 2 {
		return nil, "", os.ErrInvalid
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, "", os.ErrInvalid
		}
	}
	current := data
	owned := false
	for _, part := range parts[:len(parts)-1] {
		next, e := current.OpenDir(ctx, part, platform.RootPolicy{Owner: 0, Mode: 0700, AllowCreate: create})
		if owned {
			closeErr := current.Close()
			if e == nil {
				e = closeErr
			}
		}
		if e != nil {
			if next != nil {
				_ = next.Close()
			}
			return nil, "", e
		}
		current = next
		owned = true
	}
	return current, parts[len(parts)-1], nil
}
func (s *unixProvenanceStore) parent(ctx context.Context, r ProvenanceRole, tx string, create bool) (*platform.TrustedRoot, string, uint32, error) {
	if e := s.check(ctx); e != nil {
		return nil, "", 0, e
	}
	relative, mode, e := rolePath(r, tx)
	if e != nil {
		return nil, "", 0, e
	}
	parent, name, e := openCoreParent(ctx, s.data, relative, create)
	return parent, name, mode, e
}
func readCoreFile(ctx context.Context, f io.Reader, limit int64) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	b, e := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, r: f}, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, dataFailure("core object exceeds size limit")
	}
	return b, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	if len(b) > 32768 {
		b = b[:32768]
	}
	return r.r.Read(b)
}
func (s *unixProvenanceStore) Load(ctx context.Context, r ProvenanceRole, tx string) (b []byte, err error) {
	p, n, m, e := s.parent(ctx, r, tx, false)
	if e != nil {
		return nil, e
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	f, _, e := p.OpenFile(ctx, n, m)
	if e != nil {
		return nil, e
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	limit := int64(1 << 20)
	if r == InstalledBinary || r == CandidateBinary || r == BackupBinary || r == RestoreBinary {
		limit = maxCoreBinarySize
	}
	return readCoreFile(ctx, f, limit)
}
func (s *unixProvenanceStore) Inspect(ctx context.Context, r ProvenanceRole, tx string) (result ProvenanceObject, err error) {
	p, n, m, e := s.parent(ctx, r, tx, false)
	if errors.Is(e, os.ErrNotExist) {
		return result, nil
	}
	if e != nil {
		return result, e
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	f, id, e := p.OpenFile(ctx, n, m)
	if errors.Is(e, os.ErrNotExist) {
		return result, nil
	}
	if e != nil {
		return result, e
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	b, e := readCoreFile(ctx, f, maxCoreBinarySize)
	if e != nil {
		return result, e
	}
	return ProvenanceObject{Present: true, SHA256: digest(b), Identity: s.identity + "/" + id.Key(), BootID: s.boot}, nil
}
func (s *unixProvenanceStore) Save(ctx context.Context, r ProvenanceRole, tx string, b []byte) (err error) {
	p, n, m, e := s.parent(ctx, r, tx, true)
	if e != nil {
		return e
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	var expected *platform.FileIdentity
	f, id, e := p.OpenFile(ctx, n, m)
	if e == nil {
		expected = &id
		if e = f.Close(); e != nil {
			return e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if r != PairJournal && expected != nil {
		return os.ErrExist
	}
	return p.WriteFile(ctx, n, b, m, expected)
}
func (s *unixProvenanceStore) Apply(ctx context.Context, a ProvenanceMutation) (err error) {
	p, n, m, e := s.parent(ctx, a.Role, a.Transaction, true)
	if e != nil {
		return e
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	observed, e := s.Inspect(ctx, a.Role, a.Transaction)
	if e != nil {
		return e
	}
	if observed != a.Expected {
		return platform.ErrIdentityMismatch
	}
	var expected *platform.FileIdentity
	f, id, e := p.OpenFile(ctx, n, m)
	if e == nil {
		if s.identity+"/"+id.Key() != observed.Identity {
			_ = f.Close()
			return platform.ErrIdentityMismatch
		}
		expected = &id
		if e = f.Close(); e != nil {
			return e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if a.Source == "" {
		if expected == nil {
			return nil
		}
		return p.RemoveFile(ctx, n, m, *expected)
	}
	publish := (a.Role == InstalledBinary || a.Role == InstalledReceipt) && (a.Source == candidateRole(a.Role) || a.Source == restoreRole(a.Role))
	quarantine := (a.Source == InstalledBinary || a.Source == InstalledReceipt) && a.Role == quarantineRole(a.Source)
	if !publish && !quarantine {
		return os.ErrInvalid
	}
	actual, e := s.Inspect(ctx, a.Source, a.Transaction)
	if e != nil {
		return e
	}
	if !sameObject(actual, a.SourceExpected) {
		return platform.ErrIdentityMismatch
	}
	source, sn, _, e := s.parent(ctx, a.Source, a.Transaction, false)
	if e != nil {
		return e
	}
	defer func() { err = errors.Join(err, source.Close()) }()
	sf, sid, e := source.OpenFile(ctx, sn, m)
	if e != nil {
		return e
	}
	if s.identity+"/"+sid.Key() != actual.Identity {
		_ = sf.Close()
		return platform.ErrIdentityMismatch
	}
	if e = sf.Close(); e != nil {
		return e
	}
	return source.MoveFileTo(ctx, sn, sid, p, n, m, expected)
}
func (s *unixProvenanceStore) Sync(ctx context.Context) error { return s.data.Sync(ctx) }
func (s *unixProvenanceStore) open(ctx context.Context, r ProvenanceRole, tx string) (verifiedFile, error) {
	p, n, m, e := s.parent(ctx, r, tx, false)
	if e != nil {
		return nil, e
	}
	f, id, e := p.OpenFile(ctx, n, m)
	if e != nil {
		return nil, errors.Join(e, p.Close())
	}
	b, e := readCoreFile(ctx, f, maxCoreBinarySize)
	if e != nil {
		return nil, errors.Join(e, f.Close(), p.Close())
	}
	relative, _, e := rolePath(r, tx)
	if e != nil {
		return nil, errors.Join(e, f.Close(), p.Close())
	}
	return &unixVerifiedFile{parent: p, file: f, id: id, name: n, path: filepath.Join(s.path, filepath.FromSlash(relative)), mode: m, hash: digest(b)}, nil
}

type unixVerifiedFile struct {
	parent           *platform.TrustedRoot
	file             *os.File
	id               platform.FileIdentity
	name, path, hash string
	mode             uint32
}

func (f *unixVerifiedFile) verify(ctx context.Context) (string, error) {
	current, id, e := f.parent.OpenFile(ctx, f.name, f.mode)
	if e != nil {
		return "", e
	}
	defer func() { _ = current.Close() }() // Read-only capability: no pending writes; closure cannot change the operation result.
	if id != f.id {
		return "", platform.ErrIdentityMismatch
	}
	info, e := f.file.Stat()
	if e != nil {
		return "", e
	}
	if info.Mode().Perm() != os.FileMode(f.mode) {
		return "", os.ErrPermission
	}
	b, e := readCoreFile(ctx, io.NewSectionReader(f.file, 0, maxCoreBinarySize+1), maxCoreBinarySize)
	if e != nil {
		return "", e
	}
	if digest(b) != f.hash {
		return "", dataFailure("verified core object bytes changed")
	}
	return f.path, nil
}
func (f *unixVerifiedFile) Close() error { return errors.Join(f.file.Close(), f.parent.Close()) }

func (s *unixProvenanceStore) execution() *executionGate { return &s.gate }

func (s *unixProvenanceStore) target() (string, string) { return runtime.GOOS, runtime.GOARCH }
