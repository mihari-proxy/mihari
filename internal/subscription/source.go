package subscription

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/mihari-proxy/mihari/internal/platform"
)

// IsFileSource reports whether a source uses the local file URI scheme.
func IsFileSource(source string) bool { return strings.HasPrefix(source, "file:") }

func validateSource(source string) error {
	if IsFileSource(source) {
		canonical, err := platform.FileURI(source)
		if err != nil {
			return dataError("invalid local YAML source", err)
		}
		if canonical != source {
			return dataError("local YAML source must use a canonical file URI")
		}
		return nil
	}
	u, err := url.Parse(source)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return dataError("subscription source must use HTTP, HTTPS or file", err)
	}
	return nil
}

func (s *Service) fetch(ctx context.Context, profile Profile) (FetchResult, error) {
	if !IsFileSource(profile.URL) {
		return s.downloader.Fetch(ctx, FetchRequest{URL: profile.URL, ETag: profile.ETag, LastModified: profile.LastModified, Mode: profile.ProxyMode})
	}
	if err := ctx.Err(); err != nil {
		return FetchResult{}, err
	}
	path, err := platform.FileURIPath(profile.URL)
	if err != nil {
		return FetchResult{}, dataError("decode local YAML source", err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return FetchResult{}, dataError("resolve local YAML source", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return FetchResult{}, dataError("inspect local YAML source", err)
	}
	if !info.Mode().IsRegular() {
		return FetchResult{}, dataError("local YAML source must be a regular file", fmt.Errorf("not a regular file: %s", path))
	}
	file, err := openSourceFile(path)
	if err != nil {
		return FetchResult{}, dataError("open local YAML source", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return FetchResult{}, dataError("inspect opened local YAML source", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return FetchResult{}, dataError("local YAML source changed while opening")
	}
	if opened.Size() > maxDocumentBytes {
		return FetchResult{}, dataError("local YAML source exceeds 16 MiB", fmt.Errorf("file size %d: %s", opened.Size(), path))
	}
	cancelDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(cancelDone)
		_ = file.Close() // Closing the owned reader interrupts a cancelled read.
	})
	defer func() {
		if !stop() {
			<-cancelDone
		}
	}()
	content, err := io.ReadAll(io.LimitReader(file, maxDocumentBytes+1))
	if ctx.Err() != nil {
		return FetchResult{}, ctx.Err()
	}
	if err != nil {
		return FetchResult{}, dataError("read local YAML source", fmt.Errorf("%s: %w", path, err))
	}
	if len(content) > maxDocumentBytes {
		return FetchResult{}, dataError("local YAML source exceeds 16 MiB")
	}
	return FetchResult{Content: content, BaseDir: filepath.Dir(path)}, nil
}
