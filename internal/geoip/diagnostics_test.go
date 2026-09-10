package geoip

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeoIPDiagnostic_PairOpenFailureKeepsBothRestoreCauses(t *testing.T) {
	root := t.TempDir()
	countryPath := filepath.Join(root, "country.mmdb")
	asnPath := filepath.Join(root, "asn.mmdb")
	for _, path := range []string{countryPath, asnPath} {
		if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	server := geoIPPairServer(t, []byte("country"), []byte("asn"))
	defer server.Close()
	countryCause := &os.PathError{Op: "open", Path: "/private/business-secret", Err: os.ErrPermission}
	asnCause := errors.New("asn open failed")
	countryReopenCause := errors.New("country reopen failed")
	asnReopenCause := errors.New("asn reopen failed")
	opens := 0
	var openOrder []string
	service := New(ServiceOptions{CountryPath: countryPath, ASNPath: asnPath, CountryURL: server.URL + "/country", CountryChecksumURL: server.URL + "/country.sha256sum", ASNURL: server.URL + "/asn", ASNChecksumURL: server.URL + "/asn.sha256sum", Downloader: Downloader{Client: server.Client(), StagingDir: filepath.Join(root, "staging"), AllowHTTP: true, Validate: func(string) error { return nil }}, OpenDatabase: func(path string) (databaseReader, error) {
		opens++
		openOrder = append(openOrder, path)
		if opens == 3 || opens == 4 {
			if err := os.Remove(path + ".previous"); err != nil {
				t.Fatal(err)
			}
			if opens == 3 {
				return nil, countryCause
			}
			return nil, asnCause
		}
		if opens == 5 {
			return nil, countryReopenCause
		}
		if opens == 6 {
			return nil, asnReopenCause
		}
		return &fakeReader{}, nil
	}})
	defer service.Close()
	candidate, err := service.PrepareUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.Cleanup()
	err = candidate.Commit()
	if !errors.Is(err, countryCause) || !errors.Is(err, asnCause) || !errors.Is(err, os.ErrNotExist) || !errors.Is(err, countryReopenCause) || !errors.Is(err, asnReopenCause) {
		t.Fatalf("pair restoration cause lost: %v", err)
	}
	if err.Error() != errors.Join(countryCause, asnCause).Error() {
		t.Fatalf("primary public text changed: %v", err)
	}
	if opens != 6 || service.generation != 0 {
		t.Fatalf("reopen/generation changed opens=%d generation=%d", opens, service.generation)
	}
	wantOrder := []string{countryPath, asnPath, countryPath, asnPath, countryPath, asnPath}
	if strings.Join(openOrder, "\n") != strings.Join(wantOrder, "\n") {
		t.Fatalf("open order=%q want=%q", openOrder, wantOrder)
	}
}

func TestGeoIPDiagnostic_CommitFailuresKeepReopenCauses(t *testing.T) {
	for _, failure := range []string{"country", "asn"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			countryPath := filepath.Join(root, "country.mmdb")
			asnPath := filepath.Join(root, "asn.mmdb")
			country := testFileCandidate(t, filepath.Join(root, "country.staged"), countryPath, "country")
			asn := testFileCandidate(t, filepath.Join(root, "asn.staged"), asnPath, "asn")
			blockedPrevious := countryPath + ".previous"
			if failure == "asn" {
				blockedPrevious = asnPath + ".previous"
			}
			if err := os.Mkdir(blockedPrevious, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(blockedPrevious, "owned"), []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}

			countryReopenCause := errors.New("country reopen failed")
			asnReopenCause := errors.New("asn reopen failed")
			var openOrder []string
			service := &Service{
				countryPath: countryPath,
				asnPath:     asnPath,
				country:     &fakeReader{},
				asn:         &fakeReader{},
				openDatabase: func(path string) (databaseReader, error) {
					openOrder = append(openOrder, path)
					if path == countryPath {
						return nil, countryReopenCause
					}
					return nil, asnReopenCause
				},
			}
			candidate := &PreparedUpdate{service: service, country: country, asn: asn}
			err := candidate.Commit()
			if !errors.Is(err, countryReopenCause) || !errors.Is(err, asnReopenCause) {
				t.Fatalf("%s commit lost reopen causes: %v", failure, err)
			}
			if !strings.HasPrefix(err.Error(), "remove retained geoip database: ") || strings.Contains(err.Error(), "reopen failed") {
				t.Fatalf("%s primary text changed: %v", failure, err)
			}
			if strings.Join(openOrder, "\n") != strings.Join([]string{countryPath, asnPath}, "\n") || service.generation != 0 {
				t.Fatalf("%s recovery order=%q generation=%d", failure, openOrder, service.generation)
			}
		})
	}
}

func testFileCandidate(t *testing.T, staged, destination, value string) *FileCandidate {
	t.Helper()
	payload := []byte(value)
	if err := os.WriteFile(staged, payload, 0600); err != nil {
		t.Fatal(err)
	}
	return &FileCandidate{staged: staged, destination: destination, digest: sha256.Sum256(payload)}
}

func TestGeoIPDiagnostic_RecoveryAggregateKeepsTextAndNewOwner(t *testing.T) {
	cause := &os.PathError{Op: "open", Path: "/private/business-secret", Err: os.ErrPermission}
	primary := diagnostics.MarkReported(cause)
	if !diagnostics.AlreadyReported(joinUpdateRecovery(primary, nil)) {
		t.Fatal("unchanged primary lost marker")
	}
	recovery := &os.PathError{Op: "rename", Path: "/private/restore-secret", Err: os.ErrNotExist}
	combined := joinUpdateRecovery(primary, recovery)
	if diagnostics.AlreadyReported(combined) || combined.Error() != primary.Error() || !errors.Is(combined, cause) || !errors.Is(combined, recovery) {
		t.Fatal("new recovery ownership/text/cause changed")
	}
	var output bytes.Buffer
	level := new(slog.LevelVar)
	redactor := logging.NewRedactor("business-secret", "restore-secret")
	report := logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&output, level, "daemon", redactor)), redactor)
	report(logging.WithOperation(context.Background(), logging.OperationMetadata{ID: "geoip-pair", Name: "geoip.update"}), diagnostics.Record{Component: "runtime", Event: "operation.failed", Level: slog.LevelError, Err: combined})
	if !strings.Contains(output.String(), "permission denied") || !strings.Contains(output.String(), "file does not exist") || strings.Contains(output.String(), "secret") || !strings.Contains(output.String(), `"operation_id":"geoip-pair"`) {
		t.Fatalf("aggregate logs=%s", output.String())
	}
}

func TestGeoIPDiagnostic_FileActivationFailureKeepsRestoreAttempt(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "country.mmdb")
	candidate := &FileCandidate{staged: filepath.Join(root, "missing.mmdb"), destination: destination}
	err := candidate.Commit()
	var restoreFound bool
	var visit func(error)
	visit = func(e error) {
		if e == nil {
			return
		}
		if link, ok := e.(*os.LinkError); ok && link.Old == destination+".previous" {
			restoreFound = true
		}
		switch wrapped := e.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range wrapped.Unwrap() {
				visit(child)
			}
		case interface{ Unwrap() error }:
			visit(wrapped.Unwrap())
		}
	}
	visit(err)
	if !restoreFound {
		t.Fatalf("failed restoration attempt cause lost: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "activate geoip database: ") || strings.Contains(err.Error(), ".previous") {
		t.Fatalf("primary error text changed: %v", err)
	}
}
