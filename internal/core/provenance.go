package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"io"
	"net/http"
	"os"
)

//go:embed supported_policy.json
var supportedPolicyJSON []byte

type supportedAsset struct {
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Tag         string `json:"tag"`
	Channel     string `json:"channel"`
	Asset       string `json:"asset"`
	AssetSHA256 string `json:"asset_sha256"`
	URL         string `json:"url"`
	Size        int64  `json:"size"`
}

func supportedAssets() ([]supportedAsset, error) {
	var entries []supportedAsset
	if err := json.Unmarshal(supportedPolicyJSON, &entries); err != nil {
		return nil, fmt.Errorf("decode compiled core policy: %w", err)
	}
	return entries, nil
}
func supportedCore(ctx context.Context, goos, arch, tag, channel string) (supportedAsset, error) {
	if err := ctx.Err(); err != nil {
		return supportedAsset{}, err
	}
	entries, err := supportedAssets()
	if err != nil {
		return supportedAsset{}, err
	}
	for _, entry := range entries {
		if entry.OS == goos && entry.Arch == arch && entry.Tag == tag && entry.Channel == channel {
			return entry, nil
		}
	}
	return supportedAsset{}, protocol.APIError{Code: protocol.CodeInvalidState, Message: "unsupported mihomo core policy"}
}

// VerifyCompiledAssetDigest checks a digest against the compiled Unix core table.
// Callers must obtain the digest from an independent TLS checksum or a root-only
// offline manifest; a user-supplied request hash is not the trust root.
func VerifyCompiledAssetDigest(ctx context.Context, goos, arch, tag, channel, digestHex string) error {
	a, err := supportedCore(ctx, goos, arch, tag, channel)
	if err != nil {
		return err
	}
	if digestHex != a.AssetSHA256 {
		return dataFailure("mihomo compiled asset hash mismatch")
	}
	return nil
}

// CompiledAssetDigest returns the compiled SHA-256 for one supported Unix core.
func CompiledAssetDigest(ctx context.Context, goos, arch, tag, channel string) (string, error) {
	a, err := supportedCore(ctx, goos, arch, tag, channel)
	if err != nil {
		return "", err
	}
	return a.AssetSHA256, nil
}

const provenanceSchema = "mihari.core-provenance/v1"

// ProvenanceReceipt records a binary derived from an immutable supported asset.
// Receipts are accepted only from the private store, never from a caller or migration tree.
type ProvenanceReceipt struct {
	Schema       string `json:"schema"`
	PolicyID     string `json:"policy_id"`
	AssetSHA256  string `json:"asset_sha256"`
	BinarySHA256 string `json:"binary_sha256"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Tag          string `json:"tag"`
}

func (r ProvenanceReceipt) validate(ctx context.Context) error {
	if r.Schema != provenanceSchema || r.PolicyID != legacyReceiptPolicyID || !validHash(r.BinarySHA256) {
		return dataFailure("invalid mihomo provenance receipt")
	}
	a, err := supportedCore(ctx, r.OS, r.Arch, r.Tag, "stable")
	if err != nil {
		return err
	}
	if a.AssetSHA256 != r.AssetSHA256 {
		return dataFailure("mihomo provenance asset mismatch")
	}
	return nil
}

func (i Installer) prepareTrusted(ctx context.Context, request InstallRequest) (PreparedCore, error) {
	channel := request.Channel
	if channel == "" {
		channel = "stable"
	}
	entries, e := supportedAssets()
	if e != nil {
		return nil, e
	}
	a, e := supportedCore(ctx, i.targetOS(), i.targetArch(), entries[0].Tag, channel)
	if e != nil {
		return nil, e
	}
	if request.CurrentVersion == a.Tag {
		// The recorded version alone is not authority. Recheck the installed
		// receipt, bytes and version before treating this install as a no-op.
		if version, ready := i.localReadyVersion(ctx, request.BinaryPath); ready && version == a.Tag {
			return &Candidate{version: version}, nil
		}
		if e := ctx.Err(); e != nil {
			return nil, e
		}
	}
	if i.GeneratedConfig == nil {
		return nil, dataFailure("generated configuration capability unavailable")
	}
	// Resolve exclusively from the compiled table. Neither GitHub latest nor
	// downloaded digest/version metadata can select a trusted asset.
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "mihari")
	response, e := i.httpClient().Do(req)
	if e != nil {
		return nil, protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "download trusted mihomo core failed"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, dataFailure("trusted core download status")
	}
	archive, e := io.ReadAll(io.LimitReader(response.Body, maxCoreArchiveSize+1))
	if e != nil {
		return nil, e
	}
	if int64(len(archive)) != a.Size || len(archive) > maxCoreArchiveSize || digest(archive) != a.AssetSHA256 {
		return nil, dataFailure("mihomo compiled asset hash mismatch")
	}
	return i.prepareTrustedAsset(ctx, a, archive)
}

// prepareTrustedAsset is private: its asset argument always comes from the
// compiled resolver above. Tests supply compressed fixtures without a public
// alternate trust table, hash override, or executable bypass.
func (i Installer) prepareTrustedAsset(ctx context.Context, a supportedAsset, archive []byte) (prepared PreparedCore, err error) {
	if digest(archive) != a.AssetSHA256 || len(archive) > maxCoreArchiveSize {
		return nil, dataFailure("mihomo compiled asset hash mismatch")
	}
	reader, e := gzip.NewReader(bytes.NewReader(archive))
	if e != nil {
		return nil, dataFailure("invalid trusted core archive")
	}
	defer reader.Close()
	binary, e := io.ReadAll(io.LimitReader(reader, maxCoreBinarySize+1))
	if e != nil || len(binary) == 0 || len(binary) > maxCoreBinarySize {
		return nil, dataFailure("invalid trusted core binary")
	}
	return i.stageTrustedBinary(ctx, a, binary)
}
func (i Installer) stageTrustedBinary(ctx context.Context, a supportedAsset, binary []byte) (prepared PreparedCore, err error) {
	var random [16]byte
	if _, e := rand.Read(random[:]); e != nil {
		return nil, e
	}
	tx := hex.EncodeToString(random[:])
	s := i.Provenance
	receipt := ProvenanceReceipt{Schema: provenanceSchema, PolicyID: legacyReceiptPolicyID, AssetSHA256: a.AssetSHA256, BinarySHA256: digest(binary), OS: a.OS, Arch: a.Arch, Tag: a.Tag}
	rb, e := json.Marshal(receipt)
	if e != nil {
		return nil, e
	}
	c := &Candidate{updated: true, version: a.Tag, trusted: &trustedCandidate{store: s, transaction: tx}}
	defer func() {
		if err != nil {
			c.cleanupTrusted()
		}
	}()
	for _, item := range []struct {
		r ProvenanceRole
		b []byte
	}{{TransactionMarker, []byte(tx)}, {CandidateBinary, binary}, {CandidateReceipt, rb}} {
		if e = s.Save(ctx, item.r, tx, item.b); e != nil {
			return nil, e
		}
	}
	c.trusted.marker, e = s.Inspect(ctx, TransactionMarker, tx)
	if e != nil {
		return nil, e
	}
	c.trusted.binary, e = s.Inspect(ctx, CandidateBinary, tx)
	if e != nil {
		return nil, e
	}
	c.trusted.receipt, e = s.Inspect(ctx, CandidateReceipt, tx)
	if e != nil {
		return nil, e
	}
	if e = s.Sync(ctx); e != nil {
		return nil, e
	}
	v, e := c.Verified(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { _ = v.Close() }() // Read-only capability: no pending writes; closure cannot change the operation result.
	version, e := DetectVerifiedVersion(ctx, v, i.Executor)
	if e != nil || version != a.Tag {
		return nil, dataFailure("trusted core version mismatch")
	}
	configuration, e := i.GeneratedConfig(ctx)
	if e != nil {
		return nil, e
	}
	defer func() { _ = configuration.Close() }() // Read-only capability: no pending writes; closure cannot change the operation result.
	if e = ValidateVerifiedConfig(ctx, v, configuration, i.Executor); e != nil {
		return nil, e
	}
	return c, nil
}
func (c *Candidate) commitTrusted() (InstallResult, error) {
	ctx := context.Background()
	release, e := c.trusted.store.coreStore().execution().acquire(ctx)
	if e != nil {
		return InstallResult{}, e
	}
	defer release()
	v, e := c.Verified(ctx)
	if e != nil {
		return InstallResult{}, e
	}
	if e = v.Close(); e != nil {
		return InstallResult{}, e
	}
	t := c.trusted
	e = commitProvenance(ctx, t.store, t.transaction)
	// Even a failed done/committed fsync can follow a real replacement. Always
	// converge before returning to Manager, which may resume the old process.
	j, journalErr := loadPair(ctx, t.store)
	committed := journalErr == nil && j.Phase == "committed"
	if journalErr == nil {
		t.retired = make(map[ProvenanceRole]ProvenanceObject)
		for _, m := range j.Members {
			t.retired[backupRole(m.Role)] = m.Backup
			t.retired[restoreRole(m.Role)] = m.Restore
			t.retired[quarantineRole(m.Role)] = m.New
		}
	}
	recovery := recoverProvenance(ctx, t.store)
	if recovery != nil {
		return InstallResult{}, protocol.APIError{Code: protocol.CodeDataFailure, Message: "core pair recovery failed; execution prohibited", Details: map[string]any{"degraded": true}}
	}
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	if e != nil && !committed {
		return InstallResult{}, e
	}
	return InstallResult{Version: c.version, Updated: true}, nil
}
func (c *Candidate) cleanupTrusted() {
	t := c.trusted
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	ctx := context.Background()
	release, e := t.store.coreStore().execution().acquire(ctx)
	if e != nil {
		return
	}
	defer release()
	if _, e := t.store.Load(ctx, PairJournal, ""); !errors.Is(e, os.ErrNotExist) {
		return
	}
	for r, expected := range t.retired {
		o, e := t.store.Inspect(ctx, r, t.transaction)
		if e == nil && o.Present && sameObject(o, expected) {
			_ = t.store.Apply(ctx, ProvenanceMutation{Role: r, Transaction: t.transaction, Expected: o})
		}
	}
	// A pending or unreadable journal retains everything. Fixed-role cleanup
	// compares each observed inode immediately before deletion; it never walks
	// arbitrary paths or recursively removes a transaction tree.
	for _, item := range []struct {
		r        ProvenanceRole
		expected ProvenanceObject
	}{{CandidateBinary, t.binary}, {CandidateReceipt, t.receipt}, {TransactionMarker, t.marker}} {
		o, e := t.store.Inspect(ctx, item.r, t.transaction)
		if e != nil || !o.Present || !sameObject(o, item.expected) {
			continue
		}
		_ = t.store.Apply(ctx, ProvenanceMutation{Role: item.r, Transaction: t.transaction, Expected: o})
	}
}

// legacyReceiptPolicyID preserves the historical provenance receipt contract.
const legacyReceiptPolicyID = "mihari.root-config/v1/mihomo-v1.19.30"
