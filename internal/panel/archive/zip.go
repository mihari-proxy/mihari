// Package archive provides safe extraction helpers for panel distribution zips.
package archive

import (
	"archive/zip"
	"bytes"
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
	maxFile      uint64
	maxTotal     uint64
	maxEntries   int
	maxDepth     int
	requireIndex bool
}

var defaultExtractLimits = extractLimits{
	maxFile:      MaxExtractedFileSize,
	maxTotal:     MaxTotalExtractedBytes,
	maxEntries:   MaxArchiveEntries,
	maxDepth:     MaxArchiveDepth,
	requireIndex: true,
}

// Limits is the shared zip extract budget used by panel installs and install bundles.
type Limits struct {
	MaxFile      uint64
	MaxTotal     uint64
	MaxEntries   int
	MaxDepth     int
	RequireIndex bool
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

// ExtractZipLimited extracts with caller-supplied budgets. It reuses the same
// path, symlink, device, and duplicate policy as ExtractZip.
func ExtractZipLimited(archivePath, destDir string, limits Limits) error {
	return extractZipWithLimits(archivePath, destDir, limits.internal())
}

// ExtractZipBytes extracts through mkdir/write callbacks so callers can stay on a TrustedRoot fd.
func ExtractZipBytes(data []byte, limits Limits, mkdir func(string) error, write func(string, []byte) error) error {
	if int64(len(data)) < 0 {
		return dataFailure("invalid panel archive")
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return dataFailure("invalid panel archive")
	}
	return extractZipFiles(reader.File, limits.internal(), mkdir, write)
}

func (l Limits) internal() extractLimits {
	return extractLimits{
		maxFile: l.MaxFile, maxTotal: l.MaxTotal, maxEntries: l.MaxEntries,
		maxDepth: l.MaxDepth, requireIndex: l.RequireIndex,
	}
}

func extractZipWithLimits(archivePath, destDir string, limits extractLimits) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return dataFailure("invalid panel archive")
	}
	defer reader.Close()
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return fmt.Errorf("create panel extract directory: %w", err)
	}
	err = extractZipFiles(reader.File, limits, func(name string) error {
		return os.MkdirAll(filepath.Join(destDir, filepath.FromSlash(name)), 0o700)
	}, func(name string, body []byte) error {
		path := filepath.Join(destDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return os.WriteFile(path, body, 0o600)
	})
	if err != nil {
		_ = os.RemoveAll(destDir)
	}
	return err
}

func extractZipFiles(files []*zip.File, limits extractLimits, mkdir func(string) error, write func(string, []byte) error) error {
	if limits.maxEntries > 0 && len(files) > limits.maxEntries {
		return dataFailure("panel archive has too many entries")
	}
	var foundIndex bool
	var declaredTotal, actualTotal uint64
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		if !SafeName(file.Name) {
			return dataFailure("unsafe path in panel archive")
		}
		cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.ReplaceAll(file.Name, "\\", "/"))))
		if seen[cleaned] {
			return dataFailure("duplicate path in panel archive")
		}
		seen[cleaned] = true
		mode := file.Mode()
		if mode&os.ModeSymlink != 0 || mode&os.ModeType == os.ModeSymlink {
			return dataFailure("symlink in panel archive")
		}
		if mode&os.ModeDevice != 0 || mode&os.ModeCharDevice != 0 || mode&os.ModeNamedPipe != 0 || mode&os.ModeSocket != 0 {
			return dataFailure("device in panel archive")
		}
		if archivePathDepth(file.Name) > limits.maxDepth {
			return dataFailure("panel archive path is too deep")
		}
		if file.FileInfo().IsDir() {
			if mkdir != nil {
				if err := mkdir(cleaned); err != nil {
					return fmt.Errorf("create panel directory: %w", err)
				}
			}
			continue
		}
		if file.UncompressedSize64 > limits.maxFile {
			return dataFailure("panel archive file is too large")
		}
		if declaredTotal+file.UncompressedSize64 < declaredTotal || declaredTotal+file.UncompressedSize64 > limits.maxTotal {
			return dataFailure("panel archive is too large")
		}
		declaredTotal += file.UncompressedSize64
		body, err := readZipFile(file, limits.maxFile)
		if err != nil {
			return err
		}
		if actualTotal+uint64(len(body)) < actualTotal || actualTotal+uint64(len(body)) > limits.maxTotal {
			return dataFailure("panel archive is too large")
		}
		actualTotal += uint64(len(body))
		if write != nil {
			if err := write(cleaned, body); err != nil {
				return err
			}
		}
		if strings.EqualFold(filepath.Base(file.Name), "index.html") {
			foundIndex = true
		}
	}
	if limits.requireIndex && !foundIndex {
		return dataFailure("panel archive is missing index.html")
	}
	return nil
}

func readZipFile(file *zip.File, maxFile uint64) ([]byte, error) {
	source, err := file.Open()
	if err != nil {
		return nil, dataFailure("open panel archive entry")
	}
	defer source.Close()
	body, err := io.ReadAll(io.LimitReader(source, int64(maxFile)+1))
	if err != nil {
		return nil, dataFailure("extract panel archive entry")
	}
	if uint64(len(body)) > maxFile {
		return nil, dataFailure("panel archive file is too large")
	}
	return body, nil
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
