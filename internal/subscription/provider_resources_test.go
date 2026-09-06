package subscription

import (
	"context"
	"strings"
	"testing"
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
