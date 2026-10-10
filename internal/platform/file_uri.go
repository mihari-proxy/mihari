package platform

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// FileURI returns a canonical URI for an absolute native file path or file URI.
func FileURI(path string) (string, error) {
	if len(path) >= 5 && strings.EqualFold(path[:5], "file:") {
		var err error
		path, err = FileURIPath(path)
		if err != nil {
			return "", err
		}
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("file path must be absolute: %s", path)
	}
	path = filepath.Clean(path)
	if err := validateNativeFilePath(path); err != nil {
		return "", err
	}
	return nativeFileURI(path), nil
}

// FileURIPath converts a file URI to an absolute native path without IO.
func FileURIPath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid file URI: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "file") || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsRune(u.Path, 0) {
		return "", fmt.Errorf("invalid file URI: %s", raw)
	}
	path, err := nativeFilePath(u)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("file URI must contain an absolute path: %s", raw)
	}
	path = filepath.Clean(path)
	if err := validateNativeFilePath(path); err != nil {
		return "", err
	}
	return path, nil
}
