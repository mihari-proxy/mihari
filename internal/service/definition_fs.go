//go:build linux || darwin

package service

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/platform"
	"os"
	"path"
	"strings"
)

type osDefinitionStore struct{ systemdUnitFile string }

// NewUnixDefinitionStore constructs the retained-capability native service store.
// It performs no IO; each operation verifies and owns its parent capability.
func NewUnixDefinitionStore() DefinitionStore { return osDefinitionStore{} }

func (s osDefinitionStore) withSystemdPaths(paths SystemdPaths) DefinitionStore {
	s.systemdUnitFile = paths.UnitFile
	return s
}

func (osDefinitionStore) Read(ctx context.Context, name string) (file DefinitionFile, err error) {
	parent, err := platform.OpenTrustedParent(ctx, path.Dir(name), 0)
	if err != nil {
		return file, err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	entry, err := parent.ReadServiceEntry(ctx, path.Base(name))
	if err != nil {
		return file, err
	}
	if !entry.Present {
		return file, os.ErrNotExist
	}
	file = DefinitionFile{Path: name, Bytes: entry.Bytes, Owner: entry.Owner, Mode: entry.Mode, Identity: entry.Key()}
	switch {
	case entry.Link == defaultDevNull:
		file.Kind = "mask"
	case entry.Link != "":
		file.Kind = "link"
	case strings.HasSuffix(name, ".plist"):
		file.Kind = "plist"
	case strings.HasSuffix(name, ".conf"):
		file.Kind = "dropin"
	default:
		file.Kind = "unit"
	}
	return file, nil
}
func (osDefinitionStore) Write(ctx context.Context, file DefinitionFile) (err error) {
	if file.Owner != 0 || file.Mode&^uint32(0644) != 0 {
		return os.ErrPermission
	}
	parent, err := platform.OpenTrustedParent(ctx, path.Dir(file.Path), 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	old, err := parent.ReadServiceEntry(ctx, path.Base(file.Path))
	if err != nil {
		return err
	}
	// file.Identity belongs to the saved snapshot, which need not be the live
	// inode after installation or masking. Retain the live observation for the
	// atomic replacement; the transaction owns recorded-version validation.
	return parent.WriteServiceEntry(ctx, path.Base(file.Path), file.Bytes, "", file.Mode, old)
}
func (s osDefinitionStore) Mask(ctx context.Context, name, target string) (err error) {
	unitFile := s.systemdUnitFile
	if unitFile == "" {
		unitFile = defaultSystemdUnitFile
	}
	if !unixAbs(target) || path.Clean(target) != target || (target != defaultDevNull && target != unitFile) {
		return os.ErrPermission
	}
	parent, err := platform.OpenTrustedParent(ctx, path.Dir(name), 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	old, err := parent.ReadServiceEntry(ctx, path.Base(name))
	if err != nil {
		return err
	}
	return parent.WriteServiceEntry(ctx, path.Base(name), nil, target, 0644, old)
}
func (osDefinitionStore) Remove(ctx context.Context, name string) (err error) {
	parent, err := platform.OpenTrustedParent(ctx, path.Dir(name), 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	old, err := parent.ReadServiceEntry(ctx, path.Base(name))
	if err != nil {
		return err
	}
	return parent.RemoveServiceEntry(ctx, path.Base(name), old)
}
func (osDefinitionStore) ReadLink(ctx context.Context, name string) (link string, err error) {
	parent, err := platform.OpenTrustedParent(ctx, path.Dir(name), 0)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	entry, err := parent.ReadServiceEntry(ctx, path.Base(name))
	if err != nil {
		return "", err
	}
	if !entry.Present {
		return "", os.ErrNotExist
	}
	if entry.Link == "" {
		return "", os.ErrInvalid
	}
	return entry.Link, nil
}
func (osDefinitionStore) List(ctx context.Context, dir string) (names []string, err error) {
	parent, err := platform.OpenTrustedParent(ctx, dir, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	children, err := parent.ReadNames(ctx)
	if err != nil {
		return nil, err
	}
	for _, name := range children {
		names = append(names, path.Join(dir, name))
	}
	return names, nil
}
