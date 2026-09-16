package release

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

type githubDiagnosticTransport func(*http.Request) (*http.Response, error)

func (f githubDiagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGitHubDiagnostics_PreservesTransportAndDecodeCauses(t *testing.T) {
	transportErr := errors.New("proxy dial failed at https://example.test?token=fixture")
	client := Client{HTTPClient: &http.Client{Transport: githubDiagnosticTransport(func(*http.Request) (*http.Response, error) { return nil, transportErr })}}
	_, err := client.LatestRelease(context.Background(), "owner", "repo")
	if !errors.Is(err, transportErr) {
		t.Fatal("transport cause was replaced")
	}
	var detail *diagnostics.HTTPError
	if !errors.As(err, &detail) || detail.URL == "" || detail.Phase != "transport" {
		t.Fatal("transport source was discarded")
	}
	var public protocol.APIError
	if !errors.As(err, &public) || public.Code != protocol.CodeNetworkFailure || public.Message != "fetch github resource failed" {
		t.Fatal("public network classification changed")
	}
	client.HTTPClient.Transport = githubDiagnosticTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"tag_name":`))}, nil
	})
	_, err = client.LatestRelease(context.Background(), "owner", "repo")
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) {
		t.Fatal("JSON syntax cause was replaced")
	}
	if !errors.As(err, &detail) || detail.URL == "" || detail.Phase != "decode" {
		t.Fatal("decode source was discarded")
	}
	if !errors.As(err, &public) || public.Message != "invalid github response" {
		t.Fatal("public decode message changed")
	}
}

type githubDiagnosticBody struct {
	io.Reader
	closeErr error
	closes   int
}

func (b *githubDiagnosticBody) Close() error { b.closes++; return b.closeErr }

func TestGitHubDiagnostics_CloseFailureRetainsPrimaryCause(t *testing.T) {
	closeErr := errors.New("close github response fixture-token")
	body := &githubDiagnosticBody{Reader: strings.NewReader(`{"tag_name":`), closeErr: closeErr}
	client := Client{HTTPClient: &http.Client{Transport: githubDiagnosticTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
	})}}
	_, err := client.LatestRelease(context.Background(), "owner", "repo")
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) || !errors.Is(err, closeErr) || body.closes != 1 || err.Error() != "invalid github response" {
		t.Fatal("close failure replaced primary cause, was discarded, or changed public result")
	}
}

func TestGitHubDiagnostics_CloseWarningKeepsSuccessfulResult(t *testing.T) {
	closeErr := errors.New("close github response fixture-token")
	body := &githubDiagnosticBody{Reader: strings.NewReader(`{"tag_name":"v1"}`), closeErr: closeErr}
	var records []diagnostics.Record
	client := Client{HTTPClient: &http.Client{Transport: githubDiagnosticTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
	})}}
	// The caller supplies its existing file reporter; the client creates no logs.
	client.Reporter = func(_ context.Context, record diagnostics.Record) { records = append(records, record) }
	release, err := client.LatestRelease(context.Background(), "owner", "repo")
	if err != nil || release.TagName != "v1" || body.closes != 1 || len(records) != 1 || records[0].Level != slog.LevelWarn || !errors.Is(records[0].Err, closeErr) {
		t.Fatal("close warning lost or changed completed successful result")
	}
}

func TestGitHubDiagnostics_RetainsHTTPFailureBodyPrivately(t *testing.T) {
	const body = `{"message":"release access denied","token":"fixture-secret"}`
	client := Client{HTTPClient: &http.Client{Transport: githubDiagnosticTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	_, err := client.LatestRelease(context.Background(), "owner", "repo")
	var detail *diagnostics.HTTPError
	if !errors.As(err, &detail) || detail.Body != body || detail.Status != 403 {
		t.Fatal("upstream failure body was discarded")
	}
	if strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "access denied") {
		t.Fatal("internal response body escaped into public message")
	}
}

func TestGitHubDiagnostics_CollectionLimitIsStructured(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		original := "literal [truncated] token=fixture"
		if oversized {
			original += strings.Repeat("x", diagnostics.MaxHTTPBodyBytes)
		}
		client := Client{HTTPClient: &http.Client{Transport: githubDiagnosticTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(original))}, nil
		})}}
		_, err := client.LatestRelease(context.Background(), "owner", "repo")
		var detail *diagnostics.HTTPError
		if !errors.As(err, &detail) || detail.BodyTruncated != oversized {
			t.Fatalf("structured collection state missing for oversized=%v", oversized)
		}
		if !oversized && diagnostics.Capture(err).Truncated {
			t.Fatal("literal truncation marker was interpreted as metadata")
		}
	}
}
