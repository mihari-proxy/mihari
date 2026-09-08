//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/mihari-proxy/mihari/internal/platform"
)

type unixJournalFiles struct {
	root                 *platform.TrustedRoot
	path, identity, boot string
}

func (f *unixJournalFiles) validationRoot() string { return f.path }

// NewInstallJournalStore borrows a trusted base-dir capability. The caller
// retains root ownership; the store never exposes raw descriptors.
func NewInstallJournalStore(ctx context.Context, root *platform.TrustedRoot) (*InstallJournalStore, error) {
	if root == nil {
		return nil, os.ErrInvalid
	}
	path, identity, owner, mode, err := root.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if owner != 0 || (mode != 0711 && mode != 0700) || path == "" {
		return nil, os.ErrPermission
	}
	boot, err := installBootIdentity()
	if err != nil {
		return nil, err
	}
	return &InstallJournalStore{files: &unixJournalFiles{root: root, path: path, identity: identity, boot: boot}}, nil
}

func (f *unixJournalFiles) inspect(ctx context.Context, name string) (object JournalObject, err error) {
	parent, fileName, err := f.parent(ctx, name, false)
	if errors.Is(err, os.ErrNotExist) {
		return JournalObject{}, nil
	}
	if err != nil {
		return JournalObject{}, err
	}
	defer func() { err = errors.Join(err, f.closeParent(parent)) }()
	file, id, err := parent.OpenFile(ctx, fileName, installJournalFileMode)
	if errors.Is(err, os.ErrNotExist) {
		return JournalObject{}, nil
	}
	if err != nil {
		return JournalObject{}, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	return f.observe(ctx, file, id, name)
}

func (f *unixJournalFiles) read(ctx context.Context, name string, limit int64) (raw []byte, err error) {
	parent, fileName, err := f.parent(ctx, name, false)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.closeParent(parent)) }()
	file, _, err := parent.OpenFile(ctx, fileName, installJournalFileMode)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	return readInstallFile(ctx, file, limit)
}

func (f *unixJournalFiles) write(ctx context.Context, name string, data []byte, want JournalObject) (dur JournalDurability, err error) {
	parent, fileName, err := f.parent(ctx, name, true)
	if err != nil {
		return JournalDurability{}, err
	}
	defer func() { err = errors.Join(err, f.closeParent(parent)) }()
	expected, err := f.expected(ctx, parent, fileName, want)
	if err != nil {
		return JournalDurability{}, err
	}
	writeErr := parent.WriteFile(ctx, fileName, data, installJournalFileMode, expected)
	actual, inspectErr := f.inspectPublished(ctx, parent, fileName, name)
	published := inspectErr == nil && actual.Present && actual.SHA256 == sha256HexBytes(data)
	if writeErr != nil {
		return JournalDurability{Published: published, Object: actual}, writeErr
	}
	if inspectErr != nil {
		return JournalDurability{Published: true, Object: actual}, inspectErr
	}
	return JournalDurability{Published: true, Durable: true, Object: actual}, nil
}

func (f *unixJournalFiles) expected(ctx context.Context, parent *platform.TrustedRoot, name string, want JournalObject) (*platform.FileIdentity, error) {
	file, id, err := parent.OpenFile(ctx, name, installJournalFileMode)
	if errors.Is(err, os.ErrNotExist) && !want.Present {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	observed, err := f.observe(ctx, file, id, name)
	if err != nil {
		return nil, err
	}
	if want.Present && observed.SHA256 != want.SHA256 {
		return nil, platform.ErrIdentityMismatch
	}
	if want.Present && want.BootID == f.boot && want.Identity != observed.Identity {
		return nil, platform.ErrIdentityMismatch
	}
	return &id, nil
}

func (f *unixJournalFiles) inspectPublished(ctx context.Context, parent *platform.TrustedRoot, fileName, name string) (JournalObject, error) {
	file, id, err := parent.OpenFile(ctx, fileName, installJournalFileMode)
	if err != nil {
		return JournalObject{}, err
	}
	defer func() { _ = file.Close() }()
	return f.observe(ctx, file, id, name)
}

func (f *unixJournalFiles) observe(ctx context.Context, file *os.File, id platform.FileIdentity, name string) (JournalObject, error) {
	raw, err := readInstallFile(ctx, file, MaxInstallJournalBytes)
	if err != nil {
		return JournalObject{}, err
	}
	dev, ino, _ := strings.Cut(id.Key(), ":")
	object := JournalObject{
		Present:  true,
		SHA256:   sha256HexBytes(raw),
		Dev:      dev,
		Ino:      ino,
		MountID:  f.mountID(),
		BootID:   f.boot,
		Identity: id.Key(),
	}
	if strings.HasSuffix(name, "/transaction-id") {
		object.Marker = string(raw)
	}
	return object, nil
}

func (f *unixJournalFiles) mountID() string {
	_, rest, ok := strings.Cut(f.identity, "@")
	if !ok {
		return ""
	}
	return rest
}

func (f *unixJournalFiles) parent(ctx context.Context, path string, create bool) (*platform.TrustedRoot, string, error) {
	if err := f.check(ctx); err != nil {
		return nil, "", err
	}
	if !installJournalPathAllowed(path) {
		return nil, "", os.ErrInvalid
	}
	if path == installJournalFileName {
		return f.root, path, nil
	}
	parts := strings.Split(path, "/")
	current := f.root
	owned := false
	for _, part := range parts[:len(parts)-1] {
		next, err := current.OpenDir(ctx, part, platform.RootPolicy{Owner: 0, Mode: installTransactionDirMode, AllowCreate: create})
		if owned {
			err = errors.Join(err, current.Close())
		}
		if err != nil {
			if next != nil {
				err = errors.Join(err, next.Close())
			}
			return nil, "", err
		}
		current = next
		owned = true
	}
	return current, parts[len(parts)-1], nil
}

func (f *unixJournalFiles) closeParent(parent *platform.TrustedRoot) error {
	if parent == nil || parent == f.root {
		return nil
	}
	return parent.Close()
}

func (f *unixJournalFiles) check(ctx context.Context) error {
	path, identity, owner, mode, err := f.root.Snapshot(ctx)
	if err != nil {
		return err
	}
	if path != f.path || identity != f.identity || owner != 0 || (mode != 0711 && mode != 0700) {
		return os.ErrPermission
	}
	return nil
}

func installJournalPathAllowed(path string) bool {
	if path == installJournalFileName {
		return true
	}
	parts := strings.Split(path, "/")
	if len(parts) != 3 || parts[0] != "transactions" || !validTransactionID(parts[1]) {
		return false
	}
	switch parts[2] {
	case "transaction-id", "unit", "unit-bootstrap", "ready.json", "validation-launch.json":
		return true
	default:
		return false
	}
}

type installContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r installContextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(b) > 32768 {
		b = b[:32768]
	}
	return r.r.Read(b)
}

func readInstallFile(ctx context.Context, r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, os.ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(installContextReader{ctx: ctx, r: r}, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, invalidInstallJournal()
	}
	return raw, nil
}
