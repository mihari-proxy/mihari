package geoip

import (
	"bytes"
	"context"
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
	opens := 0
	service := New(ServiceOptions{CountryPath: countryPath, ASNPath: asnPath, CountryURL: server.URL + "/country", CountryChecksumURL: server.URL + "/country.sha256sum", ASNURL: server.URL + "/asn", ASNChecksumURL: server.URL + "/asn.sha256sum", Downloader: Downloader{Client: server.Client(), StagingDir: filepath.Join(root, "staging"), AllowHTTP: true, Validate: func(string) error { return nil }}, OpenDatabase: func(path string) (databaseReader, error) {
		opens++
		if opens == 3 || opens == 4 {
			if err := os.Remove(path + ".previous"); err != nil {
				t.Fatal(err)
			}
			if opens == 3 {
				return nil, countryCause
			}
			return nil, asnCause
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
	if !errors.Is(err, countryCause) || !errors.Is(err, asnCause) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pair restoration cause lost: %v", err)
	}
	if err.Error() != errors.Join(countryCause, asnCause).Error() {
		t.Fatalf("primary public text changed: %v", err)
	}
	if opens != 6 || service.generation != 0 {
		t.Fatalf("reopen/generation changed opens=%d generation=%d", opens, service.generation)
	}
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
