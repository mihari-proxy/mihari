package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// NewUpdateStore binds transaction IO to the existing private application root.
// It deliberately provides no installed-core execution capability. The daemon
// retains responsibility for validating its platform data-root access policy.
func NewUpdateStore(root string) (ProvenanceStore, error) {
	path, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, os.ErrPermission
	}
	return &fileUpdateStore{path: path, identity: info}, nil
}

type fileUpdateStore struct {
	path     string
	identity os.FileInfo
	gate     executionGate
}

func (s *fileUpdateStore) coreStore() storeBackend   { return s }
func (s *fileUpdateStore) location() string          { return s.path }
func (s *fileUpdateStore) execution() *executionGate { return &s.gate }
func (s *fileUpdateStore) target() (string, string)  { return runtime.GOOS, runtime.GOARCH }
func (s *fileUpdateStore) open(context.Context, ProvenanceRole, string) (verifiedFile, error) {
	return nil, os.ErrPermission
}

func (s *fileUpdateStore) root(ctx context.Context) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(s.path)
	if err != nil {
		return nil, err
	}
	held, err := r.Stat(".")
	named, namedErr := os.Lstat(s.path)
	if err != nil || namedErr != nil || !os.SameFile(s.identity, held) || !os.SameFile(s.identity, named) || !named.IsDir() {
		return nil, errors.Join(os.ErrPermission, err, namedErr, r.Close())
	}
	return r, nil
}
func fileUpdatePath(role ProvenanceRole, tx string) (string, os.FileMode, error) {
	if role == InstalledBinary && tx == "" {
		name := "mihomo"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		return filepath.Join("bin", name), 0700, nil
	}
	if role == PairJournal && tx == "" {
		return filepath.Join("staging", "core", "provenance-commit.json"), 0600, nil
	}
	// Apply supplies the transaction even for the installed destination.
	if role == InstalledBinary {
		return fileUpdatePath(role, "")
	}
	path, mode, err := updateRolePath(role, tx)
	if err == nil && runtime.GOOS == "windows" && (role == UpdateCandidate || role == UpdateRestore) {
		path += ".exe"
	}
	return filepath.FromSlash(path), os.FileMode(mode), err
}

// parent refuses links and non-directory components. Root keeps every operation
// within the held tree even if a pathname changes between these checks.
func updateFileParent(r *os.Root, path string, create bool) error {
	dir := filepath.Dir(path)
	prefix := ""
	for _, part := range strings.Split(dir, string(filepath.Separator)) {
		if part == "." {
			continue
		}
		prefix = filepath.Join(prefix, part)
		info, err := r.Lstat(prefix)
		if errors.Is(err, os.ErrNotExist) && create {
			if err = r.Mkdir(prefix, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = r.Lstat(prefix)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return os.ErrPermission
		}
	}
	return nil
}
func readUpdateFile(ctx context.Context, r *os.Root, path string) (body []byte, object ProvenanceObject, err error) {
	if err = updateFileParent(r, path, false); err != nil {
		return nil, object, err
	}
	named, err := r.Lstat(path)
	if err != nil {
		return nil, object, err
	}
	if !named.Mode().IsRegular() || named.Size() > maxCoreBinarySize {
		return nil, object, os.ErrPermission
	}
	f, err := r.Open(path)
	if err != nil {
		return nil, object, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	before, err := f.Stat()
	if err != nil {
		return nil, object, err
	}
	if !os.SameFile(named, before) {
		return nil, object, os.ErrPermission
	}
	id, err := updateFileIdentity(f)
	if err != nil {
		return nil, object, err
	}
	body, err = io.ReadAll(io.LimitReader(f, maxCoreBinarySize+1))
	if err != nil {
		return nil, object, err
	}
	if len(body) > maxCoreBinarySize {
		return nil, object, os.ErrInvalid
	}
	after, err := f.Stat()
	if err != nil {
		return nil, object, err
	}
	current, err := r.Lstat(path)
	if err != nil {
		return nil, object, err
	}
	if !os.SameFile(before, current) || before.Size() != after.Size() || before.Size() != int64(len(body)) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		return nil, object, os.ErrPermission
	}
	if err = ctx.Err(); err != nil {
		return nil, object, err
	}
	return body, ProvenanceObject{Present: true, SHA256: digest(body), Identity: id, BootID: "file-update-identity-v1"}, nil
}
func (s *fileUpdateStore) Load(ctx context.Context, role ProvenanceRole, tx string) (body []byte, err error) {
	path, _, err := fileUpdatePath(role, tx)
	if err != nil {
		return nil, err
	}
	r, err := s.root(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, r.Close()) }()
	body, _, err = readUpdateFile(ctx, r, path)
	return body, err
}
func (s *fileUpdateStore) Inspect(ctx context.Context, role ProvenanceRole, tx string) (object ProvenanceObject, err error) {
	path, _, err := fileUpdatePath(role, tx)
	if err != nil {
		return object, err
	}
	r, err := s.root(ctx)
	if err != nil {
		return object, err
	}
	defer func() { err = errors.Join(err, r.Close()) }()
	_, object, err = readUpdateFile(ctx, r, path)
	if errors.Is(err, os.ErrNotExist) {
		return ProvenanceObject{}, nil
	}
	return object, err
}
func (s *fileUpdateStore) Save(ctx context.Context, role ProvenanceRole, tx string, body []byte) (err error) {
	if len(body) > maxCoreBinarySize || role == InstalledBinary || role == PairJournal {
		return os.ErrInvalid
	}
	path, mode, err := fileUpdatePath(role, tx)
	if err != nil {
		return err
	}
	r, err := s.root(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, r.Close()) }()
	if err = updateFileParent(r, path, true); err != nil {
		return err
	}
	if info, e := r.Lstat(path); e == nil {
		if role != UpdateJournal {
			return os.ErrExist
		}
		if !info.Mode().IsRegular() {
			return os.ErrPermission
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), ".core-"+hex.EncodeToString(random[:]))
	f, err := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() {
		if e := r.Remove(tmp); e != nil && !errors.Is(e, os.ErrNotExist) {
			err = errors.Join(err, e)
		}
	}()
	_, writeErr := f.Write(body)
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = r.Rename(tmp, path); err != nil {
		return err
	}
	return syncUpdateDirectory(r, filepath.Dir(path))
}
func (s *fileUpdateStore) Apply(ctx context.Context, a ProvenanceMutation) (err error) {
	path, _, err := fileUpdatePath(a.Role, a.Transaction)
	if err != nil {
		return err
	}
	r, err := s.root(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, r.Close()) }()
	if err = updateFileParent(r, path, true); err != nil {
		return err
	}
	_, actual, err := readUpdateFile(ctx, r, path)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return err
	}
	if !sameObject(actual, a.Expected) {
		return dataFailure("core update destination identity changed")
	}
	if a.Source == "" {
		if !actual.Present {
			return nil
		}
		if err = r.Remove(path); err != nil {
			return err
		}
	} else {
		if a.Role != InstalledBinary || (a.Source != UpdateCandidate && a.Source != UpdateRestore) {
			return os.ErrInvalid
		}
		source, _, err := fileUpdatePath(a.Source, a.Transaction)
		if err != nil {
			return err
		}
		_, from, err := readUpdateFile(ctx, r, source)
		if err != nil {
			return err
		}
		if !sameObject(from, a.SourceExpected) {
			return dataFailure("core update source identity changed")
		}
		if err = r.Rename(source, path); err != nil {
			return err
		}
		if err = syncUpdateDirectory(r, filepath.Dir(source)); err != nil {
			return err
		}
	}
	return syncUpdateDirectory(r, filepath.Dir(path))
}
func (s *fileUpdateStore) Sync(ctx context.Context) (err error) {
	r, err := s.root(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, r.Close()) }()
	return syncUpdateDirectory(r, ".")
}
