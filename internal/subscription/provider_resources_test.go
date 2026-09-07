package subscription

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type providerDownloadFunc func(context.Context, ProviderSpec, string) ([]byte, error)

func (f providerDownloadFunc) Download(ctx context.Context, s ProviderSpec, m string) ([]byte, error) {
	return f(ctx, s, m)
}

type geoDownloadFunc func(context.Context, GeoResourceKind, string) ([]byte, error)

func (f geoDownloadFunc) DownloadGeo(ctx context.Context, k GeoResourceKind, m string) ([]byte, error) {
	return f(ctx, k, m)
}
func TestResourcePreparation_ClosesProviderGeoGraph(t *testing.T) {
	input := rootPolicyInput()
	input.YAML = []byte("rule-providers:\n  source: {type: http, behavior: classical, url: 'https://example.test/rules'}\nrules: ['RULE-SET,source,DIRECT']\n")
	fs := newMemoryProviderFiles()
	s := &ProviderStore{files: fs}
	downloaded := false
	geoFetched := false
	p := NewResourcePreparer(s, providerDownloadFunc(func(_ context.Context, s ProviderSpec, m string) ([]byte, error) {
		downloaded = true
		return []byte("payload: ['GEOSITE,CN']"), nil
	}), geoDownloadFunc(func(_ context.Context, k GeoResourceKind, m string) ([]byte, error) {
		geoFetched = true
		if k != GeoSiteDAT {
			t.Fatal("wrong Geo closure")
		}
		return geoSiteFixture(), nil
	}))
	p.catalog = func(k GeoResourceKind) (geoArtifact, error) {
		return geoArtifact{hash: providerDigest(geoSiteFixture()), size: int64(len(geoSiteFixture()))}, nil
	}
	result, err := p.Prepare(context.Background(), input, ProxyModeDirect, nil)
	if err != nil {
		t.Fatalf("complete resource graph failed: %v", err)
	}
	if !downloaded || !geoFetched || len(result.output.Providers) != 1 || len(result.output.Geo) != 1 {
		t.Fatal("resource closure incomplete")
	}
	if strings.Contains(string(result.output.YAML), "https://example.test") {
		t.Fatal("native source remained")
	}
	if len(result.providers) != 1 || len(result.geo) != 1 {
		t.Fatal("resource bytes were not privately staged")
	}
}

func TestResourcePreparation_FileSourceRequiresAuthorizedGraphAndRecheck(t *testing.T) {
	ctx := context.Background()
	fs := newMemoryProviderFiles()
	s := &ProviderStore{files: fs}
	current := rootPolicyInput()
	current.YAML = []byte("rule-providers:\n  source: {type: inline, behavior: domain, payload: ['example.test']}\nrules: ['RULE-SET,source,DIRECT']\n")
	old, err := NewRootConfigPolicy().Build(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	target, err := providerTarget(old.Providers[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = fs.write(ctx, target, old.Providers[0].Inline, providerObject{}); err != nil {
		t.Fatal(err)
	}
	graph, err := s.SnapshotResources(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	input := rootPolicyInput()
	input.Generation = 2
	input.YAML = []byte("rule-providers:\n  copy: {type: file, behavior: domain, path: '" + old.Providers[0].ResourceID + "'}\nrules: ['RULE-SET,copy,DIRECT']\n")
	prep := NewResourcePreparer(s, nil, nil)
	if _, err = prep.Prepare(ctx, input, ProxyModeDirect, nil); err == nil {
		t.Fatal("ID syntax authorized private source")
	}
	foreign := &ResourceGraph{store: &ProviderStore{files: newMemoryProviderFiles()}, sources: graph.sources}
	if _, err = prep.Prepare(ctx, input, ProxyModeDirect, foreign); err == nil {
		t.Fatal("foreign store graph accepted")
	}
	result, err := prep.Prepare(ctx, input, ProxyModeDirect, graph)
	if err != nil {
		t.Fatal(err)
	}
	if result.output.Providers[0].ResourceID == old.Providers[0].ResourceID || result.output.Providers[0].SourceResourceID != old.Providers[0].ResourceID {
		t.Fatal("source/destination identity confused")
	}
	if err = result.Recheck(ctx); err != nil {
		t.Fatal(err)
	}
	observed, _ := fs.inspect(ctx, target)
	if err = fs.write(ctx, target, old.Providers[0].Inline, observed); err != nil {
		t.Fatal(err)
	}
	if err = result.Recheck(ctx); err == nil {
		t.Fatal("same-byte replaced source accepted")
	}
	if err = result.Close(ctx); err != nil {
		t.Fatal(err)
	}
	for path := range fs.objects {
		if strings.HasPrefix(path, "staging/providers/") {
			t.Fatalf("private candidate leaked: %s", path)
		}
	}
}

func TestResourcePreparation_RejectsUnknownGeoTrustEvenIfStructurallyValid(t *testing.T) {
	input := rootPolicyInput()
	input.YAML = []byte("rules: ['GEOSITE,CN,DIRECT']\n")
	s := &ProviderStore{files: newMemoryProviderFiles()}
	p := NewResourcePreparer(s, nil, geoDownloadFunc(func(context.Context, GeoResourceKind, string) ([]byte, error) { return geoSiteFixture(), nil }))
	if _, err := p.Prepare(context.Background(), input, ProxyModeDirect, nil); err == nil {
		t.Fatal("unapproved valid DAT accepted")
	}
}

func TestResourcePreparation_OfflineUsesOnlyCurrentAuthorizedFiles(t *testing.T) {
	ctx := context.Background()
	input := rootPolicyInput()
	input.YAML = []byte("rule-providers:\n  domains: {type: http, behavior: domain, url: 'https://example.test/rules'}\nrules: ['RULE-SET,domains,DIRECT']\n")
	policy := NewRootConfigPolicy()
	requirements, err := policy.Inspect(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	fs := newMemoryProviderFiles()
	store := &ProviderStore{files: fs}
	target, err := providerTarget(requirements.Providers[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = fs.write(ctx, target, []byte("payload: ['example.test']\n"), providerObject{}); err != nil {
		t.Fatal(err)
	}
	graph, err := store.SnapshotResources(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	preparer := NewResourcePreparer(store, providerDownloadFunc(func(context.Context, ProviderSpec, string) ([]byte, error) {
		t.Fatal("offline preparation attempted a download")
		return nil, nil
	}), nil)
	hydrated, err := preparer.OfflineInput(ctx, input, graph)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated.Resources) != 1 {
		t.Fatalf("offline resources=%d", len(hydrated.Resources))
	}
	prepared, err := preparer.PrepareOffline(ctx, input, graph)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close(ctx) }()
	if gotID, gotGeneration := prepared.Identity(); gotID != input.SubscriptionID || gotGeneration != input.Generation {
		t.Fatalf("identity=(%q,%d)", gotID, gotGeneration)
	}
	if err = prepared.Recheck(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestResourcePreparation_OfflineRejectsChangedAuthorizedGeoObject(t *testing.T) {
	ctx := context.Background()
	input := rootPolicyInput()
	input.YAML = []byte("rules: ['GEOSITE,CN,DIRECT']\n")
	fs := newMemoryProviderFiles()
	store := &ProviderStore{files: fs}
	name, err := GeoResourcePath(GeoSiteDAT)
	if err != nil {
		t.Fatal(err)
	}
	path := "runtime/core-home/" + name
	if err = fs.write(ctx, path, geoSiteFixture(), providerObject{}); err != nil {
		t.Fatal(err)
	}
	source, _, err := store.captureResource(ctx, path, maxGeoResourceBytes)
	if err != nil {
		t.Fatal(err)
	}
	id, err := GeoResourceID(GeoSiteDAT)
	if err != nil {
		t.Fatal(err)
	}
	graph := &ResourceGraph{store: store, sources: map[string]resourceSource{id: source}}
	old, _ := fs.inspect(ctx, path)
	if err = fs.write(ctx, path, geoSiteFixture(), old); err != nil {
		t.Fatal(err)
	}
	preparer := NewResourcePreparer(store, nil, nil)
	preparer.catalog = func(GeoResourceKind) (geoArtifact, error) {
		return geoArtifact{hash: providerDigest(geoSiteFixture()), size: int64(len(geoSiteFixture()))}, nil
	}
	if _, err = preparer.OfflineInput(ctx, input, graph); err == nil {
		t.Fatal("changed Geo object accepted by sealed resource graph")
	}
}

func TestResourcePreparation_RefreshesOneProviderAndRevalidatesWholeConfig(t *testing.T) {
	ctx := context.Background()
	input := rootPolicyInput()
	input.YAML = []byte("rule-providers:\n  selected: {type: http, behavior: domain, url: 'https://example.test/selected'}\n  retained: {type: http, behavior: domain, url: 'https://example.test/retained'}\nrules: ['RULE-SET,selected,DIRECT', 'RULE-SET,retained,DIRECT']\n")
	requirements, err := NewRootConfigPolicy().Inspect(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	fs := newMemoryProviderFiles()
	store := &ProviderStore{files: fs}
	for _, spec := range requirements.Providers {
		target, targetErr := providerTarget(spec)
		if targetErr != nil {
			t.Fatal(targetErr)
		}
		if err = fs.write(ctx, target, []byte("payload: ['old.example']\n"), providerObject{}); err != nil {
			t.Fatal(err)
		}
	}
	graph, err := store.SnapshotResources(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	var downloaded []string
	preparer := NewResourcePreparer(store, providerDownloadFunc(func(_ context.Context, spec ProviderSpec, _ string) ([]byte, error) {
		downloaded = append(downloaded, spec.Name)
		return []byte("payload: ['new.example']\n"), nil
	}), nil)
	prepared, err := preparer.PrepareProviderRefresh(ctx, input, ProxyModeDirect, graph, "selected")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close(ctx) }()
	if len(downloaded) != 1 || downloaded[0] != "selected" {
		t.Fatalf("downloaded=%v", downloaded)
	}
	if err = prepared.Recheck(ctx); err != nil {
		t.Fatal(err)
	}
	reloads := 0
	if err = prepared.Commit(ctx, func(context.Context) error { reloads++; return nil }); err != nil {
		t.Fatal(err)
	}
	if reloads != 1 {
		t.Fatalf("reloads=%d", reloads)
	}
}

func TestResourcePreparation_ProviderRefreshRejectsUnknownNameAndChangedRetainedFile(t *testing.T) {
	ctx := context.Background()
	input := rootPolicyInput()
	input.YAML = []byte("rule-providers:\n  selected: {type: http, behavior: domain, url: 'https://example.test/selected'}\n  retained: {type: http, behavior: domain, url: 'https://example.test/retained'}\nrules: ['RULE-SET,selected,DIRECT', 'RULE-SET,retained,DIRECT']\n")
	requirements, err := NewRootConfigPolicy().Inspect(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	fs := newMemoryProviderFiles()
	store := &ProviderStore{files: fs}
	for _, spec := range requirements.Providers {
		target, _ := providerTarget(spec)
		if err = fs.write(ctx, target, []byte("payload: ['old.example']\n"), providerObject{}); err != nil {
			t.Fatal(err)
		}
	}
	graph, err := store.SnapshotResources(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	preparer := NewResourcePreparer(store, providerDownloadFunc(func(context.Context, ProviderSpec, string) ([]byte, error) {
		return []byte("payload: ['new.example']\n"), nil
	}), nil)
	if _, err = preparer.PrepareProviderRefresh(ctx, input, ProxyModeDirect, graph, "missing"); err == nil {
		t.Fatal("unknown provider accepted")
	} else {
		var api protocol.APIError
		if !errors.As(err, &api) || api.Code != protocol.CodeInvalidArgument {
			t.Fatalf("unknown provider error=%v", err)
		}
	}
	prepared, err := preparer.PrepareProviderRefresh(ctx, input, ProxyModeDirect, graph, "selected")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close(ctx) }()
	for _, spec := range requirements.Providers {
		if spec.Name != "retained" {
			continue
		}
		target, _ := providerTarget(spec)
		old, _ := fs.inspect(ctx, target)
		if err = fs.write(ctx, target, []byte("payload: ['old.example']\n"), old); err != nil {
			t.Fatal(err)
		}
	}
	if err = prepared.Recheck(ctx); err == nil {
		t.Fatal("changed retained provider file accepted")
	}
}

func TestResourcePreparation_SelectedHTTPProviderRecheckRejectsStaleDownloadAfterNewerCommit(t *testing.T) {
	ctx := context.Background()
	input := rootPolicyInput()
	input.YAML = []byte("rule-providers:\n  source: {type: http, behavior: domain, url: 'https://example.test/rules'}\nrules: ['RULE-SET,source,DIRECT']\n")
	fs := newMemoryProviderFiles()
	store := &ProviderStore{files: fs}
	req, err := NewRootConfigPolicy().Inspect(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	spec := req.Providers[0]
	spec.Inline = []byte("payload: ['old.test']\n")
	initial, err := store.Prepare(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if err = initial.Commit(ctx, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	graph, err := store.SnapshotResources(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	preparer := NewResourcePreparer(store, providerDownloadFunc(func(context.Context, ProviderSpec, string) ([]byte, error) {
		newer := spec
		newer.Inline = []byte("payload: ['newer.test']\n")
		prepared, e := store.Prepare(ctx, newer)
		if e != nil {
			return nil, e
		}
		if e = prepared.Commit(ctx, func(context.Context) error { return nil }); e != nil {
			return nil, e
		}
		return []byte("payload: ['stale.test']\n"), nil
	}), nil)
	prepared, err := preparer.PrepareProvider(ctx, input, ProxyModeDirect, graph, "rule", "source")
	if err != nil {
		t.Fatalf("prepare selected provider: %v", err)
	}
	defer func() { _ = prepared.Close(ctx) }()
	if err = prepared.Recheck(ctx); err == nil {
		t.Fatal("selected provider changed after graph snapshot during download, but stale candidate Recheck succeeds")
	}
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeRevisionConflict {
		t.Fatalf("stale selected provider recheck error=%v", err)
	}
}

func TestResourcePreparation_OfflineReinspectsHydratedProvidersForGeoClosure(t *testing.T) {
	ctx := context.Background()
	input := rootPolicyInput()
	input.YAML = []byte("rule-providers:\n  source: {type: http, behavior: classical, url: 'https://example.test/rules'}\nrules: ['RULE-SET,source,DIRECT']\n")
	fs := newMemoryProviderFiles()
	store := &ProviderStore{files: fs}
	req, err := NewRootConfigPolicy().Inspect(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	spec := req.Providers[0]
	target, err := providerTarget(spec)
	if err != nil {
		t.Fatal(err)
	}
	if err = fs.write(ctx, target, []byte("payload: ['GEOSITE,CN']\n"), providerObject{}); err != nil {
		t.Fatal(err)
	}
	geoName, err := GeoResourcePath(GeoSiteDAT)
	if err != nil {
		t.Fatal(err)
	}
	geoPath := "runtime/core-home/" + geoName
	if err = fs.write(ctx, geoPath, geoSiteFixture(), providerObject{}); err != nil {
		t.Fatal(err)
	}
	providerSource, _, err := store.captureResource(ctx, target, maxDocumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	geoSource, _, err := store.captureResource(ctx, geoPath, maxGeoResourceBytes)
	if err != nil {
		t.Fatal(err)
	}
	geoID, err := GeoResourceID(GeoSiteDAT)
	if err != nil {
		t.Fatal(err)
	}
	graph := &ResourceGraph{store: store, sources: map[string]resourceSource{spec.ResourceID: providerSource, geoID: geoSource}}
	preparer := NewResourcePreparer(store, nil, nil)
	preparer.catalog = func(GeoResourceKind) (geoArtifact, error) {
		return geoArtifact{hash: providerDigest(geoSiteFixture()), size: int64(len(geoSiteFixture()))}, nil
	}
	if _, err = preparer.OfflineInput(ctx, input, graph); err != nil {
		t.Fatalf("complete valid offline provider+Geo graph rejected: %v", err)
	}
}

func TestResourcePreparation_InactiveCommitPersistsMissingHydratedGeo(t *testing.T) {
	ctx := context.Background()
	input := rootPolicyInput()
	input.YAML = []byte("rule-providers:\n  source: {type: http, behavior: classical, url: 'https://example.test/rules'}\nrules: ['RULE-SET,source,DIRECT']\n")
	fs := newMemoryProviderFiles()
	store := &ProviderStore{files: fs}
	preparer := NewResourcePreparer(store, providerDownloadFunc(func(context.Context, ProviderSpec, string) ([]byte, error) {
		return []byte("payload: ['GEOSITE,CN']\n"), nil
	}), geoDownloadFunc(func(_ context.Context, kind GeoResourceKind, _ string) ([]byte, error) {
		if kind != GeoSiteDAT {
			t.Fatalf("downloaded Geo kind=%q", kind)
		}
		return geoSiteFixture(), nil
	}))
	preparer.catalog = func(GeoResourceKind) (geoArtifact, error) {
		return geoArtifact{hash: providerDigest(geoSiteFixture()), size: int64(len(geoSiteFixture()))}, nil
	}
	prepared, err := preparer.Prepare(ctx, input, ProxyModeDirect, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prepared.Close(ctx) }()
	geoName, err := GeoResourcePath(GeoSiteDAT)
	if err != nil {
		t.Fatal(err)
	}
	geoPath := "runtime/core-home/" + geoName
	if _, ok := fs.objects[geoPath]; ok {
		t.Fatal("Geo published before inactive persist")
	}
	if _, ok := fs.objects["runtime/config.yaml"]; ok {
		t.Fatal("live configuration published before inactive persist")
	}
	if err = prepared.CommitCachedOutputs(ctx); err != nil {
		t.Fatalf("inactive persist of hydrated Geo kind: %v", err)
	}
	if got := fs.objects[geoPath]; string(got) != string(geoSiteFixture()) {
		t.Fatal("missing Geo kind required after provider hydration was not persisted")
	}
	if _, ok := fs.objects["runtime/config.yaml"]; ok {
		t.Fatal("inactive persist published live configuration")
	}
	if err = store.Recover(ctx); err != nil {
		t.Fatalf("recover after Geo persist: %v", err)
	}
	if got := fs.objects[geoPath]; string(got) != string(geoSiteFixture()) {
		t.Fatal("recovery dropped persisted Geo")
	}
}
