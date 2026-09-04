package platform

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	privateLogDirName    = "logs"
	privateExportDirName = "logs-export"
)

// ErrIdentityMismatch is returned when OpenReadChecked sees a different identity.
var ErrIdentityMismatch = errors.New("private fs file identity mismatch")

var errPrivateFSClosed = fmt.Errorf("private fs: %w", os.ErrClosed)

// PrivateFS is a closeable capability over a verified data-root directory.
type PrivateFS struct {
	mu     sync.RWMutex
	dirsMu sync.Mutex
	closed bool
	root   string
	plat   privateFSState
}

// FileIdentity is an opaque platform identity for a directory entry or open handle.
type FileIdentity struct {
	plat fileIdentity
}

// DirectoryIdentity is a closeable capability over a held directory handle.
type DirectoryIdentity struct {
	mu     sync.Mutex
	closed bool
	plat   dirIdentityState
}

// FileEntry is a no-follow directory listing record.
type FileEntry struct {
	Name     string
	Mode     os.FileMode
	Identity FileIdentity
}

// NewPrivateFS opens or creates an absolute data root and holds its identity.
func NewPrivateFS(dataRoot string) (*PrivateFS, error) {
	if dataRoot == "" || !filepath.IsAbs(dataRoot) {
		return nil, fmt.Errorf("private fs data root must be absolute")
	}
	root := filepath.Clean(dataRoot)
	volume := filepath.VolumeName(root)
	if root == volume || root == volume+string(filepath.Separator) {
		return nil, fmt.Errorf("private fs data root must not be a filesystem root")
	}
	fs := &PrivateFS{root: root}
	if err := fs.openRoot(); err != nil {
		return nil, err
	}
	return fs, nil
}

// EnsureDir creates or hardens logs/ or logs-export/ relative to the held root.
func (fs *PrivateFS) EnsureDir(path string) error {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if fs.closed {
		return errPrivateFSClosed
	}
	name, err := fs.resolveDir(path)
	if err != nil {
		return err
	}
	return fs.ensureDirLocked(name)
}

// OpenAppend opens a log or export file for append-create, no-follow.
func (fs *PrivateFS) OpenAppend(path string) (*os.File, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if fs.closed {
		return nil, errPrivateFSClosed
	}
	dir, name, err := fs.resolveFile(path)
	if err != nil {
		return nil, err
	}
	return fs.openAppendLocked(dir, name)
}

// OpenReadChecked opens a file no-follow and requires the handle identity to match.
func (fs *PrivateFS) OpenReadChecked(path string, expected FileIdentity) (*os.File, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if fs.closed {
		return nil, errPrivateFSClosed
	}
	dir, name, err := fs.resolveFile(path)
	if err != nil {
		return nil, err
	}
	return fs.openReadCheckedLocked(dir, name, expected)
}

// CreateTemp creates an exclusive temporary file in logs/ or logs-export/.
func (fs *PrivateFS) CreateTemp(dir, pattern string) (*os.File, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if fs.closed {
		return nil, errPrivateFSClosed
	}
	name, err := fs.resolveDir(dir)
	if err != nil {
		return nil, err
	}
	f, _, err := fs.createTempLocked(name, pattern)
	return f, err
}

// ReplaceEmpty replaces path with a new empty inode. The caller must close its write handle first.
func (fs *PrivateFS) ReplaceEmpty(path string) error {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if fs.closed {
		return errPrivateFSClosed
	}
	dir, name, err := fs.resolveFile(path)
	if err != nil {
		return err
	}
	tmp, tmpName, err := fs.createTempLocked(dir, "mihari-empty-*")
	if err != nil {
		return err
	}
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = fs.removeLocked(dir, tmpName)
		}
	}()
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := fs.renameLocked(dir, tmpName, name, true); err != nil {
		return err
	}
	removeTmp = false
	return nil
}

// ReadDir lists no-follow entries of logs/ or logs-export/.
func (fs *PrivateFS) ReadDir(path string) ([]FileEntry, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if fs.closed {
		return nil, errPrivateFSClosed
	}
	name, err := fs.resolveDir(path)
	if err != nil {
		return nil, err
	}
	return fs.readDirLocked(name)
}

// OpenDirIdentity duplicates a held logs/ or logs-export/ directory capability.
func (fs *PrivateFS) OpenDirIdentity(path string) (*DirectoryIdentity, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if fs.closed {
		return nil, errPrivateFSClosed
	}
	name, err := fs.resolveDir(path)
	if err != nil {
		return nil, err
	}
	return fs.openDirIdentityLocked(name)
}

// OpenPublishDir opens one of PrivateFS's verified child directories as a
// held publish capability.
func (fs *PrivateFS) OpenPublishDir(path string) (*PublishDir, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if fs.closed {
		return nil, errPrivateFSClosed
	}
	name, err := fs.resolveDir(path)
	if err != nil {
		return nil, err
	}
	return fs.openPublishDirLocked(name)
}

// Rename renames a file within the same verified logs/ or logs-export/ directory.
func (fs *PrivateFS) Rename(oldpath, newpath string) error {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if fs.closed {
		return errPrivateFSClosed
	}
	oldDir, oldName, err := fs.resolveFile(oldpath)
	if err != nil {
		return err
	}
	newDir, newName, err := fs.resolveFile(newpath)
	if err != nil {
		return err
	}
	if oldDir != newDir {
		return fmt.Errorf("private fs rename must stay in the same directory")
	}
	return fs.renameLocked(oldDir, oldName, newName, false)
}

// Remove unlinks a file in logs/ or logs-export/.
func (fs *PrivateFS) Remove(path string) error {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if fs.closed {
		return errPrivateFSClosed
	}
	dir, name, err := fs.resolveFile(path)
	if err != nil {
		return err
	}
	return fs.removeLocked(dir, name)
}

// Close releases held directory handles. Repeat calls return nil.
func (fs *PrivateFS) Close() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.closed {
		return nil
	}
	fs.closed = true
	return fs.closePlatform()
}

func (fs *PrivateFS) resolve(path string) (parent, name string, err error) {
	if path == "" {
		return "", "", fmt.Errorf("private fs path is empty")
	}
	var rel string
	if filepath.IsAbs(path) {
		rel, err = filepath.Rel(fs.root, path)
		if err != nil {
			return "", "", fmt.Errorf("private fs path is outside data root")
		}
	} else {
		if !isSingleSegment(path) {
			return "", "", fmt.Errorf("private fs relative path must be a single segment")
		}
		rel = path
	}
	rel = filepath.Clean(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("private fs path is outside data root")
	}
	name = filepath.Base(rel)
	if !isSingleSegment(name) {
		return "", "", fmt.Errorf("private fs path final segment is invalid")
	}
	parent = filepath.Dir(rel)
	if parent == "." {
		parent = ""
	}
	return parent, name, nil
}

func (fs *PrivateFS) resolveDir(path string) (string, error) {
	parent, name, err := fs.resolve(path)
	if err != nil {
		return "", err
	}
	if parent != "" {
		return "", fmt.Errorf("private fs directory must be logs or logs-export")
	}
	canon, ok := canonicalChildDir(name)
	if !ok {
		return "", fmt.Errorf("private fs directory must be logs or logs-export")
	}
	return canon, nil
}

func (fs *PrivateFS) resolveFile(path string) (dir, name string, err error) {
	parent, name, err := fs.resolve(path)
	if err != nil {
		return "", "", err
	}
	canon, ok := canonicalChildDir(parent)
	if !ok {
		return "", "", fmt.Errorf("private fs file must be a child of logs or logs-export")
	}
	return canon, name, nil
}

func isSingleSegment(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, '\\') {
		return false
	}
	return name == filepath.Base(name)
}

func randomTempName(pattern string) (string, error) {
	if strings.ContainsRune(pattern, '/') || strings.ContainsRune(pattern, '\\') {
		return "", fmt.Errorf("private fs temp pattern must be a single segment")
	}
	prefix, suffix := pattern, ""
	if i := strings.LastIndex(pattern, "*"); i >= 0 {
		prefix, suffix = pattern[:i], pattern[i+1:]
	}
	if pattern == "" {
		prefix = "tmp"
	}
	if prefix == "." || prefix == ".." || suffix == ".." {
		return "", fmt.Errorf("private fs temp pattern is invalid")
	}
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return prefix + strconv.FormatUint(binary.BigEndian.Uint64(buf[:]), 16) + suffix, nil
}
