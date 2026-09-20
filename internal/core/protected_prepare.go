package core

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type protectedCandidate struct {
	store          ProvenanceStore
	transaction    string
	binary, marker ProvenanceObject
	closed         atomic.Bool
}

// PreparedUpdate binds a validated candidate to a runtime-owned transaction.
type PreparedUpdate interface {
	BeginUpdate(context.Context, UpdateIntent) (*UpdateTransaction, error)
}

// UpdateCandidate exposes recoverable publication when this candidate has a
// protected store. Legacy pair candidates are retained only for migration.
func (c *Candidate) UpdateCandidate() PreparedUpdate {
	if c.protected == nil {
		return nil
	}
	return c
}

func (i Installer) prepareProtected(ctx context.Context, target ReleaseTarget) (prepared PreparedCore, resultErr error) {
	if i.GeneratedConfig == nil {
		return nil, dataFailure("generated configuration capability unavailable")
	}
	goos, arch := i.Provenance.coreStore().target()
	if goos != target.goos || arch != target.goarch {
		return nil, dataFailure("core candidate platform mismatch")
	}
	archive, err := i.readTargetArchive(ctx, target)
	if err != nil {
		return nil, err
	}
	binary, err := archiveBinary(archive, target.asset.Name)
	if err != nil {
		return nil, err
	}
	candidate, err := i.stageUpdateCandidate(ctx, i.Provenance, target, binary)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			candidate.Cleanup()
		}
	}()
	verified, err := candidate.Verified(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, verified.Close()) }()
	version, err := DetectVerifiedVersion(ctx, verified, i.Executor)
	if err != nil {
		return nil, err
	}
	if version != candidate.version {
		return nil, dataFailureCause("mihomo candidate version does not match selected release", fmt.Errorf("selected %s, candidate reported %s", candidate.version, version))
	}
	configuration, err := i.GeneratedConfig(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, configuration.Close()) }()
	if err := ValidateVerifiedConfig(ctx, verified, configuration, i.Executor); err != nil {
		return nil, err
	}
	return candidate, nil
}

func (i Installer) readTargetArchive(ctx context.Context, target ReleaseTarget) (archive []byte, resultErr error) {
	i.HTTPClient = i.targetHTTPClient()
	if err := i.recheckTarget(ctx, target); err != nil {
		return nil, err
	}
	address := i.targetAssetURL(target)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "mihari")
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	response, err := i.httpClient().Do(request)
	if err != nil {
		return nil, coreHTTPError(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "download mihomo core failed"}, "core GET asset", address, "transport", nil, err)
	}
	defer closeCoreResponse(ctx, response, "core GET asset", address, &resultErr, i.Reporter)
	if response.StatusCode != http.StatusOK {
		return nil, coreHTTPError(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "download mihomo core failed"}, "core GET asset", address, "response", response, nil)
	}
	archive, err = io.ReadAll(io.LimitReader(response.Body, maxCoreArchiveSize+1))
	if err != nil {
		return nil, coreHTTPError(protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "read mihomo core download failed"}, "core GET asset", address, "read", response, err)
	}
	if len(archive) > maxCoreArchiveSize || int64(len(archive)) != target.asset.Size {
		return nil, dataFailure("mihomo asset size mismatch")
	}
	if target.asset.Digest != "" && !strings.EqualFold(target.asset.Digest, "sha256:"+digest(archive)) {
		return nil, dataFailure("mihomo asset digest mismatch")
	}
	if err := i.recheckTarget(ctx, target); err != nil {
		return nil, err
	}
	return archive, nil
}

func archiveBinary(archive []byte, name string) (binary []byte, resultErr error) {
	var reader io.ReadCloser
	if strings.HasSuffix(name, ".gz") {
		var err error
		reader, err = gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return nil, dataFailureCause("invalid mihomo gzip archive", err)
		}
	} else if strings.HasSuffix(name, ".zip") {
		zipReader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, dataFailureCause("invalid mihomo zip archive", err)
		}
		var selected *zip.File
		for _, file := range zipReader.File {
			if !safeArchiveName(file.Name) || (file.Mode()&os.ModeType != 0 && !file.FileInfo().IsDir()) {
				return nil, dataFailure("unsafe file in mihomo archive")
			}
			base := strings.ToLower(filepath.Base(file.Name))
			if file.FileInfo().Mode().IsRegular() && strings.Contains(base, "mihomo") && strings.HasSuffix(base, ".exe") {
				if selected != nil {
					return nil, dataFailure("ambiguous executable in mihomo archive")
				}
				selected = file
			}
		}
		if selected == nil {
			return nil, dataFailure("mihomo executable is missing from archive")
		}
		reader, err = selected.Open()
		if err != nil {
			return nil, err
		}
	} else {
		return nil, dataFailure("unsupported mihomo archive")
	}
	defer func() { resultErr = errors.Join(resultErr, reader.Close()) }()
	binary, err := io.ReadAll(io.LimitReader(reader, maxCoreBinarySize+1))
	if err != nil {
		return nil, dataFailureCause("read mihomo executable", err)
	}
	if len(binary) == 0 || len(binary) > maxCoreBinarySize {
		return nil, dataFailure("invalid mihomo executable size")
	}
	return binary, nil
}

func missingDigestWarning(asset Asset) error {
	return fmt.Errorf("official mihomo asset %s has no SHA-256 digest; downloaded using HTTPS without an upstream checksum", asset.Name)
}

func (c *Candidate) verifiedProtected(ctx context.Context) (*VerifiedCore, error) {
	p := c.protected
	if p.closed.Load() {
		return nil, os.ErrClosed
	}
	observed, err := p.store.Inspect(ctx, UpdateCandidate, p.transaction)
	if err != nil {
		return nil, err
	}
	if !sameObject(observed, p.binary) {
		return nil, dataFailure("core candidate identity changed")
	}
	binary, err := p.store.coreStore().open(ctx, UpdateCandidate, p.transaction)
	if err != nil {
		return nil, err
	}
	current, err := p.store.Inspect(ctx, UpdateCandidate, p.transaction)
	if err == nil && !sameObject(current, p.binary) {
		err = dataFailure("core candidate changed while opening")
	}
	if err != nil {
		return nil, errors.Join(err, binary.Close())
	}
	return &VerifiedCore{store: p.store, binary: binary, candidate: c}, nil
}

// BeginUpdate binds this already-validated candidate to the owner's update intent.
func (c *Candidate) BeginUpdate(ctx context.Context, intent UpdateIntent) (*UpdateTransaction, error) {
	if c == nil || c.protected == nil || c.protected.closed.Load() {
		return nil, dataFailure("protected core candidate unavailable")
	}
	p := c.protected
	return BeginUpdate(ctx, p.store, p.transaction, p.binary, intent)
}

// BeginReinstall supersedes an interrupted update only at explicit user request.
func (c *Candidate) BeginReinstall(ctx context.Context, intent UpdateIntent) (*UpdateTransaction, error) {
	if c == nil || c.protected == nil || c.protected.closed.Load() {
		return nil, dataFailure("core reinstall candidate unavailable")
	}
	p := c.protected
	return beginUpdate(ctx, p.store, p.transaction, p.binary, intent, true)
}

func (c *Candidate) cleanupProtected() {
	p := c.protected
	ctx := context.Background()
	release, err := p.store.coreStore().execution().acquire(ctx)
	if err != nil {
		c.reportCleanup("acquire candidate cleanup ownership", err)
		return
	}
	defer release()
	p.closed.Store(true)
	for _, role := range []ProvenanceRole{PairJournal, UpdateJournal} {
		if _, err := p.store.Load(ctx, role, ""); !errors.Is(err, os.ErrNotExist) {
			return
		}
	}
	for _, item := range []struct {
		role     ProvenanceRole
		expected ProvenanceObject
	}{{UpdateCandidate, p.binary}, {UpdateMarker, p.marker}} {
		actual, err := p.store.Inspect(ctx, item.role, p.transaction)
		if err != nil {
			c.reportCleanup("inspect protected candidate", err)
			continue
		}
		if actual.Present && sameObject(actual, item.expected) {
			c.reportCleanup("remove protected candidate", p.store.Apply(ctx, ProvenanceMutation{Role: item.role, Transaction: p.transaction, Expected: actual}))
		}
	}
}

func (i Installer) stageUpdateCandidate(ctx context.Context, store ProvenanceStore, target ReleaseTarget, binary []byte) (prepared *Candidate, resultErr error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	transaction := hex.EncodeToString(random[:])
	protected := &protectedCandidate{store: store, transaction: transaction}
	candidate := &Candidate{protected: protected, updated: true, version: target.tag, alphaSHA: ParseAlphaSHA(target.asset.Name), reporter: i.Reporter}
	if target.channel == "alpha" {
		candidate.version = "alpha-" + candidate.alphaSHA
	}
	if target.asset.Digest == "" {
		candidate.warnings = []error{missingDigestWarning(target.asset)}
	}
	defer func() {
		if resultErr != nil {
			candidate.Cleanup()
		}
	}()
	for _, item := range []struct {
		role     ProvenanceRole
		data     []byte
		observed *ProvenanceObject
	}{
		{UpdateMarker, []byte(transaction), &protected.marker},
		{UpdateCandidate, binary, &protected.binary},
	} {
		if err := protected.store.Save(ctx, item.role, transaction, item.data); err != nil {
			return nil, err
		}
		observed, err := protected.store.Inspect(ctx, item.role, transaction)
		if err != nil {
			return nil, err
		}
		if !observed.Present || observed.SHA256 != digest(item.data) {
			return nil, dataFailure("core candidate changed while staging")
		}
		*item.observed = observed
	}

	return candidate, nil
}
