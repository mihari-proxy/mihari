package core

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
)

type originalCoreRunner struct {
	output []byte
	err    error
}

func TestCoreOriginal_ProvenanceParserCausePreserved(t *testing.T) {
	err := decodeStrict([]byte("{"), &pairCommit{})
	if !hasCorePrivateCause(err) || err.Error() != "invalid provenance JSON" {
		t.Fatalf("provenance parser cause or public message changed: %v", err)
	}
}

func hasCorePrivateCause(err error) bool {
	if err == nil {
		return false
	}
	if _, public := err.(protocol.APIError); !public {
		if _, multi := err.(interface{ Unwrap() []error }); !multi {
			return true
		}
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range wrapped.Unwrap() {
			if hasCorePrivateCause(child) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		return hasCorePrivateCause(wrapped.Unwrap())
	}
	return false
}

func (r originalCoreRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return r.output, r.err
}

type originalCoreBody struct {
	io.Reader
	closeErr          error
	closes, readBytes int
}

func (b *originalCoreBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.readBytes += n
	return n, err
}
func (b *originalCoreBody) Close() error { b.closes++; return b.closeErr }

func TestCoreOriginal_CommandOutputPreservedInFileDiagnostics(t *testing.T) {
	cause := errors.New("fixture process exit")
	output := []byte("token=fixture config.yaml: line 42 invalid node")
	for _, method := range []string{"version", "validation", "parse"} {
		t.Run(method, func(t *testing.T) {
			var err error
			runner := originalCoreRunner{output, cause}
			switch method {
			case "version":
				_, err = DetectVersion(t.Context(), runner, "/private/mihomo")
			case "validation":
				err = ValidateConfig(t.Context(), runner, "/private/mihomo", "data", "config")
			case "parse":
				_, err = ParseVersion(string(output))
			}
			if method != "parse" && !errors.Is(err, cause) {
				t.Fatal("command cause lost")
			}
			if err == nil || strings.Contains(err.Error(), "fixture") {
				t.Fatal("public output changed")
			}
			var out bytes.Buffer
			logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&out, &slog.LevelVar{}, "test", nil)), nil)(t.Context(), diagnostics.Record{Component: "core", Event: "failed", Level: slog.LevelError, Err: err})
			if !strings.Contains(out.String(), string(output)) {
				t.Fatal("original command output missing from file log")
			}
		})
	}
}

func coreHTTPEntrypoints(t *testing.T) map[string]func(*http.Client) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	return map[string]func(*http.Client) error{
		"release": func(c *http.Client) error {
			_, err := (Installer{HTTPClient: c}).LatestRelease(t.Context(), "stable")
			return err
		},
		"legacy": func(c *http.Client) error {
			return (Installer{HTTPClient: c}).Download(t.Context(), Asset{URL: "https://fixture.invalid/core?token=fixture"}, path)
		},
		"trusted": func(c *http.Client) error {
			i := Installer{HTTPClient: c, GOOS: "linux", GOARCH: "amd64", GeneratedConfig: func(context.Context) (*ConfigCapability, error) { return nil, nil }}
			_, err := i.prepareTrusted(t.Context(), InstallRequest{})
			return err
		},
	}
}

func TestCoreOriginal_HTTPFailurePreservesBodyAndClose(t *testing.T) {
	for name, run := range coreHTTPEntrypoints(t) {
		t.Run(name, func(t *testing.T) {
			cause := errors.New("fixture close")
			body := &originalCoreBody{Reader: strings.NewReader("token=fixture\n" + strings.Repeat("x", diagnostics.MaxHTTPBodyBytes)), closeErr: cause}
			err := run(&http.Client{Transport: diagnosticRoundTripper(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 503, Body: body, Request: r, Header: make(http.Header)}, nil
			})})
			var detail *diagnostics.HTTPError
			if !errors.As(err, &detail) || detail.Status != 503 || detail.URL == "" || !strings.HasPrefix(detail.Body, "token=fixture\n") || !strings.Contains(detail.Body, "[truncated]") {
				t.Fatal("HTTP cause/body lost")
			}
			if !errors.Is(err, cause) || body.closes != 1 || body.readBytes != diagnostics.MaxHTTPBodyBytes+1 {
				t.Fatalf("close cause/accounting lost: closes=%d bytes=%d", body.closes, body.readBytes)
			}
			if strings.Contains(err.Error(), "fixture") {
				t.Fatal("public error leaked raw details")
			}
		})
	}
}

func TestCoreOriginal_HTTPTransportCausePreserved(t *testing.T) {
	for name, run := range coreHTTPEntrypoints(t) {
		t.Run(name, func(t *testing.T) {
			cause := errors.New("fixture network")
			err := run(&http.Client{Transport: diagnosticRoundTripper(func(*http.Request) (*http.Response, error) { return nil, cause })})
			if !errors.Is(err, cause) {
				t.Fatal("transport cause lost")
			}
		})
	}
}

func TestCoreOriginal_InvalidDownloadURLPreservesCause(t *testing.T) {
	err := (Installer{}).Download(t.Context(), Asset{URL: "://invalid"}, filepath.Join(t.TempDir(), "archive"))
	var api protocol.APIError
	var parseErr *url.Error
	if !errors.As(err, &api) || api.Code != protocol.CodeInternal || api.Message != "create core download request" {
		t.Fatalf("public request error changed: %v", err)
	}
	if !errors.As(err, &parseErr) || err.Error() != "create core download request" {
		t.Fatalf("private URL parse cause lost: %v", err)
	}
}

func TestCoreOriginal_ArchiveAndFilesystemCausePreserved(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "archive")
	if err := os.WriteFile(archive, bytes.Repeat([]byte("x"), 32), 0600); err != nil {
		t.Fatal(err)
	}
	if err := extractGzip(archive, filepath.Join(dir, "out")); !errors.Is(err, gzip.ErrHeader) {
		t.Fatalf("gzip cause lost: %v", err)
	}
	if err := extractZip(archive, filepath.Join(dir, "out")); !errors.Is(err, zip.ErrFormat) {
		t.Fatalf("zip cause lost: %v", err)
	}
	var pathErr *os.PathError
	if err := writeCandidate(filepath.Join(dir, "missing"), strings.NewReader("x")); !errors.As(err, &pathErr) {
		t.Fatalf("candidate open cause lost: %v", err)
	}
	block := filepath.Join(dir, "block")
	if err := os.WriteFile(block, nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := (&Candidate{updated: true, path: archive, binaryPath: filepath.Join(block, "mihomo")}).Commit()
	if !errors.As(err, &pathErr) || err.Error() != "create core binary directory" {
		t.Fatalf("commit cause lost: %v", err)
	}
}

func TestCoreOriginal_RuntimeConfigCausePreserved(t *testing.T) {
	dir := t.TempDir()
	err := EnsureRuntimeConfig(dir, config.Settings{})
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Fatal("config read cause lost")
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("proxies: ["), 0600); err != nil {
		t.Fatal(err)
	}
	err = EnsureRuntimeConfig(path, config.Settings{})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Message != "invalid runtime configuration" {
		t.Fatal("public parse message changed")
	}
	var out bytes.Buffer
	logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&out, &slog.LevelVar{}, "test", nil)), nil)(t.Context(), diagnostics.Record{Component: "core", Event: "failed", Level: slog.LevelError, Err: err})
	if !strings.Contains(out.String(), "yaml:") {
		t.Fatal("config parser diagnostic lost")
	}
}
