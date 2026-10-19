package geoip

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

type geoIPDiagnosticTransport func(*http.Request) (*http.Response, error)

func TestGeoIPHTTP_GitHubRateLimitSummary(t *testing.T) {
	for _, checksum := range []bool{false, true} {
		closeCause := errors.New("fixture close")
		body := &geoIPDiagnosticBody{Reader: strings.NewReader("API rate limit exceeded"), closeErr: closeCause}
		client := &http.Client{Transport: geoIPDiagnosticTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 403, Header: make(http.Header), Body: body, Request: r}, nil
		})}
		var err error
		if checksum {
			_, err = downloadChecksum(t.Context(), client, DefaultCountryChecksumURL, false, nil)
		} else {
			_, err = downloadFile(t.Context(), client, DefaultCountryURL, io.Discard, 1024, false, nil)
		}
		if SafeFailureReason(err) != diagnostics.GitHubRateLimitSummary || !errors.Is(err, closeCause) || body.closes != 1 {
			t.Fatalf("checksum=%t error=%v closes=%d", checksum, err, body.closes)
		}
	}
}

func (f geoIPDiagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type geoIPDiagnosticBody struct {
	io.Reader
	closeErr          error
	closes, readBytes int
}

func (b *geoIPDiagnosticBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.readBytes += n
	return n, err
}
func (b *geoIPDiagnosticBody) Close() error { b.closes++; return b.closeErr }

func TestGeoIPHTTP_StatusBodyAndCloseCauseArePreserved(t *testing.T) {
	closeCause := errors.New("fixture geoip close")
	body := &geoIPDiagnosticBody{Reader: strings.NewReader("token=fixture\n" + strings.Repeat("x", diagnostics.MaxHTTPBodyBytes)), closeErr: closeCause}
	client := &http.Client{Transport: geoIPDiagnosticTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: body, Request: r}, nil
	})}
	_, err := downloadChecksum(t.Context(), client, "https://fixture.invalid/geoip?token=fixture", false, nil)
	var detail *diagnostics.HTTPError
	if !errors.As(err, &detail) || detail.URL != "https://fixture.invalid/geoip?token=fixture" || !strings.HasPrefix(detail.Body, "token=fixture\n") || !strings.Contains(detail.Body, "[truncated]") || !detail.BodyTruncated {
		t.Fatalf("HTTP detail lost: %v", err)
	}
	if !errors.Is(err, closeCause) || body.closes != 1 || body.readBytes != diagnostics.MaxHTTPBodyBytes+1 {
		t.Fatalf("close/body accounting lost: %v closes=%d bytes=%d", err, body.closes, body.readBytes)
	}
}

func TestGeoIPHTTP_SuccessCloseFailureIsWarning(t *testing.T) {
	closeCause := errors.New("fixture geoip close")
	body := &geoIPDiagnosticBody{Reader: strings.NewReader(strings.Repeat("0", 64)), closeErr: closeCause}
	client := &http.Client{Transport: geoIPDiagnosticTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: r}, nil
	})}
	var records []diagnostics.Record
	_, err := downloadChecksum(context.Background(), client, "https://fixture.invalid/geoip", false, func(_ context.Context, record diagnostics.Record) { records = append(records, record) })
	if err != nil || len(records) != 1 || records[0].Level != slog.LevelWarn || !errors.Is(records[0].Err, closeCause) {
		t.Fatalf("success or warning changed: %v records=%+v", err, records)
	}
}
