package subscription

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

type Fetcher interface {
	Fetch(context.Context, FetchRequest) (FetchResult, error)
}

type ServiceOptions struct {
	CatalogPath string
	CacheDir    string
	Downloader  Fetcher
	Reporter    diagnostics.Reporter
	// ProxyAddr is the mihomo mixed-port address (host:port). When non-empty,
	// subscriptions with proxy/auto modes fetch through it.
	ProxyAddr string
	Now       func() time.Time
}

type Service struct {
	mu               sync.RWMutex
	catalogPath      string
	cacheDir         string
	downloader       Fetcher
	writeCache       func(string, []byte, os.FileMode) error
	restoreCacheFile func(string, []byte, os.FileMode) (config.CommitResult, error)
	saveCatalog      func(string, Catalog) error
	now              func() time.Time
	catalog          Catalog
}

type PreparedRefresh struct {
	profileID      string
	profileVersion uint64
	profile        Profile
	result         FetchResult
	document       Document
}

func (p PreparedRefresh) ProfileID() string        { return p.profileID }
func (p PreparedRefresh) Document() Document       { return p.document }
func (p PreparedRefresh) AdvancesGeneration() bool { return !p.result.NotModified }

type Receipt struct {
	Before      Catalog
	After       Catalog
	cachePath   string
	cacheBefore []byte
	hadCache    bool
	wroteCache  bool
	profileID   string
}

func Open(options ServiceOptions) (*Service, error) {
	if err := os.MkdirAll(options.CacheDir, 0o700); err != nil {
		return nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "create subscription cache directory"}, err)
	}
	catalog, err := LoadOrCreate(options.CatalogPath)
	if err != nil {
		return nil, err
	}
	downloader := options.Downloader
	if downloader == nil {
		var proxyURL *url.URL
		if options.ProxyAddr != "" {
			// MixedAddr is host:port; the HTTP proxy scheme is implicit.
			if parsed, err := url.Parse("http://" + options.ProxyAddr); err == nil {
				proxyURL = parsed
			}
		}
		downloader = NewDownloader(DownloaderOptions{ProxyURL: proxyURL, Reporter: options.Reporter})
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{catalogPath: options.CatalogPath, cacheDir: options.CacheDir, downloader: downloader, writeCache: config.AtomicWrite, restoreCacheFile: config.AtomicWriteWithCommit, saveCatalog: Save, now: now, catalog: catalog}, nil
}

func (s *Service) Snapshot() Catalog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.catalog.Clone()
}

// ResetRefreshSchedule starts a new interval on a candidate profile. Callers
// publish that candidate through Mutate, keeping it within the same transaction.
func (s *Service) ResetRefreshSchedule(profile *Profile) {
	profile.ScheduleFrom = s.now().UTC()
}

func (s *Service) Add(name, rawURL, proxyMode string) (Profile, error) {
	id, err := newProfileID()
	if err != nil {
		return Profile{}, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInternal, Message: "generate subscription ID"}, err)
	}
	profile := Profile{ID: id, Name: name, URL: rawURL, Enabled: true, AutoRefresh: true, Version: 1, ProxyMode: proxyMode}
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
	after.fillDefaults()
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
	result, err := s.fetch(ctx, profile)
	if err != nil {
		return PreparedRefresh{}, s.refreshFailure(id, err, profile.Version)
	}
	content := result.Content
	if result.NotModified {
		if profile.Generation > 0 && profile.CacheURL != profile.URL {
			return PreparedRefresh{}, s.refreshFailure(id, dataError("subscription provider returned not-modified for a different cache source"), profile.Version)
		}
		content, err = os.ReadFile(s.CachePath(id))
		if err != nil {
			fail := diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "subscription provider returned not-modified without a valid cache"}, err)
			return PreparedRefresh{}, s.refreshFailure(id, fail, profile.Version)
		}
	}
	document, err := ParseDocument(content)
	if err != nil {
		return PreparedRefresh{}, s.refreshFailure(id, err, profile.Version)
	}
	baseDir := result.BaseDir
	if result.NotModified {
		baseDir = profile.CacheBaseDir
	}
	ResolveFileReferences(document, baseDir)
	return PreparedRefresh{profileID: id, profileVersion: profile.Version, profile: profile, result: result, document: document}, nil
}

// refreshFailure preserves both the actual failure and a failed status write.
// The original public classification and message still describe the operation.
func (s *Service) refreshFailure(id string, cause error, versions ...uint64) error {
	if errors.Is(cause, context.Canceled) {
		return cause
	}
	if noteErr := s.noteRefreshError(id, cause, versions...); noteErr != nil && !errors.Is(noteErr, errStaleRefreshFailure) {
		joined := errors.Join(cause, fmt.Errorf("record subscription refresh failure: %w", noteErr))
		var api protocol.APIError
		if errors.As(cause, &api) {
			return diagnostics.Wrap(api, joined)
		}
		// Match the public fallback for the original unclassified failure.
		// A secondary status error must not replace its code or expose IO text.
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInternal, Message: "internal error"}, joined)
	}
	return cause
}

var errStaleRefreshFailure = errors.New("subscription refresh failure belongs to an older profile")

// RecordRefreshFailure keeps a prepared refresh's failure on its original
// profile version only. Revision conflicts and cancellation do not invalidate a cache.
func (s *Service) RecordRefreshFailure(prepared PreparedRefresh, cause error) error {
	var api protocol.APIError
	if errors.As(cause, &api) && (api.Code == protocol.CodeRevisionConflict || api.Details["degraded"] == true) {
		return cause
	}
	return s.refreshFailure(prepared.profileID, cause, prepared.profileVersion)
}

// noteRefreshError records a safe last-error on the profile without replacing
// a valid cache. Its caller keeps failures alongside the original cause.
func (s *Service) noteRefreshError(id string, cause error, versions ...uint64) error {
	message := "subscription refresh failed"
	var apiError protocol.APIError
	if errors.As(cause, &apiError) && apiError.Message != "" {
		message = apiError.Message
	}
	_, _, err := s.Mutate(func(catalog *Catalog) error {
		index := catalog.Index(id)
		if len(versions) > 0 && (index < 0 || catalog.Profiles[index].Version != versions[0]) {
			return errStaleRefreshFailure
		}
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
	return s.commitRefreshLocked(prepared, s.catalog.Clone(), false)
}

// CommitRefreshAndActivate refreshes and selects an existing profile in one receipt.
func (s *Service) CommitRefreshAndActivate(prepared PreparedRefresh) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.catalog.Clone()
	s.catalog.ActiveID = prepared.profileID
	if index := s.catalog.Index(prepared.profileID); index >= 0 {
		s.catalog.Profiles[index].Enabled = true
	}
	receipt, err := s.commitRefreshLocked(prepared, before, true)
	return s.settleSetupCommitLocked(receipt, err)
}

func (s *Service) commitRefreshLocked(prepared PreparedRefresh, before Catalog, setup bool) (receipt Receipt, err error) {
	keepCommitted := false
	defer func() {
		if err != nil && !keepCommitted {
			s.catalog = before.Clone()
		}
	}()
	index := s.catalog.Index(prepared.profileID)
	if index < 0 || s.catalog.Profiles[index].Version != prepared.profileVersion {
		return Receipt{}, ErrRefreshChanged
	}
	after := s.catalog.Clone()
	profile := &after.Profiles[index]
	cachePath := s.CachePath(profile.ID)
	cacheBefore, readErr := os.ReadFile(cachePath)
	hadCache := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return Receipt{}, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "read existing subscription cache"}, readErr)
	}
	wroteCache := !prepared.result.NotModified
	if wroteCache {
		if err := s.writeCache(cachePath, prepared.result.Content, 0o600); err != nil {
			err = s.failAfterRestore(err, cachePath, cacheBefore, hadCache, true, setup)
			var api protocol.APIError
			if errors.As(err, &api) && api.Details["degraded"] == true {
				return Receipt{}, err
			}
			return Receipt{}, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "write subscription cache"}, err)
		}
		profile.Generation++
		profile.CacheBaseDir = prepared.result.BaseDir
	}
	profile.Version++
	profile.UpdatedAt = s.now().UTC()
	profile.LastError = ""
	profile.CacheURL = profile.URL
	profile.ScheduleFrom = time.Time{}
	profile.IntervalRefreshRequired = false
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
		return Receipt{}, s.failAfterRestore(err, cachePath, cacheBefore, hadCache, wroteCache, setup)
	}
	after.fillDefaults()
	if err := s.saveCatalog(s.catalogPath, after); err != nil {
		// Replacement may have committed before a directory-sync error. Restore
		// catalog metadata as well as bytes so cached relative paths stay paired.
		if restoreErr := s.saveCatalog(s.catalogPath, before); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("restore subscription catalog: %w", restoreErr))
			durable, readErr := Load(s.catalogPath)
			if readErr != nil {
				return Receipt{}, subscriptionRollbackError(errors.Join(err, fmt.Errorf("inspect subscription catalog after failed restore: %w", readErr)), setup)
			}
			if reflect.DeepEqual(durable, after) {
				// The new catalog remains committed: retain its matching bytes
				// and publish the same generation to in-process readers.
				s.catalog = durable
				keepCommitted = true
				return Receipt{Before: before, After: after.Clone(), cachePath: cachePath, cacheBefore: cacheBefore, hadCache: hadCache, wroteCache: wroteCache, profileID: profile.ID}, err
			}
			if !reflect.DeepEqual(durable, before) {
				return Receipt{}, subscriptionRollbackError(errors.Join(err, errors.New("subscription catalog changed during failed restore")), setup)
			}

		}
		return Receipt{}, s.failAfterRestore(err, cachePath, cacheBefore, hadCache, wroteCache, setup)
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
	// Commit owns this same lock while replacing bytes and directory metadata;
	// readers must observe both from one committed cache generation.
	s.mu.RLock()
	defer s.mu.RUnlock()
	content, err := os.ReadFile(s.CachePath(id))
	if err != nil {
		return nil, nil, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "subscription cache is unavailable"}, err)
	}
	document, err := ParseDocument(content)
	if err == nil {
		index := s.catalog.Index(id)
		if index >= 0 {
			ResolveFileReferences(document, s.catalog.Profiles[index].CacheBaseDir)
		}
	}
	return content, document, err
}

// failAfterRestore returns original, joined with the cache rollback error when
// the rollback itself fails, so a failed refresh never hides a failed restore.
func (s *Service) failAfterRestore(original error, path string, content []byte, existed, changed, setup bool) error {
	if err := s.restoreCache(path, content, existed, changed); err != nil {
		return subscriptionRollbackError(errors.Join(original, fmt.Errorf("restore subscription cache: %w", err)), setup)
	}
	return original
}

func (s *Service) restoreCache(path string, content []byte, existed, changed bool) error {
	_, err := s.restoreCacheWithCommit(path, content, existed, changed)
	return err
}

// restoreCacheWithCommit distinguishes a failed replacement from a sync warning
// after replacement, even when the old and new YAML bytes happen to be equal.
func (s *Service) restoreCacheWithCommit(path string, content []byte, existed, changed bool) (bool, error) {
	if !changed {
		return true, nil
	}
	if existed {
		result, err := s.restoreCacheFile(path, content, 0600)
		return result.Committed, errors.Join(err, result.Warning)
	}
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "remove rolled-back subscription cache"}, err)
	}
	return true, nil
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

// ErrRefreshChanged identifies a prepared refresh invalidated before publication.
var ErrRefreshChanged = staleRefreshError{}

type staleRefreshError struct{}

func (staleRefreshError) Error() string { return "subscription changed while refresh was in progress" }
func (e staleRefreshError) Unwrap() error {
	return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: e.Error()}
}

// ReprepareSetupRefresh preserves the selected source while rebasing harmless
// metadata changes. A changed cache used by a 304 response must be fetched again.
func (s *Service) ReprepareSetupRefresh(ctx context.Context, prepared PreparedRefresh) (PreparedRefresh, error) {
	catalog := s.Snapshot()
	index := catalog.Index(prepared.profileID)
	if index < 0 {
		return PreparedRefresh{}, notFoundError()
	}
	current := catalog.Profiles[index]
	if current.URL != prepared.profile.URL || current.ProxyMode != prepared.profile.ProxyMode {
		return PreparedRefresh{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "selected subscription source changed; review the subscription before retrying setup"}
	}
	if prepared.result.NotModified && (current.Generation != prepared.profile.Generation || current.CacheBaseDir != prepared.profile.CacheBaseDir) {
		next, err := s.PrepareRefresh(ctx, prepared.profileID)
		if err != nil {
			return PreparedRefresh{}, err
		}
		if next.profile.URL != prepared.profile.URL || next.profile.ProxyMode != prepared.profile.ProxyMode {
			return PreparedRefresh{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "selected subscription source changed during setup refresh"}
		}
		return next, nil
	}
	prepared.profileVersion = current.Version
	prepared.profile = current
	return prepared, nil
}

func subscriptionRollbackError(err error, setup bool) error {
	if !setup {
		return err
	}
	return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "subscription state rollback failed", Details: map[string]any{"degraded": true}}, err)
}

// settleSetupCommitLocked compensates a retained catalog before the runtime has
// applied its configuration. Restore cache bytes first so a failed replacement
// leaves the committed catalog and its matching cache untouched.
func (s *Service) settleSetupCommitLocked(receipt Receipt, err error) (Receipt, error) {
	if err == nil || receipt.profileID == "" {
		return receipt, err
	}
	var cacheAfter []byte
	if receipt.wroteCache {
		var readErr error
		cacheAfter, readErr = os.ReadFile(receipt.cachePath)
		if readErr != nil {
			return Receipt{}, subscriptionRollbackError(errors.Join(err, fmt.Errorf("read committed subscription cache before compensation: %w", readErr)), true)
		}
	}
	restored, cacheErr := s.restoreCacheWithCommit(receipt.cachePath, receipt.cacheBefore, receipt.hadCache, receipt.wroteCache)
	if !restored {
		return Receipt{}, subscriptionRollbackError(errors.Join(err, cacheErr), true)
	}
	restoreErr := s.saveCatalog(s.catalogPath, receipt.Before)
	if restoreErr != nil {
		durable, readErr := Load(s.catalogPath)
		if readErr != nil {
			return Receipt{}, subscriptionRollbackError(errors.Join(err, cacheErr, restoreErr, readErr), true)
		}
		s.catalog = durable
		if reflect.DeepEqual(durable, receipt.After) {
			// Metadata stayed committed. Put back the matching new cache rather than
			// pairing that generation and reference base with rolled-back bytes.
			if receipt.wroteCache {
				if forwardErr := s.writeCache(receipt.cachePath, cacheAfter, 0600); forwardErr != nil {
					return Receipt{}, subscriptionRollbackError(errors.Join(err, cacheErr, restoreErr, fmt.Errorf("restore committed subscription cache after failed catalog rollback: %w", forwardErr)), true)
				}
			}
			return Receipt{}, subscriptionRollbackError(errors.Join(err, cacheErr, restoreErr), true)
		}
		if !reflect.DeepEqual(durable, receipt.Before) {
			return Receipt{}, subscriptionRollbackError(errors.Join(err, cacheErr, restoreErr, errors.New("subscription catalog changed during setup compensation")), true)
		}
	}
	s.catalog = receipt.Before.Clone()
	if restoreErr != nil || cacheErr != nil {
		return Receipt{}, subscriptionRollbackError(errors.Join(err, restoreErr, cacheErr), true)
	}
	return Receipt{}, err
}
