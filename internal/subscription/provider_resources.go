package subscription

import (
	"context"
	"errors"
)

// ResourcePreparer closes the provider and Geo graph before producing config.
// Constructors cannot replace the compiled Geo catalog or source authorization.
type ResourcePreparer struct {
	store     *ProviderStore
	providers ProviderDownloader
	geo       GeoDownloader
	catalog   func(GeoResourceKind) (geoArtifact, error)
}

// GeoDownloader retrieves one compiled kind; the preparer independently verifies its digest.
type GeoDownloader interface {
	DownloadGeo(context.Context, GeoResourceKind, string) ([]byte, error)
}

// NewResourcePreparer creates the managed resource boundary. Callers retain store lifetime.
func NewResourcePreparer(store *ProviderStore, providers ProviderDownloader, geo GeoDownloader) *ResourcePreparer {
	return &ResourcePreparer{store: store, providers: providers, geo: geo, catalog: trustedGeoArtifact}
}

// ResourceGraph is an immutable authorization snapshot of the current private resource graph.
// It is made from daemon-owned source definitions, never from a public candidate.
type ResourceGraph struct {
	store   *ProviderStore
	sources map[string]resourceSource
}
type resourceSource struct {
	path   string
	object providerObject
}

// PreparedResources owns unpublished private candidates and their complete policy result.
type PreparedResources struct {
	store     *ProviderStore
	input     PolicyInput
	output    PolicyOutput
	providers []*PreparedProvider
	geo       []*PreparedProvider
	sources   map[string]resourceSource
}

// SnapshotResources validates current daemon-owned cached definitions and their
// exact target files. Source IDs in a new candidate confer no authority; only
// this sealed graph can authorize reuse. The caller owns the current config
// snapshot and must recheck its subscription identity/generation at commit.
func (s *ProviderStore) SnapshotResources(ctx context.Context, current PolicyInput) (*ResourceGraph, error) {
	if s == nil || s.files == nil {
		return nil, dataError("resource store unavailable")
	}
	current.Resources = nil
	requirements, err := NewRootConfigPolicy().Inspect(ctx, current)
	if err != nil {
		return nil, err
	}
	current.Resources = make(map[string][]byte)
	graph := &ResourceGraph{store: s, sources: make(map[string]resourceSource)}
	for _, spec := range requirements.Providers {
		path, err := providerTarget(spec)
		if err != nil {
			return nil, err
		}
		source, b, err := s.captureResource(ctx, path, maxDocumentBytes)
		if err != nil {
			return nil, err
		}
		key := spec.ResourceID
		if spec.SourceResourceID != "" {
			key = spec.SourceResourceID
		}
		current.Resources[key] = b
		graph.sources[spec.ResourceID] = source
	}
	requirements, err = NewRootConfigPolicy().Inspect(ctx, current)
	if err != nil {
		return nil, err
	}
	for _, kind := range requirements.Geo {
		name, err := GeoResourcePath(kind)
		if err != nil {
			return nil, err
		}
		_, b, err := s.captureResource(ctx, "runtime/core-home/"+name, maxGeoResourceBytes)
		if err != nil {
			return nil, err
		}
		a, err := trustedGeoArtifact(kind)
		if err != nil || !a.matches(b) {
			return nil, dataError("untrusted Geo resource")
		}
		id, err := GeoResourceID(kind)
		if err != nil {
			return nil, err
		}
		current.Resources[id] = b
	}
	if _, err = NewRootConfigPolicy().Build(ctx, current); err != nil {
		return nil, err
	}
	return graph, nil
}
func (s *ProviderStore) captureResource(ctx context.Context, path string, limit int64) (resourceSource, []byte, error) {
	before, err := s.files.inspect(ctx, path)
	if err != nil {
		return resourceSource{}, nil, err
	}
	if !before.Present {
		return resourceSource{}, nil, dataError("required resource unavailable")
	}
	b, err := s.files.read(ctx, path, limit)
	if err != nil {
		return resourceSource{}, nil, err
	}
	after, err := s.files.inspect(ctx, path)
	if err != nil {
		return resourceSource{}, nil, err
	}
	if after != before || providerDigest(b) != before.SHA256 {
		return resourceSource{}, nil, providerConflict()
	}
	return resourceSource{path: path, object: before}, b, nil
}

// Prepare resolves a candidate through Inspect, managed fetch/import, Geo closure
// and Build outside Manager mutation. Supplied PolicyInput.Resources is rejected:
// source authentication cannot be replaced by caller-provided byte maps.
func (p *ResourcePreparer) Prepare(ctx context.Context, input PolicyInput, mode string, graph *ResourceGraph) (result *PreparedResources, err error) {
	if p == nil || p.store == nil || p.store.files == nil {
		return nil, dataError("resource preparer unavailable")
	}
	if len(input.Resources) != 0 {
		return nil, dataError("resource bytes require managed preparation")
	}
	if graph != nil && graph.store != p.store {
		return nil, dataError("resource graph belongs to another store")
	}
	input.YAML = append([]byte(nil), input.YAML...)
	input.Settings = input.Settings.Clone()
	input.Resources = make(map[string][]byte)
	result = &PreparedResources{store: p.store, sources: make(map[string]resourceSource)}
	var rawTransactions []string
	defer func() {
		for _, tx := range rawTransactions {
			err = errors.Join(err, p.store.cleanupTransaction(context.WithoutCancel(ctx), tx))
		}
		if err != nil {
			err = errors.Join(err, result.Close(context.WithoutCancel(ctx)))
			result = nil
		}
	}()
	requirements, err := NewRootConfigPolicy().Inspect(ctx, input)
	if err != nil {
		return result, err
	}
	var budget policyProviderBudget
	for _, spec := range requirements.Providers {
		if e := ctx.Err(); e != nil {
			return result, e
		}
		var b []byte
		key := spec.ResourceID
		switch {
		case spec.SourceResourceID != "":
			key = spec.SourceResourceID
			if graph == nil {
				return result, dataError("provider source is not authorized")
			}
			authorized, ok := graph.sources[key]
			if !ok {
				return result, dataError("provider source is not authorized")
			}
			source, bytes, e := p.store.captureResource(ctx, authorized.path, maxDocumentBytes)
			if e != nil {
				return result, e
			}
			if source != authorized {
				return result, providerConflict()
			}
			result.sources[key] = source
			b = bytes
		case spec.URL != "":
			if p.providers == nil {
				return result, dataError("provider downloader unavailable")
			}
			b, err = p.providers.Download(ctx, spec, mode)
			if err != nil {
				return result, err
			}
		default:
			b = spec.Inline
		}
		if err = budget.source(int64(len(b))); err != nil {
			return result, err
		}
		// Inline policy bytes are already derived from the authoritative definition;
		// supplying them again would incorrectly reinterpret text as YAML.
		if spec.URL != "" || spec.SourceResourceID != "" {
			tx, raw, e := p.store.stageSource(ctx, b, maxDocumentBytes)
			if e != nil {
				return result, e
			}
			rawTransactions = append(rawTransactions, tx)
			input.Resources[key] = raw
		}
	}
	requirements, err = NewRootConfigPolicy().Inspect(ctx, input)
	if err != nil {
		return result, err
	}
	var geoBudget policyGeoBudget
	for _, kind := range requirements.Geo {
		a, e := p.catalog(kind)
		if e != nil {
			return result, e
		}
		name, e := GeoResourcePath(kind)
		if e != nil {
			return result, e
		}
		path := "runtime/core-home/" + name
		source, b, e := p.store.captureResource(ctx, path, maxGeoResourceBytes)
		if e == nil && a.matches(b) {
			result.sources["geo:"+string(kind)] = source
		} else {
			// Existing invalid bytes are never accepted as an offline cache. Network
			// preparation may repair them; its old-object snapshot is retained below.
			if p.geo == nil {
				return result, dataError("required trusted Geo resource unavailable")
			}
			b, e = p.geo.DownloadGeo(ctx, kind, mode)
			if e != nil {
				return result, e
			}
		}
		if !a.matches(b) {
			return result, dataError("untrusted Geo artifact")
		}
		tx, raw, e := p.store.stageSource(ctx, b, maxGeoResourceBytes)
		if e != nil {
			return result, e
		}
		rawTransactions = append(rawTransactions, tx)
		b = raw
		if e = geoBudget.add(int64(len(b))); e != nil {
			return result, e
		}
		id, e := GeoResourceID(kind)
		if e != nil {
			return result, e
		}
		input.Resources[id] = append([]byte(nil), b...)
	}
	output, err := NewRootConfigPolicy().Build(ctx, input)
	if err != nil {
		return result, err
	}
	for _, spec := range output.Providers {
		staged, e := p.store.Prepare(ctx, spec)
		if e != nil {
			return result, e
		}
		result.providers = append(result.providers, staged)
	}
	for _, geo := range output.Geo {
		staged, e := p.store.prepareGeo(ctx, geo)
		if e != nil {
			return result, e
		}
		result.geo = append(result.geo, staged)
	}
	result.input = input
	result.output = output
	return result, nil
}

func (s *ProviderStore) stageSource(ctx context.Context, b []byte, limit int64) (tx string, raw []byte, err error) {
	if int64(len(b)) > limit {
		return "", nil, dataError("source resource exceeds size limit")
	}
	tx, err = newProfileID()
	if err != nil {
		return "", nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.cleanupTransaction(context.WithoutCancel(ctx), tx))
		}
	}()
	prefix := "staging/providers/" + tx + "/"
	if err = s.files.write(ctx, prefix+"transaction-id", []byte(tx), providerObject{}); err != nil {
		return tx, nil, err
	}
	if err = s.files.write(ctx, prefix+"source", b, providerObject{}); err != nil {
		return tx, nil, err
	}
	_, raw, err = s.captureResource(ctx, prefix+"source", limit)
	if err == nil && providerDigest(raw) != providerDigest(b) {
		err = providerConflict()
	}
	return tx, raw, err
}

// PolicyInput returns a deep resource snapshot for the immutable policy's final
// Manager-side Build. It conveys bytes, not write authority or mutable handles.
func (p *PreparedResources) PolicyInput() PolicyInput {
	if p == nil {
		return PolicyInput{}
	}
	input := p.input
	input.YAML = append([]byte(nil), input.YAML...)
	input.Settings = input.Settings.Clone()
	input.Resources = make(map[string][]byte, len(p.input.Resources))
	for k, b := range p.input.Resources {
		input.Resources[k] = append([]byte(nil), b...)
	}
	return input
}

// Recheck verifies all authorized source objects and privately staged targets.
// Manager must also recheck active ID/generation/configGeneration under mutation.
func (p *PreparedResources) Recheck(ctx context.Context) error {
	if p == nil || p.store == nil {
		return dataError("resource candidate unavailable")
	}
	for _, source := range p.sources {
		actual, err := p.store.files.inspect(ctx, source.path)
		if err != nil {
			return err
		}
		if actual != source.object {
			return providerConflict()
		}
	}
	for _, set := range [][]*PreparedProvider{p.providers, p.geo} {
		for _, candidate := range set {
			if err := candidate.recheck(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// Close removes only owned, unpublished candidate files. Recovery journals take
// precedence and retain their objects until recovery finishes.
func (p *PreparedResources) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var err error
	for _, set := range [][]*PreparedProvider{p.providers, p.geo} {
		for _, candidate := range set {
			err = errors.Join(err, candidate.Close(ctx))
		}
	}
	return err
}
