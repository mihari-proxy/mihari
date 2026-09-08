package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"path"
	"strings"
)

// ExtractTarGzipBytes extracts a Unix release bundle through capability callbacks.
// ZIP and tar share SafeName, depth and caller budgets. Only ordinary files and
// directories are accepted; links, devices and sparse/extension entries fail.
func ExtractTarGzipBytes(data []byte, limits Limits, mkdir func(string) error, write func(string, []byte) error) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return dataFailure("invalid install archive")
	}
	defer gz.Close()
	gz.Multistream(false)
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	var total uint64
	count := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return dataFailure("invalid install archive")
		}
		count++
		if limits.MaxEntries <= 0 || count > limits.MaxEntries {
			return dataFailure("install archive has too many entries")
		}
		if !SafeName(h.Name) || strings.ContainsRune(h.Name, 0) {
			return dataFailure("unsafe path in install archive")
		}
		name := path.Clean(strings.ReplaceAll(h.Name, "\\", "/"))
		if seen[name] || archivePathDepth(name) > limits.MaxDepth {
			return dataFailure("duplicate or deep install archive path")
		}
		seen[name] = true
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
			return dataFailure("unsupported install archive entry")
		}
		for key := range h.PAXRecords {
			if strings.HasPrefix(key, "GNU.sparse.") {
				return dataFailure("unsupported sparse install archive")
			}
		}
		if h.Typeflag == tar.TypeDir {
			if mkdir != nil {
				if err := mkdir(name); err != nil {
					return err
				}
			}
			continue
		}
		if name == "." || h.Size < 0 || uint64(h.Size) > limits.MaxFile || uint64(h.Size) > limits.MaxTotal-total {
			return dataFailure("install archive exceeds size limit")
		}
		total += uint64(h.Size)
		body, err := io.ReadAll(io.LimitReader(tr, h.Size+1))
		if err != nil || int64(len(body)) != h.Size {
			return dataFailure("truncated install archive")
		}
		if write != nil {
			if err := write(name, body); err != nil {
				return err
			}
		}
	}
	// Drain the gzip member to verify its footer and CRC, even when tar ended early.
	if n, err := io.Copy(io.Discard, io.LimitReader(gz, (1<<20)+1)); err != nil || n > 1<<20 {
		return dataFailure("invalid compressed install archive")
	}
	return nil
}
