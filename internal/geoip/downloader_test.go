package geoip

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloader_PreparesValidatedCandidateAndRetainsPreviousFile(t *testing.T) {
	payload := []byte("valid-mmdb-fixture")
	sum := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, ".sha256sum") {
			_, _ = io.WriteString(writer, hex.EncodeToString(sum[:])+"  database.mmdb\n")
			return
		}
		_, _ = writer.Write(payload)
	}))
	defer server.Close()
	root := t.TempDir()
	destination := filepath.Join(root, "geoip", "database.mmdb")
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	validated := false
	downloader := Downloader{
		Client: server.Client(), StagingDir: filepath.Join(root, "staging"), AllowHTTP: true, MaxBytes: 1024,
		Validate: func(path string) error {
			validated = true
			got, err := os.ReadFile(path)
			if err != nil || string(got) != string(payload) {
				t.Fatalf("candidate=%q err=%v", got, err)
			}
			return nil
		},
	}
	candidate, err := downloader.Prepare(context.Background(), DownloadSpec{
		URL: server.URL + "/database.mmdb", ChecksumURL: server.URL + "/database.mmdb.sha256sum", Destination: destination,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.Cleanup()
	if !validated {
		t.Fatal("candidate was not validated")
	}
	if err := candidate.Commit(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(destination)
	previous, _ := os.ReadFile(destination + ".previous")
	if string(got) != string(payload) || string(previous) != "previous" {
		t.Fatalf("current=%q previous=%q", got, previous)
	}
}

func TestDownloader_RejectsStatusSizeChecksumAndInvalidCandidate(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		payload   string
		checksum  string
		maxBytes  int64
		validator func(string) error
	}{
		{name: "status", status: http.StatusBadGateway, payload: "x"},
		{name: "size", status: http.StatusOK, payload: "too-large", maxBytes: 2},
		{name: "checksum", status: http.StatusOK, payload: "x", checksum: strings.Repeat("0", 64)},
		{name: "reader validation", status: http.StatusOK, payload: "x", validator: func(string) error { return errors.New("invalid mmdb") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := []byte(test.payload)
			sum := sha256.Sum256(payload)
			checksum := test.checksum
			if checksum == "" {
				checksum = hex.EncodeToString(sum[:])
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if strings.HasSuffix(request.URL.Path, ".sha256sum") {
					_, _ = io.WriteString(writer, checksum)
					return
				}
				writer.WriteHeader(test.status)
				_, _ = writer.Write(payload)
			}))
			defer server.Close()
			maxBytes := test.maxBytes
			if maxBytes == 0 {
				maxBytes = 1024
			}
			downloader := Downloader{Client: server.Client(), StagingDir: t.TempDir(), AllowHTTP: true, MaxBytes: maxBytes, Validate: test.validator}
			candidate, err := downloader.Prepare(context.Background(), DownloadSpec{
				URL: server.URL + "/db", ChecksumURL: server.URL + "/db.sha256sum", Destination: filepath.Join(t.TempDir(), "db"),
			})
			if err == nil {
				candidate.Cleanup()
				t.Fatal("prepare succeeded")
			}
			entries, _ := os.ReadDir(downloader.StagingDir)
			if len(entries) != 0 {
				t.Fatalf("staging entries=%v", entries)
			}
		})
	}
}

func TestDownloader_RejectsHTTPSRedirectDowngrade(t *testing.T) {
	insecure := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, strings.Repeat("0", 64))
	}))
	defer insecure.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, insecure.URL, http.StatusFound)
	}))
	defer secure.Close()
	downloader := Downloader{Client: secure.Client(), StagingDir: t.TempDir()}
	_, err := downloader.Prepare(context.Background(), DownloadSpec{
		URL: secure.URL + "/db", ChecksumURL: secure.URL + "/db.sha256sum", Destination: filepath.Join(t.TempDir(), "db"),
	})
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("err=%v", err)
	}
}

func TestDownloader_PropagatesContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	downloader := Downloader{Client: server.Client(), StagingDir: t.TempDir(), AllowHTTP: true}
	_, err := downloader.Prepare(ctx, DownloadSpec{
		URL: server.URL + "/db", ChecksumURL: server.URL + "/db.sha256sum", Destination: filepath.Join(t.TempDir(), "db"),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
