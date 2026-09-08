package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrPublishDirectoryChanged reports that the visible path no longer names the
// held publish directory.
var ErrPublishDirectoryChanged = errors.New("publish directory changed")

// ErrPublishCleanupIncomplete marks a failed cleanup attempt, even when a later
// held-workspace cleanup succeeds. Callers should report their sanitized warning.
var ErrPublishCleanupIncomplete = errors.New("publish cleanup incomplete")

var publishWorkspaceCleanupCheckpoint = func() {}

// PublishDir is a closeable capability over a held output directory.
type PublishDir struct {
	capabilityLifetime
	path string
	plat publishDirState
}

// PublishWorkspace is a private child directory held by identity.
type PublishWorkspace struct {
	capabilityLifetime
	owner *PublishDir
	name  string
	plat  publishWorkspaceState
}

// OpenPublishDir opens an existing absolute directory. The selected path may
// resolve through a symlink or junction once; later operations use the held
// directory capability.
func OpenPublishDir(path string) (*PublishDir, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("publish directory must be absolute")
	}
	return openPublishDir(filepath.Clean(path))
}

// Path returns the immutable canonical absolute path captured at open time.
// It remains available after Close.
func (d *PublishDir) Path() string { return d.path }

// Exists reports whether name exists in the held directory without following
// the directory's visible path.
func (d *PublishDir) Exists(name string) (bool, error) {
	finish, err := d.begin(context.Background())
	if err != nil {
		return false, err
	}
	defer finish()
	if !isSingleSegment(name) {
		return false, fmt.Errorf("publish target must be a basename")
	}
	return d.existsLocked(name)
}

// IsWithin reports whether the held publish directory is the held ancestor or
// one of its descendants.
func (d *PublishDir) IsWithin(ancestor *DirectoryIdentity) (bool, error) {
	finish, err := d.begin(context.Background())
	if err != nil {
		return false, err
	}
	defer finish()
	if ancestor == nil {
		return false, fmt.Errorf("publish ancestor is nil")
	}
	finishAncestor, err := ancestor.begin(context.Background())
	if err != nil {
		return false, err
	}
	defer finishAncestor()
	return d.isWithinLocked(ancestor)
}

// CreateWorkspace creates and holds a private child directory.
func (d *PublishDir) CreateWorkspace() (*PublishWorkspace, error) {
	finish, err := d.begin(context.Background())
	if err != nil {
		return nil, err
	}
	defer finish()
	return d.createWorkspaceLocked()
}

// CreateTemp creates a private, exclusive temporary file relative to the held
// workspace and returns its single-segment basename.
func (w *PublishWorkspace) CreateTemp(pattern string) (*os.File, string, error) {
	finish, err := w.begin(context.Background())
	if err != nil {
		return nil, "", err
	}
	defer finish()
	if !validTempPattern(pattern) {
		return nil, "", fmt.Errorf("publish temp pattern must be a basename")
	}
	return w.createTempLocked(pattern)
}

// Remove removes a non-reparse file relative to the held workspace.
func (w *PublishWorkspace) Remove(name string) error {
	finish, err := w.begin(context.Background())
	if err != nil {
		return err
	}
	defer finish()
	if !isSingleSegment(name) {
		return fmt.Errorf("publish temp must be a basename")
	}
	return w.removeLocked(name)
}

// PublishNoReplace atomically publishes tempName into the held directory. It
// never replaces targetName. Once the target exists, later cleanup or sync
// failures are delivered to onWarning and success remains committed.
func (d *PublishDir) PublishNoReplace(workspace *PublishWorkspace, tempName, targetName string, onWarning func(error)) error {
	finish, err := d.begin(context.Background())
	if err != nil {
		return err
	}
	defer finish()
	if workspace == nil {
		return fmt.Errorf("publish workspace is nil")
	}
	finishWorkspace, err := workspace.begin(context.Background())
	if err != nil {
		return err
	}
	defer finishWorkspace()
	if !isSingleSegment(tempName) || !isSingleSegment(targetName) {
		return fmt.Errorf("publish names must be basenames")
	}
	if workspace.owner != d {
		return fmt.Errorf("publish workspace belongs to a different directory")
	}
	return d.publishNoReplaceLocked(workspace, tempName, targetName, warningSink(onWarning))
}

// Close cleans held workspace contents and removes the directory only when its
// namespace is proved safe. All handles are closed even if cleanup fails.
// Repeated calls return nil.
func (w *PublishWorkspace) Close() error {
	if w.owner == nil {
		return w.closeWith(func() error { return w.closePlatformLocked(nil) })
	}
	finish, err := w.owner.begin(context.Background())
	if err != nil {
		return w.closeWith(func() error { return w.closePlatformLocked(nil) })
	}
	defer finish()
	return w.closeWith(func() error { return w.closePlatformLocked(w.owner) })
}

// Close releases the held publish directory. Repeated calls return nil.
func (d *PublishDir) Close() error { return d.closeWith(d.closePlatformLocked) }

func validTempPattern(pattern string) bool {
	if pattern == "" {
		return true
	}
	if pattern == "." || pattern == ".." {
		return false
	}
	return !strings.ContainsAny(pattern, `/\\`) && filepath.Base(pattern) == pattern
}

func warningSink(fn func(error)) func(error) {
	if fn != nil {
		return fn
	}
	return func(error) {}
}
