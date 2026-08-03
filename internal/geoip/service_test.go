package geoip

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestService_MissingDatabaseReturnsUnavailableWithoutBreakingLookupAPI(t *testing.T) {
	service := New(ServiceOptions{
		CountryPath: filepath.Join(t.TempDir(), "missing-country.mmdb"),
		ASNPath:     filepath.Join(t.TempDir(), "missing-asn.mmdb"),
	})
	defer service.Close()
	_, err := service.Lookup(netip.MustParseAddr("203.0.113.1"))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
	status := service.Status()
	if status.Country.Available || status.ASN.Available {
		t.Fatalf("status=%#v", status)
	}
}

func TestService_RejectsNonPublicAddressesBeforeReaderLookup(t *testing.T) {
	reader := &fakeReader{}
	service := newServiceWithReaders(reader, reader)
	defer service.Close()
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "::1", "ff02::1", "0.0.0.0"} {
		_, err := service.Lookup(netip.MustParseAddr(raw))
		if !errors.Is(err, ErrInvalidAddress) {
			t.Fatalf("address=%s err=%v", raw, err)
		}
	}
	if reader.lookups != 0 {
		t.Fatalf("reader lookups=%d", reader.lookups)
	}
}

func TestService_LookupCombinesCountryAndASN(t *testing.T) {
	country := &fakeReader{decode: func(target any) { target.(*countryRecord).Country.ISOCode = "JP" }}
	asn := &fakeReader{decode: func(target any) {
		record := target.(*asnRecord)
		record.Number = 13335
		record.Organization = "Cloudflare, Inc."
	}}
	service := newServiceWithReaders(country, asn)
	defer service.Close()
	got, err := service.Lookup(netip.MustParseAddr("1.1.1.1"))
	if err != nil {
		t.Fatal(err)
	}
	if got.CountryCode != "JP" || got.ASN != 13335 || got.Organization != "Cloudflare, Inc." {
		t.Fatalf("record=%#v", got)
	}
}

func TestService_NeedsUpdateWhenEitherDatabaseIsMissingOrOlderThanThirtyDays(t *testing.T) {
	root := t.TempDir()
	country := filepath.Join(root, "country.mmdb")
	asn := filepath.Join(root, "asn.mmdb")
	now := time.Date(2026, time.August, 3, 0, 0, 0, 0, time.UTC)
	service := New(ServiceOptions{CountryPath: country, ASNPath: asn})
	defer service.Close()
	if !service.NeedsUpdate(now, 30*24*time.Hour) {
		t.Fatal("missing databases did not require update")
	}
	for _, path := range []string{country, asn} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
		fresh := now.Add(-29 * 24 * time.Hour)
		if err := os.Chtimes(path, fresh, fresh); err != nil {
			t.Fatal(err)
		}
	}
	freshService := &Service{countryPath: country, asnPath: asn, country: &fakeReader{}, asn: &fakeReader{}}
	defer freshService.Close()
	if freshService.NeedsUpdate(now, 30*24*time.Hour) {
		t.Fatal("fresh database pair required update")
	}
	stale := now.Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(asn, stale, stale); err != nil {
		t.Fatal(err)
	}
	if !freshService.NeedsUpdate(now, 30*24*time.Hour) {
		t.Fatal("stale ASN database did not require pair update")
	}
}

func TestService_NeedsUpdateWhenFreshFilesCouldNotBeOpened(t *testing.T) {
	root := t.TempDir()
	country, asn := filepath.Join(root, "country.mmdb"), filepath.Join(root, "asn.mmdb")
	for _, path := range []string{country, asn} {
		if err := os.WriteFile(path, []byte("invalid"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := New(ServiceOptions{CountryPath: country, ASNPath: asn})
	defer service.Close()
	if !service.NeedsUpdate(time.Now().UTC(), 30*24*time.Hour) {
		t.Fatal("unopenable database files did not require update")
	}
}

func TestService_FailedRefreshKeepsAvailableReadersAndExposesSanitizedHealth(t *testing.T) {
	service := newServiceWithReaders(&fakeReader{}, &fakeReader{})
	defer service.Close()
	service.RecordUpdateError(errors.New("https://user:secret@example.test/database?token=secret"))
	status := service.Status()
	if !status.Country.Available || !status.ASN.Available || status.Country.LastError != "update failed" || status.ASN.LastError != "update failed" {
		t.Fatalf("status=%#v", status)
	}
}

func TestPreparedUpdate_CommitsCountryAndASNAsOneRecoverablePair(t *testing.T) {
	root := t.TempDir()
	countryPath := filepath.Join(root, "geoip", "country.mmdb")
	asnPath := filepath.Join(root, "geoip", "asn.mmdb")
	if err := os.MkdirAll(filepath.Dir(countryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(countryPath, []byte("old-country"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(asnPath, []byte("old-asn"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := geoIPPairServer(t, []byte("new-country"), []byte("new-asn"))
	defer server.Close()
	service := New(ServiceOptions{
		CountryPath: countryPath, ASNPath: asnPath,
		CountryURL: server.URL + "/country", CountryChecksumURL: server.URL + "/country.sha256sum",
		ASNURL: server.URL + "/asn", ASNChecksumURL: server.URL + "/asn.sha256sum",
		Downloader:   Downloader{Client: server.Client(), StagingDir: filepath.Join(root, "staging"), AllowHTTP: true, Validate: func(string) error { return nil }},
		OpenDatabase: func(string) (databaseReader, error) { return &fakeReader{}, nil },
	})
	defer service.Close()
	prepared, err := service.PrepareUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()
	if prepared.Identity() == "" || !prepared.Valid() {
		t.Fatalf("identity=%q valid=%v", prepared.Identity(), prepared.Valid())
	}
	if err := prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, countryPath, "new-country")
	assertFileContent(t, asnPath, "new-asn")
	assertFileContent(t, countryPath+".previous", "old-country")
	assertFileContent(t, asnPath+".previous", "old-asn")
}

func TestPreparedUpdate_ASNCommitFailureRestoresPreviousCountry(t *testing.T) {
	root := t.TempDir()
	countryPath := filepath.Join(root, "geoip", "country.mmdb")
	blockedParent := filepath.Join(root, "blocked")
	asnPath := filepath.Join(blockedParent, "asn.mmdb")
	if err := os.MkdirAll(filepath.Dir(countryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(countryPath, []byte("old-country"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blockedParent, []byte("not-a-directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := geoIPPairServer(t, []byte("new-country"), []byte("new-asn"))
	defer server.Close()
	service := New(ServiceOptions{
		CountryPath: countryPath, ASNPath: asnPath,
		CountryURL: server.URL + "/country", CountryChecksumURL: server.URL + "/country.sha256sum",
		ASNURL: server.URL + "/asn", ASNChecksumURL: server.URL + "/asn.sha256sum",
		Downloader:   Downloader{Client: server.Client(), StagingDir: filepath.Join(root, "staging"), AllowHTTP: true, Validate: func(string) error { return nil }},
		OpenDatabase: func(string) (databaseReader, error) { return &fakeReader{}, nil },
	})
	defer service.Close()
	prepared, err := service.PrepareUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()
	if err := prepared.Commit(); err == nil {
		t.Fatal("pair commit succeeded")
	}
	assertFileContent(t, countryPath, "old-country")
}

func TestPreparedUpdate_RejectsCandidatePreparedAgainstOlderActivePair(t *testing.T) {
	root := t.TempDir()
	server := geoIPPairServer(t, []byte("country"), []byte("asn"))
	defer server.Close()
	service := New(ServiceOptions{
		CountryPath: filepath.Join(root, "country.mmdb"), ASNPath: filepath.Join(root, "asn.mmdb"),
		CountryURL: server.URL + "/country", CountryChecksumURL: server.URL + "/country.sha256sum",
		ASNURL: server.URL + "/asn", ASNChecksumURL: server.URL + "/asn.sha256sum",
		Downloader:   Downloader{Client: server.Client(), StagingDir: filepath.Join(root, "staging"), AllowHTTP: true, Validate: func(string) error { return nil }},
		OpenDatabase: func(string) (databaseReader, error) { return &fakeReader{}, nil },
	})
	defer service.Close()
	first, err := service.PrepareUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Cleanup()
	second, err := service.PrepareUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Cleanup()
	if err := second.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(); !errors.Is(err, ErrStaleCandidate) {
		t.Fatalf("err=%v", err)
	}
}

func geoIPPairServer(t *testing.T, country, asn []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload := country
		if request.URL.Path == "/asn" || request.URL.Path == "/asn.sha256sum" {
			payload = asn
		}
		if filepath.Ext(request.URL.Path) == ".sha256sum" {
			sum := sha256.Sum256(payload)
			_, _ = io.WriteString(writer, hex.EncodeToString(sum[:]))
			return
		}
		_, _ = writer.Write(payload)
	}))
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("path=%s content=%q err=%v", path, got, err)
	}
}

type fakeReader struct {
	lookups int
	decode  func(any)
}

func (r *fakeReader) Lookup(_ netip.Addr, target any) error {
	r.lookups++
	if r.decode != nil {
		r.decode(target)
	}
	return nil
}

func (*fakeReader) Close() error { return nil }

func newServiceWithReaders(country, asn databaseReader) *Service {
	return &Service{country: country, asn: asn}
}
