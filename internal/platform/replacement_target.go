package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// ReplacementFile binds a replacement target to its observed contents and identity.
// Lack of execution trust does not imply absence.
type ReplacementFile struct {
	Path       string
	FileID     string
	SHA256     string
	Exists     bool
	MayExecute bool
}

// Keep the read bound aligned with update.maxSelfBinarySize without introducing
// a dependency from platform to the update use case.
const replacementFileLimit = 128 << 20

// ObserveReplacementFile observes an absolute replacement target without writing it.
func ObserveReplacementFile(ctx context.Context, path string) (out ReplacementFile, err error) {
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if !filepath.IsAbs(path) {
		return out, os.ErrInvalid
	}
	path = filepath.Clean(path)
	out.Path = path
	named, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, validateAbsentReplacementParent(ctx, filepath.Dir(path))
	}
	if err != nil {
		return out, err
	}
	if !named.Mode().IsRegular() {
		return out, ErrUnsafeComponent
	}
	f, id, trusted, verify, closeParent, err := openReplacementFile(ctx, path)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, f.Close(), closeParent()) }()
	before, err := f.Stat()
	if err != nil {
		return out, err
	}
	if !os.SameFile(named, before) || !before.Mode().IsRegular() {
		return out, ErrIdentityMismatch
	}
	if before.Size() > replacementFileLimit {
		return out, os.ErrInvalid
	}
	if err = verify(); err != nil {
		return out, err
	}
	_, size, digest, err := readSourceContent(ctx, f, replacementFileLimit, false)
	if err != nil {
		return out, err
	}
	if size > replacementFileLimit {
		return out, os.ErrInvalid
	}
	after, err := f.Stat()
	if err != nil {
		return out, err
	}
	if size != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() {
		return out, ErrIdentityMismatch
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if err = verify(); err != nil {
		return out, err
	}
	// The platform opener supplies the canonical name of this directory entry.
	out.Path = f.Name()
	out.Exists = true
	out.FileID = id
	out.SHA256 = digest
	out.MayExecute = trusted
	return out, nil
}

// Absence of the leaf does not establish that its existing ancestry is safe.
// Locate the deepest existing parent, then use the platform's read-only path
// checks, which do not impose execution ownership on an absent target.
func validateAbsentReplacementParent(ctx context.Context, path string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err == nil {
			if !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return ErrUnsafeComponent
			}
			return validateReplacementParent(ctx, path)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return err
		}
		path = parent
	}
}
