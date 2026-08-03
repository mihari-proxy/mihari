package geoip

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sync"
	"time"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"
)

const (
	// DefaultCountryURL is the checksum-published Country MMDB source used by the daemon.
	DefaultCountryURL         = "https://raw.githubusercontent.com/Loyalsoldier/geoip/release/GeoLite2-Country.mmdb"
	DefaultCountryChecksumURL = DefaultCountryURL + ".sha256sum"
	DefaultASNURL             = "https://raw.githubusercontent.com/Loyalsoldier/geoip/release/GeoLite2-ASN.mmdb"
	DefaultASNChecksumURL     = DefaultASNURL + ".sha256sum"
)

var (
	// ErrUnavailable reports that neither local MMDB reader is available.
	ErrUnavailable = errors.New("geoip database is unavailable")
	// ErrInvalidAddress reports a lookup attempt for a non-public address.
	ErrInvalidAddress = errors.New("geoip address is not a public unicast address")
	// ErrStaleCandidate reports that another pair committed after preparation.
	ErrStaleCandidate = errors.New("geoip update candidate is stale")
)

// Record is the safe local GeoIP result exposed to higher layers.
type Record struct {
	CountryCode  string
	ASN          uint32
	Organization string
}

// DatabaseStatus describes one daemon-owned MMDB file without exposing its path.
type DatabaseStatus struct {
	Available bool
	UpdatedAt time.Time
	LastError string
}

// Status describes the Country and ASN database health.
type Status struct {
	Country DatabaseStatus
	ASN     DatabaseStatus
}

type databaseReader interface {
	Lookup(netip.Addr, any) error
	Close() error
}

type maxMindReader struct{ reader *maxminddb.Reader }

func (r maxMindReader) Lookup(address netip.Addr, target any) error {
	return r.reader.Lookup(address).Decode(target)
}

func (r maxMindReader) Close() error { return r.reader.Close() }

// ServiceOptions configures local database paths, sources, and test seams.
type ServiceOptions struct {
	CountryPath        string
	ASNPath            string
	CountryURL         string
	CountryChecksumURL string
	ASNURL             string
	ASNChecksumURL     string
	Downloader         Downloader
	OpenDatabase       func(string) (databaseReader, error)
}

// Service owns the active MMDB readers and their update transaction.
type Service struct {
	mu                 sync.RWMutex
	countryPath        string
	asnPath            string
	country            databaseReader
	asn                databaseReader
	countryErr         error
	asnErr             error
	updateErr          string
	generation         uint64
	downloader         Downloader
	countryURL         string
	countryChecksumURL string
	asnURL             string
	asnChecksumURL     string
	openDatabase       func(string) (databaseReader, error)
}

type countryRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

type asnRecord struct {
	Number       uint32 `maxminddb:"autonomous_system_number"`
	Organization string `maxminddb:"autonomous_system_organization"`
}

// New opens any valid local databases and preserves missing databases as degraded state.
func New(options ServiceOptions) *Service {
	if options.CountryURL == "" {
		options.CountryURL = DefaultCountryURL
	}
	if options.CountryChecksumURL == "" {
		options.CountryChecksumURL = DefaultCountryChecksumURL
	}
	if options.ASNURL == "" {
		options.ASNURL = DefaultASNURL
	}
	if options.ASNChecksumURL == "" {
		options.ASNChecksumURL = DefaultASNChecksumURL
	}
	if options.OpenDatabase == nil {
		options.OpenDatabase = openDatabase
	}
	service := &Service{
		countryPath: options.CountryPath, asnPath: options.ASNPath, downloader: options.Downloader,
		countryURL: options.CountryURL, countryChecksumURL: options.CountryChecksumURL,
		asnURL: options.ASNURL, asnChecksumURL: options.ASNChecksumURL, openDatabase: options.OpenDatabase,
	}
	service.country, service.countryErr = service.openDatabase(options.CountryPath)
	service.asn, service.asnErr = service.openDatabase(options.ASNPath)
	return service
}

// PreparedUpdate is a validated Country/ASN candidate pair.
type PreparedUpdate struct {
	service        *Service
	country        *FileCandidate
	asn            *FileCandidate
	identity       string
	baseGeneration uint64
}

// PrepareUpdate downloads and validates both databases outside the commit critical section.
func (s *Service) PrepareUpdate(ctx context.Context) (*PreparedUpdate, error) {
	s.mu.RLock()
	baseGeneration := s.generation
	s.mu.RUnlock()
	country, err := s.downloader.Prepare(ctx, DownloadSpec{
		URL: s.countryURL, ChecksumURL: s.countryChecksumURL, Destination: s.countryPath,
	})
	if err != nil {
		return nil, err
	}
	asn, err := s.downloader.Prepare(ctx, DownloadSpec{
		URL: s.asnURL, ChecksumURL: s.asnChecksumURL, Destination: s.asnPath,
	})
	if err != nil {
		country.Cleanup()
		return nil, err
	}
	combined := sha256.Sum256(append(append([]byte(nil), country.digest[:]...), asn.digest[:]...))
	return &PreparedUpdate{
		service: s, country: country, asn: asn,
		identity: hex.EncodeToString(combined[:]), baseGeneration: baseGeneration,
	}, nil
}

// Identity returns the immutable digest identity of the candidate pair.
func (p *PreparedUpdate) Identity() string {
	if p == nil {
		return ""
	}
	return p.identity
}

// Valid reports whether both staged files still match their prepared digests.
func (p *PreparedUpdate) Valid() bool {
	return p != nil && p.country.Valid() && p.asn.Valid()
}

// Cleanup removes uncommitted candidate files.
func (p *PreparedUpdate) Cleanup() {
	if p == nil {
		return
	}
	p.country.Cleanup()
	p.asn.Cleanup()
}

// Commit atomically activates the pair or restores the previous pair on failure.
func (p *PreparedUpdate) Commit() error {
	if p == nil || p.service == nil || !p.Valid() {
		return errors.New("geoip update candidate changed before commit")
	}
	s := p.service
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != p.baseGeneration {
		return ErrStaleCandidate
	}
	s.closeReadersLocked()
	if err := p.country.Commit(); err != nil {
		s.reopenLocked()
		return err
	}
	if err := p.asn.Commit(); err != nil {
		_ = p.country.Rollback()
		s.reopenLocked()
		return err
	}
	country, countryErr := s.openDatabase(s.countryPath)
	asn, asnErr := s.openDatabase(s.asnPath)
	if countryErr != nil || asnErr != nil {
		if country != nil {
			_ = country.Close()
		}
		if asn != nil {
			_ = asn.Close()
		}
		_ = p.asn.Rollback()
		_ = p.country.Rollback()
		s.reopenLocked()
		return errors.Join(countryErr, asnErr)
	}
	s.country, s.asn = country, asn
	s.countryErr, s.asnErr = nil, nil
	s.updateErr = ""
	s.generation++
	return nil
}

func (s *Service) closeReadersLocked() {
	if s.country != nil {
		_ = s.country.Close()
		s.country = nil
	}
	if s.asn != nil {
		_ = s.asn.Close()
		s.asn = nil
	}
}

func (s *Service) reopenLocked() {
	s.country, s.countryErr = s.openDatabase(s.countryPath)
	s.asn, s.asnErr = s.openDatabase(s.asnPath)
}

func openDatabase(path string) (databaseReader, error) {
	if path == "" {
		return nil, ErrUnavailable
	}
	reader, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open geoip database: %w", err)
	}
	if err := reader.Verify(); err != nil {
		_ = reader.Close()
		return nil, fmt.Errorf("verify geoip database: %w", err)
	}
	return maxMindReader{reader: reader}, nil
}

// Close closes the active readers.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result error
	if s.country != nil {
		result = errors.Join(result, s.country.Close())
		s.country = nil
	}
	if s.asn != nil {
		result = errors.Join(result, s.asn.Close())
		s.asn = nil
	}
	return result
}

// Status returns a redacted health snapshot.
func (s *Service) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Status{
		Country: databaseStatus(s.countryPath, s.country != nil, s.countryErr, s.updateErr),
		ASN:     databaseStatus(s.asnPath, s.asn != nil, s.asnErr, s.updateErr),
	}
}

func databaseStatus(path string, available bool, lastErr error, updateErr string) DatabaseStatus {
	status := DatabaseStatus{Available: available}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		status.UpdatedAt = info.ModTime().UTC()
	}
	if lastErr != nil {
		status.LastError = "database unavailable"
	} else if updateErr != "" {
		status.LastError = updateErr
	}
	return status
}

// RecordUpdateError records only a sanitized refresh health state.
func (s *Service) RecordUpdateError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		s.updateErr = ""
		return
	}
	s.updateErr = "update failed"
}

// Lookup resolves one public address entirely from local databases.
func (s *Service) Lookup(address netip.Addr) (Record, error) {
	if !isPublicAddress(address) {
		return Record{}, ErrInvalidAddress
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.country == nil && s.asn == nil {
		return Record{}, ErrUnavailable
	}
	var result Record
	if s.country != nil {
		var country countryRecord
		if err := s.country.Lookup(address, &country); err != nil {
			return Record{}, fmt.Errorf("lookup geoip country: %w", err)
		}
		result.CountryCode = country.Country.ISOCode
	}
	if s.asn != nil {
		var asn asnRecord
		if err := s.asn.Lookup(address, &asn); err != nil {
			return Record{}, fmt.Errorf("lookup geoip asn: %w", err)
		}
		result.ASN, result.Organization = asn.Number, asn.Organization
	}
	return result, nil
}

func isPublicAddress(address netip.Addr) bool {
	return address.IsValid() && address.IsGlobalUnicast() && !address.IsPrivate() && !address.IsLoopback() && !address.IsMulticast() && !address.IsUnspecified()
}

// NeedsUpdate reports whether either database is absent or older than maxAge.
func (s *Service) NeedsUpdate(now time.Time, maxAge time.Duration) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.country == nil || s.asn == nil {
		return true
	}
	for _, path := range []string{s.countryPath, s.asnPath} {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || maxAge <= 0 || now.Sub(info.ModTime()) >= maxAge {
			return true
		}
	}
	return false
}
