package subscription

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
)

type originalSubscriptionTransport func(*http.Request) (*http.Response, error)

func (f originalSubscriptionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type originalSubscriptionBody struct {
	io.Reader
	readBytes, closes int
	closeErr          error
}

func (r *originalSubscriptionBody) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.readBytes += n
	return n, err
}
func (r *originalSubscriptionBody) Close() error { r.closes++; return r.closeErr }

type originalSubscriptionReadFailure struct{ err error }

func (r originalSubscriptionReadFailure) Read([]byte) (int, error) { return 0, r.err }

func TestDownloader_StatusBodyPreservesOriginalWithinBudget(t *testing.T) {
	body := &originalSubscriptionBody{Reader: strings.NewReader("token=fixture\n" + strings.Repeat("x", diagnostics.MaxHTTPBodyBytes))}
	d := NewDownloader(DownloaderOptions{Client: &http.Client{Transport: originalSubscriptionTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: body, Request: r}, nil
	})}})
	_, err := d.Fetch(context.Background(), FetchRequest{URL: "https://fixture.invalid/subscription?token=fixture"})
	var detail *diagnostics.HTTPError
	var api protocol.APIError
	if !errors.As(err, &detail) || !strings.HasPrefix(detail.Body, "token=fixture\n") || !strings.Contains(detail.Body, "[truncated]") || detail.URL != "https://fixture.invalid/subscription?token=fixture" {
		t.Fatal("original HTTP failure details missing")
	}
	if body.readBytes != diagnostics.MaxHTTPBodyBytes+1 || body.closes != 1 {
		t.Fatalf("response collection bytes=%d closes=%d", body.readBytes, body.closes)
	}
	if !errors.As(err, &api) || api.Code != protocol.CodeNetworkFailure || strings.Contains(err.Error(), "fixture") {
		t.Fatal("public error contract changed")
	}
}

func TestDownloader_ReadAndCloseFailureKeepBothCauses(t *testing.T) {
	readCause := errors.New("read /private/subscription")
	closeCause := errors.New("close /private/subscription")
	body := &originalSubscriptionBody{Reader: originalSubscriptionReadFailure{readCause}, closeErr: closeCause}
	d := NewDownloader(DownloaderOptions{Client: &http.Client{Transport: originalSubscriptionTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
	})}})
	_, err := d.Fetch(context.Background(), FetchRequest{URL: "https://fixture.invalid/subscription"})
	if !errors.Is(err, readCause) || !errors.Is(err, closeCause) || body.closes != 1 {
		t.Fatalf("read/close cause lost: %v closes=%d", err, body.closes)
	}
	if err.Error() != "read subscription response" {
		t.Fatal("public message changed")
	}
}

func TestDownloader_TransportFailureWinsSynchronousContextCancellation(t *testing.T) {
	transportErr := errors.New("fixture transport failure")
	ctx, cancel := context.WithCancel(context.Background())
	d := NewDownloader(DownloaderOptions{Client: &http.Client{Transport: originalSubscriptionTransport(func(*http.Request) (*http.Response, error) {
		cancel()
		return nil, transportErr
	})}})
	_, err := d.Fetch(ctx, FetchRequest{URL: "https://fixture.invalid/subscription"})
	var api protocol.APIError
	if !errors.Is(err, transportErr) || errors.Is(err, context.Canceled) {
		t.Fatalf("transport failure was replaced by cancellation: %v", err)
	}
	if !errors.As(err, &api) || api.Code != protocol.CodeNetworkFailure || api.Message != "subscription download failed" {
		t.Fatalf("public network classification changed: %v", err)
	}
}

func TestSubscription_ParsingErrorKeepsCause(t *testing.T) {
	_, err := ParseDocument([]byte("proxies: ["))
	if err == nil || err.Error() != "invalid subscription YAML" {
		t.Fatalf("YAML cause lost: %v", err)
	}
	var output bytes.Buffer
	reporter := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, new(slog.LevelVar), "test", nil)), nil)
	reporter(context.Background(), diagnostics.Record{Level: slog.LevelError, Err: err})
	if !strings.Contains(output.String(), "yaml: line") {
		t.Fatal("original YAML parser text missing from file diagnostic")
	}
}

func TestSubscription_DirectoryErrorKeepsCause(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(ServiceOptions{CacheDir: filepath.Join(file, "cache"), CatalogPath: filepath.Join(root, "catalog.yaml")})
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || pathErr.Path == "" {
		t.Fatalf("cache-directory cause lost: %v", err)
	}
}

func TestDownloader_AutoFallbackReportsAttemptAndRecovery(t *testing.T) {
	for _, recovered := range []bool{true, false} {
		t.Run(map[bool]string{true: "recovered", false: "exhausted"}[recovered], func(t *testing.T) {
			var records []diagnostics.Record
			d := &Downloader{
				proxy: &http.Client{Transport: originalSubscriptionTransport(func(*http.Request) (*http.Response, error) { return nil, syscall.ECONNREFUSED })},
				direct: &http.Client{Transport: originalSubscriptionTransport(func(*http.Request) (*http.Response, error) {
					if !recovered {
						return nil, io.ErrUnexpectedEOF
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("proxies: []"))}, nil
				})},
				Reporter: func(_ context.Context, record diagnostics.Record) { records = append(records, record) },
			}
			result, err := d.Fetch(context.Background(), FetchRequest{URL: "https://fixture.invalid/?token=original", Mode: ProxyModeAuto})
			want := 1
			if recovered {
				want = 2
			}
			if len(records) != want || records[0].Event != "download.retry" || records[0].Level != slog.LevelWarn || !errors.Is(records[0].Err, syscall.ECONNREFUSED) {
				t.Fatalf("failed attempt missing: %+v", records)
			}
			if recovered {
				if err != nil || !result.FellBack || records[1].Event != "download.recovered" || records[1].Level != slog.LevelInfo {
					t.Fatalf("recovery changed: %v %+v", err, records)
				}
			} else if !errors.Is(err, io.ErrUnexpectedEOF) || diagnostics.AlreadyReported(err) {
				t.Fatal("final failure must remain with caller owner")
			}
		})
	}
}

func TestService_RefreshFailureKeepsFailedStatusWrite(t *testing.T) {
	cause := errors.New("transport original failure")
	d := NewDownloader(DownloaderOptions{Client: &http.Client{Transport: originalSubscriptionTransport(func(*http.Request) (*http.Response, error) { return nil, cause })}})
	service := openServiceWithFetcher(t, d)
	profile, err := service.Add("fixture", "https://fixture.invalid", ProxyModeDirect)
	if err != nil {
		t.Fatal(err)
	}
	// A regular file cannot serve as the catalog's parent directory on any OS.
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	service.catalogPath = filepath.Join(blocker, "catalog.yaml")
	_, err = service.PrepareRefresh(context.Background(), profile.ID)
	var pathErr *os.PathError
	if !errors.Is(err, cause) || !errors.As(err, &pathErr) {
		t.Fatalf("primary or status write cause missing: %v", err)
	}
	if err.Error() != "subscription download failed" {
		t.Fatal("public primary error changed")
	}
}
