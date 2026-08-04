// Package archive provides safe extraction helpers for panel distribution zips.
package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
)

const (
	// MaxZipSize is the upper bound for a panel distribution archive.
	MaxZipSize = 128 << 20
	// MaxExtractedFileSize bounds a single extracted file.
	MaxExtractedFileSize = 64 << 20
)

// SafeName reports whether a zip entry name is relative and free of path traversal.
func SafeName(name string) bool {
	if name == "" {
		return false
	}
	forward := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(forward, "/") {
		return false
	}
	// Reject Windows drive-letter absolute paths (e.g. C:/...).
	if len(forward) >= 2 && forward[1] == ':' {
		return false
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(forward)))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return false
	}
	if filepath.IsAbs(filepath.FromSlash(forward)) || filepath.IsAbs(cleaned) {
		return false
	}
	return true
}

// ExtractZip extracts archivePath into destDir, rejecting unsafe paths, symlinks,
// and archives that do not contain index.html anywhere under destDir.
func ExtractZip(archivePath, destDir string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return dataFailure("invalid panel archive")
	}
	defer reader.Close()

	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return fmt.Errorf("create panel extract directory: %w", err)
	}

	var foundIndex bool
	for _, file := range reader.File {
		if !SafeName(file.Name) {
			return dataFailure("unsafe path in panel archive")
		}
		mode := file.Mode()
		if mode&os.ModeSymlink != 0 {
			return dataFailure("symlink in panel archive")
		}
		// Some archives mark links via ModeType without ModeSymlink bit on all platforms.
		if mode&os.ModeType == os.ModeSymlink {
			return dataFailure("symlink in panel archive")
		}

		target, err := resolveTarget(destDir, file.Name)
		if err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fmt.Errorf("create panel directory: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("create panel parent directory: %w", err)
		}
		if err := extractFile(file, target); err != nil {
			return err
		}
		if strings.EqualFold(filepath.Base(file.Name), "index.html") {
			foundIndex = true
		}
	}
	if !foundIndex {
		_ = os.RemoveAll(destDir)
		return dataFailure("panel archive is missing index.html")
	}
	return nil
}

func resolveTarget(destDir, name string) (string, error) {
	if !SafeName(name) {
		return "", dataFailure("unsafe path in panel archive")
	}
	forward := strings.ReplaceAll(name, "\\", "/")
	cleaned := filepath.Clean(filepath.FromSlash(forward))
	target := filepath.Join(destDir, cleaned)
	// Ensure target stays under destDir after Join.
	rel, err := filepath.Rel(destDir, target)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", dataFailure("unsafe path in panel archive")
	}
	return target, nil
}

func extractFile(file *zip.File, target string) error {
	if file.UncompressedSize64 > MaxExtractedFileSize {
		return dataFailure("panel archive file is too large")
	}
	source, err := file.Open()
	if err != nil {
		return dataFailure("open panel archive entry")
	}
	defer source.Close()

	destination, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create panel file: %w", err)
	}
	written, copyErr := io.Copy(destination, io.LimitReader(source, MaxExtractedFileSize+1))
	closeErr := destination.Close()
	if copyErr != nil {
		os.Remove(target)
		return dataFailure("extract panel archive entry")
	}
	if written > MaxExtractedFileSize {
		os.Remove(target)
		return dataFailure("panel archive file is too large")
	}
	if closeErr != nil {
		os.Remove(target)
		return closeErr
	}
	return nil
}

func dataFailure(message string) error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: message}
}
