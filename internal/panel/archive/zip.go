// Package archive provides safe extraction helpers for panel distribution zips.
package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

const (
	// MaxZipSize is the upper bound for a panel distribution archive.
	MaxZipSize = 128 << 20
	// MaxExtractedFileSize bounds a single extracted file.
	MaxExtractedFileSize = 64 << 20
	// MaxTotalExtractedBytes bounds the sum of extracted file bytes.
	MaxTotalExtractedBytes = 256 << 20
	// MaxArchiveEntries bounds the number of zip headers, including directories.
	MaxArchiveEntries = 4096
	// MaxArchiveDepth bounds slash-separated components in an entry path.
	MaxArchiveDepth = 16
)

type extractLimits struct {
	maxFile    uint64
	maxTotal   uint64
	maxEntries int
	maxDepth   int
}

var defaultExtractLimits = extractLimits{
	maxFile:    MaxExtractedFileSize,
	maxTotal:   MaxTotalExtractedBytes,
	maxEntries: MaxArchiveEntries,
	maxDepth:   MaxArchiveDepth,
}

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
	return extractZipWithLimits(archivePath, destDir, defaultExtractLimits)
}

func extractZipWithLimits(archivePath, destDir string, limits extractLimits) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return dataFailure("invalid panel archive")
	}
	defer reader.Close()

	if limits.maxEntries > 0 && len(reader.File) > limits.maxEntries {
		return dataFailure("panel archive has too many entries")
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return fmt.Errorf("create panel extract directory: %w", err)
	}

	var foundIndex bool
	var declaredTotal, actualTotal uint64
	fail := func(err error) error {
		_ = os.RemoveAll(destDir)
		return err
	}
	for _, file := range reader.File {
		if !SafeName(file.Name) {
			return fail(dataFailure("unsafe path in panel archive"))
		}
		mode := file.Mode()
		if mode&os.ModeSymlink != 0 {
			return fail(dataFailure("symlink in panel archive"))
		}
		// Some archives mark links via ModeType without ModeSymlink bit on all platforms.
		if mode&os.ModeType == os.ModeSymlink {
			return fail(dataFailure("symlink in panel archive"))
		}
		if archivePathDepth(file.Name) > limits.maxDepth {
			return fail(dataFailure("panel archive path is too deep"))
		}

		target, err := resolveTarget(destDir, file.Name)
		if err != nil {
			return fail(err)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return fail(fmt.Errorf("create panel directory: %w", err))
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fail(fmt.Errorf("create panel parent directory: %w", err))
		}
		if file.UncompressedSize64 > limits.maxFile {
			return fail(dataFailure("panel archive file is too large"))
		}
		if declaredTotal+file.UncompressedSize64 < declaredTotal || declaredTotal+file.UncompressedSize64 > limits.maxTotal {
			return fail(dataFailure("panel archive is too large"))
		}
		declaredTotal += file.UncompressedSize64
		written, err := extractFile(file, target, limits.maxFile)
		if err != nil {
			return fail(err)
		}
		if actualTotal+uint64(written) < actualTotal || actualTotal+uint64(written) > limits.maxTotal {
			return fail(dataFailure("panel archive is too large"))
		}
		actualTotal += uint64(written)
		if strings.EqualFold(filepath.Base(file.Name), "index.html") {
			foundIndex = true
		}
	}
	if !foundIndex {
		return fail(dataFailure("panel archive is missing index.html"))
	}
	return nil
}

func archivePathDepth(name string) int {
	forward := strings.ReplaceAll(name, "\\", "/")
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(forward)))
	depth := 0
	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." {
			continue
		}
		depth++
	}
	return depth
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

func extractFile(file *zip.File, target string, maxFile uint64) (int64, error) {
	source, err := file.Open()
	if err != nil {
		return 0, dataFailure("open panel archive entry")
	}
	defer source.Close()

	destination, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create panel file: %w", err)
	}
	written, copyErr := io.Copy(destination, io.LimitReader(source, int64(maxFile)+1))
	closeErr := destination.Close()
	if copyErr != nil {
		os.Remove(target)
		return written, dataFailure("extract panel archive entry")
	}
	if uint64(written) > maxFile {
		os.Remove(target)
		return written, dataFailure("panel archive file is too large")
	}
	if closeErr != nil {
		os.Remove(target)
		return written, closeErr
	}
	return written, nil
}

func dataFailure(message string) error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: message}
}
