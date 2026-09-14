package update

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

type updateDiagnosticBody struct {
	io.Reader
	closeErr          error
	closes, readBytes int
}

func (b *updateDiagnosticBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.readBytes += n
	return n, err
}
func (b *updateDiagnosticBody) Close() error { b.closes++; return b.closeErr }

type updateReadFailure struct{ err error }

func (r updateReadFailure) Read([]byte) (int, error) { return 0, r.err }

func updateHTTPEntrypoints(t *testing.T) map[string]func(*http.Client) error {
	t.Helper()
	destination := filepath.Join(t.TempDir(), "candidate")
	return map[string]func(*http.Client) error{
		"latest": func(c *http.Client) error {
			_, err := (SelfUpdater{HTTPClient: c}).latestMainRelease(t.Context())
			return err
		},
		"tag": func(c *http.Client) error {
			_, err := (SelfUpdater{HTTPClient: c}).releaseByTag(t.Context(), "v1.2.3")
			return err
		},
		"list": func(c *http.Client) error {
			_, _, err := (SelfUpdater{HTTPClient: c}).fetchReleaseListPage(t.Context(), "https://fixture.invalid/releases?token=fixture")
			return err
		},
		"checksum": func(c *http.Client) error {
			_, err := (SelfUpdater{HTTPClient: c}).fetchExpectedChecksum(t.Context(), Asset{URL: "https://fixture.invalid/checksum?token=fixture"}, "candidate")
			return err
		},
		"binary": func(c *http.Client) error {
			return (SelfUpdater{HTTPClient: c}).download(t.Context(), Asset{URL: "https://fixture.invalid/binary?token=fixture"}, sha256.Sum256(nil), destination)
		},
		"official": func(c *http.Client) error {
			_, err := (OfficialReleaseSource{Client: c}).Download(t.Context(), "v1.2.3", "asset", 1024)
			return err
		},
	}
}

func TestUpdateHTTP_TransportCauseSurvivesPublicMapping(t *testing.T) {
	cause := errors.New("fixture transport token=fixture /private/update")
	for name, run := range updateHTTPEntrypoints(t) {
		t.Run(name, func(t *testing.T) {
			err := run(&http.Client{Transport: officialTransport(func(*http.Request) (*http.Response, error) { return nil, cause })})
			var api protocol.APIError
			var detail *diagnostics.HTTPError
			if !errors.Is(err, cause) || !errors.As(err, &detail) || detail.URL == "" || detail.Phase != "transport" || !errors.As(err, &api) || api.Code != protocol.CodeNetworkFailure || strings.Contains(err.Error(), "fixture") {
				t.Fatalf("transport cause or public classification lost: %v", err)
			}
		})
	}
}

func TestUpdateHTTP_StatusBodyBoundedAndCloseCauseRetained(t *testing.T) {
	for name, run := range updateHTTPEntrypoints(t) {
		t.Run(name, func(t *testing.T) {
			closeCause := errors.New("fixture close")
			body := &updateDiagnosticBody{Reader: strings.NewReader("token=fixture\n" + strings.Repeat("x", diagnostics.MaxHTTPBodyBytes)), closeErr: closeCause}
			err := run(&http.Client{Transport: officialTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 503, Body: body, Request: r, Header: make(http.Header)}, nil
			})})
			var detail *diagnostics.HTTPError
			var api protocol.APIError
			if !errors.As(err, &detail) || detail.Status != 503 || detail.URL == "" || !strings.HasPrefix(detail.Body, "token=fixture\n") || !strings.Contains(detail.Body, "[truncated]") {
				t.Fatal("HTTP failure source/body missing")
			}
			if !errors.Is(err, closeCause) || body.closes != 1 || body.readBytes != diagnostics.MaxHTTPBodyBytes+1 {
				t.Fatalf("resource accounting or close cause lost: closes=%d bytes=%d", body.closes, body.readBytes)
			}
			if !errors.As(err, &api) || strings.Contains(err.Error(), "fixture") || (name == "official" && api.Details != nil) {
				t.Fatal("public error changed")
			}
		})
	}
}

func TestUpdateHTTP_ReadAndCloseFailuresBothSurvive(t *testing.T) {
	for name, run := range updateHTTPEntrypoints(t) {
		t.Run(name, func(t *testing.T) {
			readCause, closeCause := errors.New("fixture read"), errors.New("fixture close")
			body := &updateDiagnosticBody{Reader: updateReadFailure{readCause}, closeErr: closeCause}
			err := run(&http.Client{Transport: officialTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: body, Request: r, Header: make(http.Header)}, nil
			})})
			var detail *diagnostics.HTTPError
			if !errors.As(err, &detail) || detail.URL == "" || detail.Phase != "read" || !errors.Is(err, readCause) || !errors.Is(err, closeCause) || body.closes != 1 || strings.Contains(err.Error(), "fixture") {
				t.Fatalf("read/close causes or public message lost: %v closes=%d", err, body.closes)
			}
		})
	}
}

func TestUpdateHTTP_ReleaseJSONCauseRetained(t *testing.T) {
	for name, run := range updateHTTPEntrypoints(t) {
		if name != "latest" && name != "tag" && name != "list" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			err := run(&http.Client{Transport: officialTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{")), Header: make(http.Header)}, nil
			})})
			var syntax *json.SyntaxError
			if !errors.As(err, &syntax) {
				t.Fatal("JSON parse cause lost")
			}
		})
	}
}

func TestUpdateChannel_IOCauseRetained(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadChannel(dir)
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || err.Error() != "read mihari channel" {
		t.Fatalf("read cause lost: %v", err)
	}
	block := filepath.Join(dir, "file")
	if err := os.WriteFile(block, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	err = SaveChannel(filepath.Join(block, "channel"), ChannelMain)
	if !errors.As(err, &pathErr) || err.Error() != "write mihari channel" {
		t.Fatalf("write cause lost: %v", err)
	}
}

func TestUpdateDownload_WriteAndCloseCausesRetained(t *testing.T) {
	writeCause, closeCause := errors.New("fixture write"), errors.New("fixture file close")
	u := SelfUpdater{HTTPClient: &http.Client{Transport: officialTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("binary"))}, nil
	})},
		openCandidate: func(string) (io.WriteCloser, error) {
			return failWriter{writeErr: writeCause, closeErr: closeCause}, nil
		}}
	err := u.download(context.Background(), Asset{URL: "https://fixture.invalid/binary"}, sha256.Sum256(nil), filepath.Join(t.TempDir(), "candidate"))
	if !errors.Is(err, writeCause) || !errors.Is(err, closeCause) || err.Error() != "read mihari asset failed" {
		t.Fatalf("candidate causes lost: %v", err)
	}
}
