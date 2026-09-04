package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ErrPublishDirectoryChanged reports that the visible path no longer names the
// held publish directory.
var ErrPublishDirectoryChanged = errors.New("publish directory changed")

// PublishDir is a closeable capability over a held output directory.
type PublishDir struct {
	mu     sync.Mutex
	closed bool
	path   string
	plat   publishDirState
}

// PublishWorkspace is a private child directory held by identity.
type PublishWorkspace struct {
	mu     sync.Mutex
	closed bool
	owner  *PublishDir
	name   string
	plat   publishWorkspaceState
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
	if !isSingleSegment(name) {
		return false, fmt.Errorf("publish target must be a basename")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return false, errPrivateFSClosed
	}
	return d.existsLocked(name)
}

// IsWithin reports whether the held publish directory is the held ancestor or
// one of its descendants.
func (d *PublishDir) IsWithin(ancestor *DirectoryIdentity) (bool, error) {
	if ancestor == nil {
		return false, fmt.Errorf("publish ancestor is nil")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return false, errPrivateFSClosed
	}
	ancestor.mu.Lock()
	defer ancestor.mu.Unlock()
	if ancestor.closed {
		return false, errPrivateFSClosed
	}
	return d.isWithinLocked(ancestor)
}

// CreateWorkspace creates and holds a private child directory.
func (d *PublishDir) CreateWorkspace() (*PublishWorkspace, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, errPrivateFSClosed
	}
	return d.createWorkspaceLocked()
}

// CreateTemp creates a private, exclusive temporary file relative to the held
// workspace and returns its single-segment basename.
func (w *PublishWorkspace) CreateTemp(pattern string) (*os.File, string, error) {
	if !validTempPattern(pattern) {
		return nil, "", fmt.Errorf("publish temp pattern must be a basename")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil, "", errPrivateFSClosed
	}
	return w.createTempLocked(pattern)
}

// Remove removes a non-reparse file relative to the held workspace.
func (w *PublishWorkspace) Remove(name string) error {
	if !isSingleSegment(name) {
		return fmt.Errorf("publish temp must be a basename")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errPrivateFSClosed
	}
	return w.removeLocked(name)
}

// PublishNoReplace atomically publishes tempName into the held directory. It
// never replaces targetName. Once the target exists, later cleanup or sync
// failures are delivered to onWarning and success remains committed.
func (d *PublishDir) PublishNoReplace(workspace *PublishWorkspace, tempName, targetName string, onWarning func(error)) error {
	if !isSingleSegment(tempName) || !isSingleSegment(targetName) {
		return fmt.Errorf("publish names must be basenames")
	}
	if workspace == nil {
		return fmt.Errorf("publish workspace is nil")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return errPrivateFSClosed
	}
	workspace.mu.Lock()
	defer workspace.mu.Unlock()
	if workspace.closed {
		return errPrivateFSClosed
	}
	if workspace.owner != d {
		return fmt.Errorf("publish workspace belongs to a different directory")
	}
	return d.publishNoReplaceLocked(workspace, tempName, targetName, warningSink(onWarning))
}

// Close removes an empty workspace only when its current parent entry still
// has the held identity, then releases its handle. Repeated calls return nil.
func (w *PublishWorkspace) Close() error {
	if w.owner == nil {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.closed {
			return nil
		}
		w.closed = true
		return w.closePlatformLocked(nil)
	}
	w.owner.mu.Lock()
	defer w.owner.mu.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.closePlatformLocked(w.owner)
}

// Close releases the held publish directory. Repeated calls return nil.
func (d *PublishDir) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return d.closePlatformLocked()
}

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
