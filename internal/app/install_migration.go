package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/geoip"
	"github.com/mihari-proxy/mihari/internal/panel"
	"github.com/mihari-proxy/mihari/internal/panel/archive"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"go.yaml.in/yaml/v3"
)

type migrationOptions struct {
	Source, Target, Staging migrationCapability
	Request                 InstallRequest
	Trust                   migrationTrust
	GOOS, GOARCH            string
	AfterCopy               func()
	AfterStop               func()
	NewSecret               func() string
	HostSize                func(string) (int64, error)
	Store                   *InstallJournalStore
	FixedSource             string
}

type sourceObservation struct {
	rel          string
	hash         string
	size         int64
	dev, ino     string
	mtime, ctime int64
}

func prepareMigration(ctx context.Context, opts migrationOptions) (*preparedMigration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Source == nil && opts.Request.Source != "" {
		src, err := openMigrationCapability(ctx, opts.Request.Source, false)
		if err != nil {
			return nil, err
		}
		opts.Source = src
	}
	if opts.Staging == nil && opts.Request.Source != "" {
		return nil, migrateInvalid("migration source and staging are required")
	}
	if opts.Source == nil || opts.Staging == nil {
		return nil, migrateInvalid("migration source and staging are required")
	}
	opts.Trust = mergeTrust(compiledInstallerTrust(), opts.Trust)
	goos := opts.GOOS
	if goos == "" || goos == "windows" {
		goos = "linux"
	}
	arch := opts.GOARCH
	if arch == "" {
		arch = "amd64"
	}

	prepared := &preparedMigration{
		source:    opts.Source,
		target:    opts.Target,
		staging:   opts.Staging,
		files:     map[string]preparedFile{},
		hashes:    map[string]string{},
		afterStop: opts.AfterStop,
		cleanupFn: func() {
			if opts.Staging != nil && opts.Staging.Path() != "" {
				_ = os.RemoveAll(opts.Staging.Path())
			}
		},
	}
	fail := func(err error) (*preparedMigration, error) {
		prepared.cleanup()
		return nil, err
	}
	if opts.FixedSource != "" && !equalPath(opts.FixedSource, opts.Source.Path()) && !equalPath(opts.FixedSource, opts.Request.Source) {
		return fail(migrateInvalid("source must match service definition"))
	}
	if opts.Target != nil && nestedMigrationPaths(opts.Source.Path(), opts.Target.Path()) {
		return fail(migrateInvalid("source and target must not nest"))
	}
	if opts.Target != nil {
		sDev, sIno, _ := opts.Source.Identity()
		tDev, tIno, _ := opts.Target.Identity()
		// A bind mount changes the mount ID without changing the underlying tree.
		if sDev != "" && sDev == tDev && sIno == tIno {
			return fail(migrateInvalid("source and target must not nest"))
		}
		populated, err := targetHasBusiness(ctx, opts.Target)
		if err != nil {
			return fail(err)
		}
		if populated {
			return fail(migrateState("unknown target data"))
		}
	}
	if opts.Store != nil {
		journal, err := opts.Store.Load(ctx)
		if err == nil && journal.Phase == InstallPhaseComplete {
			if journal.SourcePath != "" && (equalPath(journal.SourcePath, opts.Source.Path()) || (opts.Request.Source != "" && equalPath(journal.SourcePath, opts.Request.Source))) {
				return fail(migrateState("source already imported"))
			}
		}
	}
	obs := map[string]sourceObservation{}
	var files, total, business int
	record := func(rel string, entry migrationEntry) error {
		files++
		total += int(entry.Size)
		if !strings.HasPrefix(rel, "bin/") {
			business += int(entry.Size)
		}
		if files > migrationMaxFiles || total > migrationMaxBytes || business > migrationBusinessMax {
			return migrateData("migration source exceeds size or file limits")
		}
		return nil
	}

	top, err := opts.Source.List(ctx, ".")
	if err != nil {
		return fail(err)
	}
	seenTop := map[string]bool{}
	bootstrap, err := observeBootstrapSource(ctx, opts.Source)
	if err != nil {
		return fail(err)
	}
	if bootstrap != nil {
		// An installation that never reached business initialization has no
		// settings to migrate. The new daemon initializes its normal defaults.
		prepared.bootstrapOnly = true
		obs = bootstrap
		top = nil
	}
	for _, entry := range top {
		if err := rejectUnsafe(entry, entry.Name); err != nil {
			return fail(err)
		}
		name := entry.Name
		if seenTop[name] {
			return fail(migrateInvalid("duplicate migration source entry"))
		}
		seenTop[name] = true
		if migrationIgnoreTop[name] || entry.Kind == "socket" || entry.Kind == "fifo" {
			continue
		}
		if !migrationAllowedTop[name] {
			return fail(migrateState("unsupported migration source"))
		}
		switch name {
		case "mihari.yaml":
			if err := copyObserved(ctx, opts, prepared, obs, record, name, migrationSettingsMax); err != nil {
				return fail(err)
			}
		case "onboarding.json":
			if err := copyObserved(ctx, opts, prepared, obs, record, name, migrationOnboardingMax); err != nil {
				return fail(err)
			}
		case "control.token", "mihari-channel":
			if err := observeOnly(ctx, opts.Source, obs, record, name); err != nil {
				return fail(err)
			}
		case "subscriptions":
			if err := copySubscriptions(ctx, opts, prepared, obs, record); err != nil {
				return fail(err)
			}
		case "runtime":
			if err := copyRuntime(ctx, opts, prepared, obs, record); err != nil {
				return fail(err)
			}
		case "preferences":
			if err := copyPreferences(ctx, opts, prepared, obs, record); err != nil {
				return fail(err)
			}
		case "bin":
			if err := copyBin(ctx, opts, prepared, obs, record); err != nil {
				return fail(err)
			}
		case "geoip":
			if err := copyGeoIP(ctx, opts, prepared, obs, record); err != nil {
				return fail(err)
			}
		case "web":
			if err := copyWeb(ctx, opts, prepared, obs, record); err != nil {
				return fail(err)
			}
		default:
			return fail(migrateState("unsupported migration source"))
		}
	}

	if opts.AfterCopy != nil {
		opts.AfterCopy()
	}
	prepared.obs = obs
	if err := prepared.verifySource(ctx); err != nil {
		return fail(err)
	}

	if !prepared.bootstrapOnly {
		if err := validateStagedBusiness(ctx, opts, prepared, goos, arch); err != nil {
			return fail(err)
		}
	}
	if err := verifyInstallBinary(ctx, opts, prepared); err != nil {
		return fail(err)
	}
	if err := prepareInstallBundle(ctx, opts, prepared); err != nil {
		return fail(err)
	}
	prepared.obs = obs
	prepared.art = buildPreparedArtifacts(opts, prepared)
	return prepared, nil
}

func copyObserved(ctx context.Context, opts migrationOptions, prepared *preparedMigration, obs map[string]sourceObservation, record func(string, migrationEntry) error, rel string, max int64) error {
	entry, err := opts.Source.Stat(ctx, rel)
	if err != nil {
		return err
	}
	if err := rejectUnsafe(entry, rel); err != nil {
		return err
	}
	if err := record(rel, entry); err != nil {
		return err
	}
	hash, err := opts.Source.CopyFile(ctx, rel, opts.Staging, rel, max)
	if err != nil {
		if errors.Is(err, errMigrationOversize) {
			return migrateData("migration source exceeds size or file limits")
		}
		return err
	}
	obs[rel] = rememberObservation(rel, hash, entry)
	prepared.files[rel] = preparedFile{rel: rel, hash: hash, size: entry.Size}
	prepared.hashes[rel] = hash
	return nil
}

func observeOnly(ctx context.Context, source migrationCapability, obs map[string]sourceObservation, record func(string, migrationEntry) error, rel string) error {
	entry, err := source.Stat(ctx, rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := rejectUnsafe(entry, rel); err != nil {
		return err
	}
	if err := record(rel, entry); err != nil {
		return err
	}
	obs[rel] = rememberObservation(rel, entry.Hash, entry)
	return nil
}

func rememberObservation(rel, hash string, entry migrationEntry) sourceObservation {
	return sourceObservation{
		rel: rel, hash: hash, size: entry.Size,
		dev: entry.Dev, ino: entry.Ino, mtime: entry.Mtime, ctime: entry.Ctime,
	}
}

func copySubscriptions(ctx context.Context, opts migrationOptions, prepared *preparedMigration, obs map[string]sourceObservation, record func(string, migrationEntry) error) error {
	if err := opts.Staging.Mkdir(ctx, "subscriptions/cache"); err != nil {
		return err
	}
	entries, err := opts.Source.List(ctx, "subscriptions")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := rejectUnsafe(entry, "subscriptions/"+entry.Name); err != nil {
			return err
		}
		switch entry.Name {
		case "catalog.yaml":
			if err := copyObserved(ctx, opts, prepared, obs, record, "subscriptions/catalog.yaml", migrationCatalogMax); err != nil {
				return err
			}
		case "cache":
			if !entry.Dir {
				return migrateState("unsupported migration source")
			}
			caches, err := opts.Source.List(ctx, "subscriptions/cache")
			if err != nil {
				return err
			}
			for _, cache := range caches {
				rel := "subscriptions/cache/" + cache.Name
				if err := rejectUnsafe(cache, rel); err != nil {
					return err
				}
				id := strings.TrimSuffix(cache.Name, ".yaml")
				if cache.Dir || !strings.HasSuffix(cache.Name, ".yaml") || !profileIDName(id) {
					return migrateState("unsupported migration source")
				}
				if err := copyObserved(ctx, opts, prepared, obs, record, rel, migrationCacheDocMax); err != nil {
					return err
				}
			}
		default:
			if entry.Kind == "socket" || entry.Kind == "fifo" {
				continue
			}
			return migrateState("unsupported migration source")
		}
	}
	return nil
}

func copyRuntime(ctx context.Context, opts migrationOptions, prepared *preparedMigration, obs map[string]sourceObservation, record func(string, migrationEntry) error) error {
	entries, err := opts.Source.List(ctx, "runtime")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		rel := "runtime/" + entry.Name
		if err := rejectUnsafe(entry, rel); err != nil {
			return err
		}
		switch entry.Name {
		case "config.yaml":
			if err := observeOnly(ctx, opts.Source, obs, record, rel); err != nil {
				return err
			}
		case "core-home":
			if !entry.Dir {
				return migrateState("unsupported migration source")
			}
			if err := copyProviders(ctx, opts, prepared, obs, record); err != nil {
				return err
			}
		default:
			if entry.Kind == "socket" || entry.Kind == "fifo" {
				continue
			}
			return migrateState("unsupported migration source")
		}
	}
	return nil
}

func copyProviders(ctx context.Context, opts migrationOptions, prepared *preparedMigration, obs map[string]sourceObservation, record func(string, migrationEntry) error) error {
	home, err := opts.Source.List(ctx, "runtime/core-home")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range home {
		rel := "runtime/core-home/" + entry.Name
		if err := rejectUnsafe(entry, rel); err != nil {
			return err
		}
		if entry.Name != "providers" {
			if entry.Kind == "socket" || entry.Kind == "fifo" {
				continue
			}
			continue
		}
		if !entry.Dir {
			return migrateState("unsupported migration source")
		}
		providers, err := opts.Source.List(ctx, rel)
		if err != nil {
			return err
		}
		for _, provider := range providers {
			path := rel + "/" + provider.Name
			if err := rejectUnsafe(provider, path); err != nil {
				return err
			}
			if provider.Dir || provider.Kind == "socket" || provider.Kind == "fifo" {
				continue
			}
			if !strings.HasSuffix(provider.Name, ".yaml") && !strings.HasSuffix(provider.Name, ".txt") {
				return migrateState("unsupported migration source")
			}
			if err := copyObserved(ctx, opts, prepared, obs, record, path, migrationCacheDocMax); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyPreferences(ctx context.Context, opts migrationOptions, prepared *preparedMigration, obs map[string]sourceObservation, record func(string, migrationEntry) error) error {
	entries, err := opts.Source.List(ctx, "preferences")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		rel := "preferences/" + entry.Name
		if err := rejectUnsafe(entry, rel); err != nil {
			return err
		}
		if entry.Name != "tui.json" {
			if entry.Kind == "socket" || entry.Kind == "fifo" {
				continue
			}
			return migrateState("unsupported migration source")
		}
		if err := copyObserved(ctx, opts, prepared, obs, record, rel, migrationTUIMax); err != nil {
			return err
		}
	}
	return nil
}

func copyBin(ctx context.Context, opts migrationOptions, prepared *preparedMigration, obs map[string]sourceObservation, record func(string, migrationEntry) error) error {
	entries, err := opts.Source.List(ctx, "bin")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		rel := "bin/" + entry.Name
		if err := rejectUnsafe(entry, rel); err != nil {
			return err
		}
		switch entry.Name {
		case "core-channel":
			if err := observeOnly(ctx, opts.Source, obs, record, rel); err != nil {
				return err
			}
		case "mihomo", "mihomo.exe":
			if err := copyObserved(ctx, opts, prepared, obs, record, rel, migrationBinaryMax); err != nil {
				return err
			}
			hash := prepared.hashes[rel]
			if !opts.Trust.acceptsCore(hash) {
				if err := core.VerifyCompiledAssetDigest(ctx, opts.GOOS, opts.GOARCH, "v1.19.30", "stable", hash); err != nil {
					return migrateState("untrusted core")
				}
			}
			prepared.coreHash = hash
		default:
			if entry.Kind == "socket" || entry.Kind == "fifo" {
				continue
			}
			return migrateState("unsupported migration source")
		}
	}
	return nil
}

func copyGeoIP(ctx context.Context, opts migrationOptions, prepared *preparedMigration, obs map[string]sourceObservation, record func(string, migrationEntry) error) error {
	entries, err := opts.Source.List(ctx, "geoip")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		rel := "geoip/" + entry.Name
		if err := rejectUnsafe(entry, rel); err != nil {
			return err
		}
		switch entry.Name {
		case "GeoLite2-Country.mmdb", "GeoLite2-ASN.mmdb":
			if err := copyObserved(ctx, opts, prepared, obs, record, rel, 128<<20); err != nil {
				return err
			}
			hash := prepared.hashes[rel]
			if !opts.Trust.acceptsGeo(hash) {
				kind := subscription.GeoCountryMMDB
				if entry.Name == "GeoLite2-ASN.mmdb" {
					kind = subscription.GeoASNMMDB
				}
				if err := subscription.VerifyGeoDigest(kind, hash); err != nil {
					return migrateState("untrusted geoip")
				}
			}
			path := filepath.Join(opts.Staging.Path(), filepath.FromSlash(rel))
			if err := geoip.ValidateMMDBFile(path); err != nil {
				return migrateData("invalid geoip database")
			}
		default:
			if entry.Kind == "socket" || entry.Kind == "fifo" {
				continue
			}
			return migrateState("unsupported migration source")
		}
	}
	return nil
}

func copyWeb(ctx context.Context, opts migrationOptions, prepared *preparedMigration, obs map[string]sourceObservation, record func(string, migrationEntry) error) error {
	entries, err := opts.Source.List(ctx, "web")
	if err != nil {
		return err
	}
	var active []byte
	for _, entry := range entries {
		rel := "web/" + entry.Name
		if err := rejectUnsafe(entry, rel); err != nil {
			return err
		}
		switch entry.Name {
		case "credential":
			if err := observeOnly(ctx, opts.Source, obs, record, rel); err != nil {
				return err
			}
		case "active.json":
			if err := observeOnly(ctx, opts.Source, obs, record, rel); err != nil {
				return err
			}
			raw, err := opts.Source.ReadFile(ctx, rel, 1<<20)
			if err != nil {
				return err
			}
			active = raw
		default:
			if entry.Kind == "socket" || entry.Kind == "fifo" {
				continue
			}
			if err := record(rel, entry); err != nil {
				return err
			}
		}
	}
	if len(active) == 0 {
		return nil
	}
	var pointer panel.Active
	if err := json.Unmarshal(active, &pointer); err != nil || pointer.Panel == "" || pointer.Build == "" {
		return migrateData("invalid panel active pointer")
	}
	zipBytes, ok := opts.Trust.panelZip(pointer.Panel, pointer.Build)
	if !ok {
		return migrateState("untrusted panel")
	}
	prefix := "web/" + pointer.Panel + "/" + pointer.Build
	if err := opts.Staging.Mkdir(ctx, prefix); err != nil {
		return err
	}
	if err := archive.ExtractZipBytes(zipBytes, archive.Limits{
		MaxFile: uint64(migrationBundleExpand), MaxTotal: uint64(migrationBundleExpand),
		MaxEntries: migrationBundleFiles, MaxDepth: migrationMaxDepth,
	}, func(name string) error {
		return opts.Staging.Mkdir(ctx, prefix+"/"+name)
	}, func(name string, body []byte) error {
		rel := prefix + "/" + name
		if err := opts.Staging.WriteFile(ctx, rel, body); err != nil {
			return err
		}
		hash := sha256HexBytes(body)
		prepared.files[rel] = preparedFile{rel: rel, hash: hash, size: int64(len(body))}
		prepared.hashes[rel] = hash
		return nil
	}); err != nil {
		return err
	}
	if err := opts.Staging.WriteFile(ctx, "web/active.json", active); err != nil {
		return err
	}
	prepared.files["web/active.json"] = preparedFile{rel: "web/active.json", hash: sha256HexBytes(active), size: int64(len(active))}
	prepared.hashes["web/active.json"] = sha256HexBytes(active)
	return nil
}

func rejectUnsafe(entry migrationEntry, rel string) error {
	if entry.Kind == "symlink" || entry.Kind == "device" {
		return migrateInvalid("unsafe object in migration source")
	}
	if entry.Nlink > 1 || entry.Kind == "hardlink" {
		return migrateInvalid("hardlink in migration source")
	}
	if entry.NestedMount {
		return migrateInvalid("nested mount in migration source")
	}
	_ = rel
	return nil
}

func verifyStationary(ctx context.Context, source migrationCapability, obs map[string]sourceObservation) error {
	for rel, seen := range obs {
		entry, err := source.Stat(ctx, rel)
		if err != nil {
			return migrateConflict("migration source changed during copy")
		}
		if entry.Hash != "" && seen.hash != "" && entry.Hash != seen.hash {
			return migrateConflict("migration source changed during copy")
		}
		if seen.size != 0 && entry.Size != seen.size {
			return migrateConflict("migration source changed during copy")
		}
		if seen.dev != "" && entry.Dev != "" && seen.dev != entry.Dev {
			return migrateConflict("migration source changed during copy")
		}
		if seen.ino != "" && entry.Ino != "" && seen.ino != entry.Ino {
			return migrateConflict("migration source changed during copy")
		}
		if seen.mtime != 0 && entry.Mtime != 0 && seen.mtime != entry.Mtime {
			return migrateConflict("migration source changed during copy")
		}
		if seen.ctime != 0 && entry.Ctime != 0 && seen.ctime != entry.Ctime {
			return migrateConflict("migration source changed during copy")
		}
	}
	entries, err := source.List(ctx, ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if migrationIgnoreTop[entry.Name] || entry.Kind == "socket" || entry.Kind == "fifo" {
			continue
		}
		if !migrationAllowedTop[entry.Name] {
			return migrateConflict("migration source changed during copy")
		}
	}
	return nil
}

func targetHasBusiness(ctx context.Context, target migrationCapability) (bool, error) {
	if target == nil {
		return false, nil
	}
	entries, err := target.List(ctx, ".")
	if err != nil {
		if isNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if migrationIgnoreTop[entry.Name] || entry.Kind == "socket" || entry.Kind == "fifo" {
			continue
		}
		if migrationAllowedTop[entry.Name] {
			return true, nil
		}
		if entry.Name != "" {
			return true, nil
		}
	}
	return false, nil
}

func validateStagedBusiness(ctx context.Context, opts migrationOptions, prepared *preparedMigration, goos, arch string) error {
	settingsRaw, err := opts.Staging.ReadFile(ctx, "mihari.yaml", migrationSettingsMax)
	if err != nil {
		return err
	}
	settings, err := decodeSettingsBytes(settingsRaw)
	if err != nil {
		return err
	}
	secret := ""
	if opts.NewSecret != nil {
		secret = opts.NewSecret()
	}
	if secret == "" {
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return err
		}
		secret = hex.EncodeToString(raw[:])
	}
	settings.ControllerSecret = secret
	encoded, err := yaml.Marshal(settings)
	if err != nil {
		return migrateData("encode settings")
	}
	if err := opts.Staging.WriteFile(ctx, "mihari.yaml", encoded); err != nil {
		return err
	}
	prepared.settings = encoded
	prepared.files["mihari.yaml"] = preparedFile{rel: "mihari.yaml", hash: sha256HexBytes(encoded), size: int64(len(encoded))}
	prepared.hashes["mihari.yaml"] = sha256HexBytes(encoded)

	if _, ok := prepared.files["onboarding.json"]; ok {
		raw, err := opts.Staging.ReadFile(ctx, "onboarding.json", migrationOnboardingMax)
		if err != nil {
			return err
		}
		if err := decodeOnboardingBytes(raw); err != nil {
			return err
		}
	}
	if _, ok := prepared.files["preferences/tui.json"]; ok {
		raw, err := opts.Staging.ReadFile(ctx, "preferences/tui.json", migrationTUIMax)
		if err != nil {
			return err
		}
		if err := decodeTUIBytes(raw); err != nil {
			return err
		}
	}

	catalogRaw, err := opts.Staging.ReadFile(ctx, "subscriptions/catalog.yaml", migrationCatalogMax)
	if err != nil {
		return err
	}
	catalog, err := decodeCatalogBytes(catalogRaw)
	if err != nil {
		return err
	}
	if catalog.ActiveID == "" {
		return migrateData("missing active subscription cache")
	}
	prepared.activeID = catalog.ActiveID
	var cacheSum int
	resources := map[string][]byte{}
	for _, profile := range catalog.Profiles {
		if profile.Generation == 0 {
			continue
		}
		rel := "subscriptions/cache/" + profile.ID + ".yaml"
		raw, err := opts.Staging.ReadFile(ctx, rel, migrationCacheDocMax)
		if err != nil {
			if profile.ID == catalog.ActiveID {
				return migrateData("missing active subscription cache")
			}
			return migrateData("missing subscription cache")
		}
		cacheSum += len(raw)
		if cacheSum > migrationCacheSumMax {
			return migrateData("migration source exceeds size or file limits")
		}
		if _, err := subscription.ParseDocument(raw); err != nil {
			return err
		}
		if profile.ID != catalog.ActiveID {
			continue
		}
		input := subscription.PolicyInput{
			YAML: raw, SubscriptionID: profile.ID, Generation: profile.Generation,
			CoreTag: "v1.19.30", OS: goos, Arch: arch, Settings: settings,
		}
		need, err := subscription.NewRootConfigPolicy().Inspect(ctx, input)
		if err != nil {
			return err
		}
		for _, spec := range need.Providers {
			key := spec.ResourceID
			if spec.SourceResourceID != "" {
				key = spec.SourceResourceID
			}
			if spec.URL == "" && spec.SourceResourceID == "" {
				continue
			}
			raw, err := loadProviderResource(ctx, opts.Staging, key)
			if err != nil {
				return migrateData("missing provider resource")
			}
			resources[key] = raw
		}
		input.Resources = resources
		out, err := subscription.GenerateWithPolicy(ctx, input, subscription.NewRootConfigPolicy())
		if err != nil {
			return err
		}
		if err := opts.Staging.Mkdir(ctx, "runtime"); err != nil {
			return err
		}
		if err := opts.Staging.WriteFile(ctx, "runtime/config.yaml", out.YAML); err != nil {
			return err
		}
		prepared.runtimeYAML = out.YAML
		prepared.files["runtime/config.yaml"] = preparedFile{rel: "runtime/config.yaml", hash: sha256HexBytes(out.YAML), size: int64(len(out.YAML))}
		prepared.hashes["runtime/config.yaml"] = sha256HexBytes(out.YAML)
	}
	if prepared.runtimeYAML == nil {
		return migrateData("missing active subscription cache")
	}
	return nil
}

func loadProviderResource(ctx context.Context, staging migrationCapability, key string) ([]byte, error) {
	for _, rel := range []string{
		"runtime/core-home/providers/" + key + ".yaml",
		"runtime/core-home/providers/" + key + ".txt",
	} {
		raw, err := staging.ReadFile(ctx, rel, migrationCacheDocMax)
		if err == nil {
			return raw, nil
		}
		if !errors.Is(err, os.ErrNotExist) && !isNotExist(err) {
			return nil, err
		}
	}
	return nil, os.ErrNotExist
}

func isNotExist(err error) bool {
	return err != nil && (errors.Is(err, os.ErrNotExist) || strings.Contains(strings.ToLower(err.Error()), "not exist"))
}

func prepareInstallBundle(ctx context.Context, opts migrationOptions, prepared *preparedMigration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if opts.Request.Bundle == "" {
		return nil
	}
	sizeFn := hostFileSize
	if opts.HostSize != nil {
		sizeFn = opts.HostSize
	}
	size, err := sizeFn(opts.Request.Bundle)
	if err != nil {
		return err
	}
	if size > int64(migrationBundleComp) {
		return migrateData("install bundle exceeds compressed size limit")
	}
	data, err := readHostFile(opts.Request.Bundle, int64(migrationBundleComp))
	if err != nil {
		if errors.Is(err, errMigrationOversize) {
			return migrateData("install bundle exceeds compressed size limit")
		}
		return err
	}
	hash := sha256HexBytes(data)
	_ = opts.Request.BundleSHA256
	if !opts.Trust.acceptsBundle(hash) {
		return migrateState("untrusted install bundle")
	}
	if err := opts.Staging.WriteFile(ctx, ".bundle.zip", data); err != nil {
		return err
	}
	if err := opts.Staging.Mkdir(ctx, ".bundle"); err != nil {
		return err
	}
	extract := archive.ExtractZipBytes
	if bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		extract = archive.ExtractTarGzipBytes
	}
	if err := extract(data, archive.Limits{
		MaxFile: uint64(migrationBundleExpand), MaxTotal: uint64(migrationBundleExpand),
		MaxEntries: migrationBundleFiles, MaxDepth: migrationMaxDepth,
	}, func(name string) error {
		return opts.Staging.Mkdir(ctx, ".bundle/"+name)
	}, func(name string, body []byte) error {
		return opts.Staging.WriteFile(ctx, ".bundle/"+name, body)
	}); err != nil {
		return err
	}
	prepared.hashes[".bundle.zip"] = hash
	return nil
}

func hostFileSize(path string) (int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, migrateInvalid("unsafe install artifact")
	}
	return info.Size(), nil
}

func verifyInstallBinary(ctx context.Context, opts migrationOptions, prepared *preparedMigration) error {
	if opts.Request.Binary == "" {
		return nil
	}
	data, err := readHostFile(opts.Request.Binary, migrationBinaryMax)
	if err != nil {
		if errors.Is(err, errMigrationOversize) {
			return migrateData("install binary exceeds size limit")
		}
		return err
	}
	hash := sha256HexBytes(data)
	if opts.Request.ArtifactSHA256 != "" && opts.Request.ArtifactSHA256 != hash {
		// User claim is not the trust root; mismatch is ignored except as a hint.
		_ = opts.Request.ArtifactSHA256
	}
	if !opts.Trust.acceptsBinary(hash) {
		return migrateState("untrusted install binary")
	}
	samePath := opts.Request.PathBinary != "" && equalPath(opts.Request.Binary, opts.Request.PathBinary)
	prepared.art.ManagedNew = append([]byte(nil), data...)
	if !samePath && opts.Request.PathBinary != "" {
		pathData, err := readHostFile(opts.Request.PathBinary, migrationBinaryMax)
		if err != nil {
			return err
		}
		if sha256HexBytes(pathData) != hash {
			prepared.art.PathNew = pathData
		}
	}
	return nil
}

func buildPreparedArtifacts(opts migrationOptions, prepared *preparedMigration) InstallArtifacts {
	art := prepared.art
	art.DataAction = InstallDataCreate
	art.Source = opts.Source.Path()
	if opts.Target != nil {
		art.Target = opts.Target.Path()
		art.DataRoot = opts.Target.Path()
	}
	if opts.Request.Data != "" {
		art.Target = opts.Request.Data
		art.DataRoot = opts.Request.Data
	}
	if opts.Request.InstallRoot != "" {
		art.Install = opts.Request.InstallRoot
	}
	if opts.Request.Endpoint != "" {
		art.Endpoint = opts.Request.Endpoint
	}
	if opts.Request.Credential != "" {
		art.Credential = opts.Request.Credential
	}
	if art.Target == "" && opts.Target != nil {
		art.Target = opts.Target.Path()
		art.DataRoot = art.Target
	}
	if art.Install == "" && opts.Request.Binary != "" {
		art.Install = filepath.Dir(opts.Request.Binary)
	}
	if art.Endpoint == "" && art.Target != "" {
		art.Endpoint = filepath.Join(art.Target, "control.sock")
	}
	if art.Credential == "" && art.Target != "" {
		art.Credential = filepath.Join(art.Target, "control.token")
	}
	rels := make([]string, 0, len(prepared.files))
	for rel := range prepared.files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	var digest bytes.Buffer
	for _, rel := range rels {
		digest.WriteString(rel)
		digest.WriteString(prepared.files[rel].hash)
	}
	art.CandidateHash = sha256HexBytes(digest.Bytes())
	if art.BackupHash == "" {
		art.BackupHash = sha256Hex("source-backup")
	}
	art.DataNew = []byte(art.CandidateHash)
	art.DefinitionNew = []byte("mihari.install-definition/v1\n" + art.CandidateHash)
	art.SourceBytes = []byte(art.Source)
	if art.BootID == "" {
		art.BootID = "11111111-1111-1111-1111-111111111111"
	}
	if opts.Request.PathBinary == "" || equalPath(opts.Request.Binary, opts.Request.PathBinary) {
		art.PathOld = nil
		art.PathNew = nil
	}
	return art
}

func (p *preparedMigration) recheckAndPublish(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if p.afterStop != nil {
		p.afterStop()
	}
	if err := p.verifySource(ctx); err != nil {
		return err
	}
	if p.target == nil || p.staging == nil {
		return nil
	}
	rels := make([]string, 0, len(p.files))
	for rel := range p.files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		if _, err := p.staging.CopyFile(ctx, rel, p.target, rel, migrationBusinessMax); err != nil {
			return err
		}
	}
	return nil
}

func decodeSettingsBytes(raw []byte) (config.Settings, error) {
	if len(raw) == 0 || len(raw) > migrationSettingsMax {
		return config.Settings{}, migrateData("migration source exceeds size or file limits")
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var settings config.Settings
	if err := dec.Decode(&settings); err != nil {
		return config.Settings{}, migrateData("invalid settings")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return config.Settings{}, migrateData("invalid settings")
	}
	if err := settings.Validate(); err != nil {
		return config.Settings{}, err
	}
	if settings.ControllerSecret == "" {
		return config.Settings{}, migrateData("controller secret is required")
	}
	return settings, nil
}

func decodeCatalogBytes(raw []byte) (subscription.Catalog, error) {
	if len(raw) == 0 || len(raw) > migrationCatalogMax {
		return subscription.Catalog{}, migrateData("migration source exceeds size or file limits")
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var catalog subscription.Catalog
	if err := dec.Decode(&catalog); err != nil {
		return subscription.Catalog{}, migrateData("invalid subscription catalog")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return subscription.Catalog{}, migrateData("invalid subscription catalog")
	}
	if err := catalog.Normalize(); err != nil {
		return subscription.Catalog{}, err
	}
	return catalog, nil
}

func decodeOnboardingBytes(raw []byte) error {
	if len(raw) == 0 || len(raw) > migrationOnboardingMax {
		return migrateData("migration source exceeds size or file limits")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var persisted struct {
		Schema   string `json:"schema"`
		Complete bool   `json:"complete"`
	}
	if err := dec.Decode(&persisted); err != nil {
		return migrateData("invalid onboarding state")
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return migrateData("invalid onboarding state")
	}
	if persisted.Schema != "mihari.onboarding/v1" {
		return migrateData("unsupported onboarding state schema")
	}
	return nil
}

func decodeTUIBytes(raw []byte) error {
	if len(raw) == 0 || len(raw) > migrationTUIMax {
		return migrateData("migration source exceeds size or file limits")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var persisted struct {
		Schema             string   `json:"schema"`
		ConnectionsColumns []string `json:"connections_columns"`
	}
	if err := dec.Decode(&persisted); err != nil {
		return migrateData("invalid tui preferences")
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return migrateData("invalid tui preferences")
	}
	if persisted.Schema != "mihari.tui-preferences/v1" {
		return migrateData("unsupported tui preferences schema")
	}
	return nil
}

func profileIDName(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// MigrationSourceNested reports whether source and target are the same or nested.
func MigrationSourceNested(source, target string) bool {
	return nestedMigrationPaths(source, target)
}

func nestedMigrationPaths(source, target string) bool {
	source = filepath.Clean(source)
	target = filepath.Clean(target)
	if source == "" || target == "" {
		return false
	}
	if equalPath(source, target) {
		return true
	}
	return pathInside(source, target) || pathInside(target, source)
}

func equalPath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func pathInside(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, "../")
}

func migrateInvalid(message string) error {
	return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: message}
}

func migrateState(message string) error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: message}
}

func migrateData(message string) error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: message}
}

func migrateConflict(message string) error {
	return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: message}
}

func (x *InstallTransaction) hasInjectedArtifacts() bool {
	if x == nil {
		return false
	}
	art := x.Artifacts
	return art.CandidateHash != "" || len(art.DefinitionNew) > 0 || len(art.ManagedNew) > 0 || len(art.DataNew) > 0
}

func (x *InstallTransaction) ensurePrepared(ctx context.Context, req InstallRequest) error {
	if x.prepared != nil || x.hasInjectedArtifacts() {
		return nil
	}
	if x.migrate == nil {
		return nil
	}
	opts := *x.migrate
	opts.Request = req
	if opts.Store == nil {
		opts.Store = x.Store
	}
	if opts.FixedSource == "" && x.Service != nil {
		def, err := x.Service.InspectDefinition(ctx)
		if err == nil && def.Status != service.StatusNotInstalled {
			for _, env := range def.Env {
				if strings.HasPrefix(env, "MIHARI_DATA=") {
					opts.FixedSource = strings.TrimPrefix(env, "MIHARI_DATA=")
				}
			}
		}
	}
	prepared, err := prepareMigration(ctx, opts)
	if err != nil {
		return err
	}
	x.prepared = prepared
	return nil
}

// businessMigrationRequest separates typed business migration from installation
// artifacts which the native release verifier and file stager already own.
func businessMigrationRequest(req InstallRequest) InstallRequest {
	req.Binary, req.PathBinary, req.ArtifactSHA256 = "", "", ""
	req.Bundle, req.BundleSHA256 = "", ""
	return req
}
