package subscription

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type Fetcher interface {
	Fetch(context.Context, FetchRequest) (FetchResult, error)
}

type ServiceOptions struct {
	CatalogPath string
	CacheDir    string
	Downloader  Fetcher
	Now         func() time.Time
}

type Service struct {
	mu          sync.RWMutex
	catalogPath string
	cacheDir    string
	downloader  Fetcher
	now         func() time.Time
	catalog     Catalog
}

type PreparedRefresh struct {
	profileID      string
	profileVersion uint64
	result         FetchResult
	document       Document
}

func (p PreparedRefresh) ProfileID() string  { return p.profileID }
func (p PreparedRefresh) Document() Document { return p.document }

type Receipt struct {
	Before       Catalog
	After        Catalog
	cachePath    string
	cacheBefore  []byte
	hadCache     bool
	wroteCache   bool
	profileID    string
	generatedNow bool
}

func Open(options ServiceOptions) (*Service, error) {
	if err := os.MkdirAll(options.CacheDir, 0o700); err != nil {
		return nil, dataError("create subscription cache directory")
	}
	catalog, err := LoadOrCreate(options.CatalogPath)
	if err != nil {
		return nil, err
	}
	downloader := options.Downloader
	if downloader == nil {
		downloader = NewDownloader(nil)
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{catalogPath: options.CatalogPath, cacheDir: options.CacheDir, downloader: downloader, now: now, catalog: catalog}, nil
}

func (s *Service) Snapshot() Catalog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.catalog.Clone()
}

func (s *Service) Add(name, rawURL string) (Profile, error) {
	id, err := newProfileID()
	if err != nil {
		return Profile{}, protocol.APIError{Code: protocol.CodeInternal, Message: "generate subscription ID"}
	}
	profile := Profile{ID: id, Name: name, URL: rawURL, Enabled: true, AutoRefresh: true, Version: 1}
	_, after, err := s.Mutate(func(catalog *Catalog) error {
		catalog.Profiles = append(catalog.Profiles, profile)
		return nil
	})
	if err != nil {
		return Profile{}, err
	}
	return after.Profiles[after.Index(id)], nil
}

func (s *Service) Mutate(mutate func(*Catalog) error) (Catalog, Catalog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.catalog.Clone()
	after := before.Clone()
	if err := mutate(&after); err != nil {
		return Catalog{}, Catalog{}, err
	}
	if err := after.Normalize(); err != nil {
		return Catalog{}, Catalog{}, err
	}
	if err := Save(s.catalogPath, after); err != nil {
		return Catalog{}, Catalog{}, err
	}
	s.catalog = after
	return before, after.Clone(), nil
}

func (s *Service) PrepareRefresh(ctx context.Context, id string) (PreparedRefresh, error) {
	s.mu.RLock()
	index := s.catalog.Index(id)
	if index < 0 {
		s.mu.RUnlock()
		return PreparedRefresh{}, notFoundError()
	}
	profile := s.catalog.Profiles[index]
	s.mu.RUnlock()
	result, err := s.downloader.Fetch(ctx, FetchRequest{URL: profile.URL, ETag: profile.ETag, LastModified: profile.LastModified})
	if err != nil {
		_ = s.noteRefreshError(id, err)
		return PreparedRefresh{}, err
	}
	content := result.Content
	if result.NotModified {
		content, err = os.ReadFile(s.CachePath(id))
		if err != nil {
			fail := dataError("subscription provider returned not-modified without a valid cache")
			_ = s.noteRefreshError(id, fail)
			return PreparedRefresh{}, fail
		}
	}
	document, err := ParseDocument(content)
	if err != nil {
		_ = s.noteRefreshError(id, err)
		return PreparedRefresh{}, err
	}
	return PreparedRefresh{profileID: id, profileVersion: profile.Version, result: result, document: document}, nil
}

// noteRefreshError records a safe last-error on the profile without replacing
// a valid cache. Failures here are best-effort and do not mask the original error.
func (s *Service) noteRefreshError(id string, cause error) error {
	message := "subscription refresh failed"
	var apiError protocol.APIError
	if errors.As(cause, &apiError) && apiError.Message != "" {
		message = apiError.Message
	}
	_, _, err := s.Mutate(func(catalog *Catalog) error {
		index := catalog.Index(id)
		if index < 0 {
			return notFoundError()
		}
		catalog.Profiles[index].LastError = message
		return nil
	})
	return err
}

func (s *Service) CommitRefresh(prepared PreparedRefresh) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := s.catalog.Index(prepared.profileID)
	if index < 0 || s.catalog.Profiles[index].Version != prepared.profileVersion {
		return Receipt{}, protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "subscription changed while refresh was in progress"}
	}
	before := s.catalog.Clone()
	after := before.Clone()
	profile := &after.Profiles[index]
	cachePath := s.CachePath(profile.ID)
	cacheBefore, readErr := os.ReadFile(cachePath)
	hadCache := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return Receipt{}, dataError("read existing subscription cache")
	}
	wroteCache := !prepared.result.NotModified
	if wroteCache {
		if err := config.AtomicWrite(cachePath, prepared.result.Content, 0o600); err != nil {
			return Receipt{}, dataError("write subscription cache")
		}
		profile.Generation++
	}
	profile.Version++
	profile.UpdatedAt = s.now().UTC()
	profile.LastError = ""
	if prepared.result.ETag != "" {
		profile.ETag = prepared.result.ETag
	}
	if prepared.result.LastModified != "" {
		profile.LastModified = prepared.result.LastModified
	}
	if info, ok := ParseUserInfo(prepared.result.Userinfo); ok {
		profile.Upload = info.Upload
		profile.Download = info.Download
		profile.Total = info.Total
		profile.Expire = info.Expire
	}
	if err := after.Normalize(); err != nil {
		s.restoreCache(cachePath, cacheBefore, hadCache, wroteCache)
		return Receipt{}, err
	}
	if err := Save(s.catalogPath, after); err != nil {
		s.restoreCache(cachePath, cacheBefore, hadCache, wroteCache)
		return Receipt{}, err
	}
	s.catalog = after
	return Receipt{Before: before, After: after.Clone(), cachePath: cachePath, cacheBefore: cacheBefore, hadCache: hadCache, wroteCache: wroteCache, profileID: profile.ID}, nil
}

func (s *Service) Rollback(receipt Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.restoreCache(receipt.cachePath, receipt.cacheBefore, receipt.hadCache, receipt.wroteCache); err != nil {
		return err
	}
	if err := Save(s.catalogPath, receipt.Before); err != nil {
		return err
	}
	s.catalog = receipt.Before.Clone()
	return nil
}

func (s *Service) Restore(catalog Catalog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := Save(s.catalogPath, catalog); err != nil {
		return err
	}
	s.catalog = catalog.Clone()
	return nil
}

func (s *Service) CachePath(id string) string {
	return filepath.Join(s.cacheDir, id+".yaml")
}

func (s *Service) ReadCache(id string) ([]byte, Document, error) {
	if !profileIDPattern.MatchString(id) {
		return nil, nil, notFoundError()
	}
	content, err := os.ReadFile(s.CachePath(id))
	if err != nil {
		return nil, nil, dataError("subscription cache is unavailable")
	}
	document, err := ParseDocument(content)
	return content, document, err
}

func (s *Service) restoreCache(path string, content []byte, existed, changed bool) error {
	if !changed {
		return nil
	}
	if existed {
		return config.AtomicWrite(path, content, 0o600)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return dataError("remove rolled-back subscription cache")
	}
	return nil
}

func newProfileID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func notFoundError() error {
	return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "subscription not found"}
}
