package subscription

import (
	"context"
	"errors"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
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

// Recover converges provider and whole-resource journals before any core IO.
func (p *ResourcePreparer) Recover(ctx context.Context) error {
	if p == nil || p.store == nil {
		return dataError("resource preparer unavailable")
	}
	return p.store.Recover(ctx)
}

// SnapshotResources seals the daemon-owned current cached graph.
func (p *ResourcePreparer) SnapshotResources(ctx context.Context, input PolicyInput) (*ResourceGraph, error) {
	if p == nil || p.store == nil {
		return nil, dataError("resource preparer unavailable")
	}
	return p.store.SnapshotResources(ctx, input)
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
	store         *ProviderStore
	input         PolicyInput
	output        PolicyOutput
	providers     []*PreparedProvider
	geo           []*PreparedProvider
	configuration *PreparedProvider
	sources       map[string]resourceSource
}

// PreparedProviderRefresh owns one unpublished provider candidate after the
// complete current configuration and every retained resource were validated.
type PreparedProviderRefresh struct {
	candidate *PreparedProvider
}

// PrepareOffline reconstructs a complete resource candidate from the sealed
// current cache without performing network IO.
func (p *ResourcePreparer) OfflineInput(ctx context.Context, input PolicyInput, graph *ResourceGraph) (PolicyInput, error) {
	if p == nil || p.store == nil || graph == nil || graph.store != p.store {
		return PolicyInput{}, dataError("authorized offline resource graph unavailable")
	}
	if len(input.Resources) != 0 {
		return PolicyInput{}, dataError("resource bytes require managed preparation")
	}
	input.YAML = append([]byte(nil), input.YAML...)
	input.Settings = input.Settings.Clone()
	input.Resources = make(map[string][]byte)
	requirements, err := NewRootConfigPolicy().Inspect(ctx, input)
	if err != nil {
		return PolicyInput{}, err
	}
	var budget policyProviderBudget
	for _, spec := range requirements.Providers {
		key, _, b, err := p.currentProvider(ctx, graph, spec)
		if err != nil {
			return PolicyInput{}, err
		}
		if err = budget.source(int64(len(b))); err != nil {
			return PolicyInput{}, err
		}
		input.Resources[key] = b
	}
	if err = p.currentGeo(ctx, graph, input.Resources, make(map[string]resourceSource), requirements.Geo); err != nil {
		return PolicyInput{}, err
	}
	if _, err = NewRootConfigPolicy().Build(ctx, input); err != nil {
		return PolicyInput{}, err
	}
	return input, nil
}

func (p *ResourcePreparer) PrepareOffline(ctx context.Context, input PolicyInput, graph *ResourceGraph) (*PreparedResources, error) {
	if p == nil || p.store == nil || graph == nil || graph.store != p.store {
		return nil, dataError("authorized offline resource graph unavailable")
	}
	if len(input.Resources) != 0 {
		return nil, dataError("resource bytes require managed preparation")
	}
	hydrated, err := p.OfflineInput(ctx, input, graph)
	if err != nil {
		return nil, err
	}
	input = hydrated
	sources := make(map[string]resourceSource)
	requirements, err := NewRootConfigPolicy().Inspect(ctx, input)
	if err != nil {
		return nil, err
	}
	for _, spec := range requirements.Providers {
		_, source, _, err := p.currentProvider(ctx, graph, spec)
		if err != nil {
			return nil, err
		}
		sources[source.path] = source
	}
	if err = p.currentGeo(ctx, graph, make(map[string][]byte), sources, requirements.Geo); err != nil {
		return nil, err
	}
	return p.stageComplete(ctx, input, sources)
}

// PrepareProviderRefresh refreshes one registered rule provider while using
// the sealed graph for every retained provider and Geo resource.
func (p *ResourcePreparer) PrepareProviderRefresh(ctx context.Context, input PolicyInput, mode string, graph *ResourceGraph, name string) (*PreparedProviderRefresh, error) {
	return p.PrepareProvider(ctx, input, mode, graph, "rule", name)
}

// PrepareProvider prepares one proxy or rule provider while validating the
// complete retained resource graph.
func (p *ResourcePreparer) PrepareProvider(ctx context.Context, input PolicyInput, mode string, graph *ResourceGraph, kind, name string) (*PreparedProviderRefresh, error) {
	if p == nil || p.store == nil || graph == nil || graph.store != p.store {
		return nil, dataError("authorized provider resource graph unavailable")
	}
	if len(input.Resources) != 0 {
		return nil, dataError("resource bytes require managed preparation")
	}
	input.YAML = append([]byte(nil), input.YAML...)
	input.Settings = input.Settings.Clone()
	input.Resources = make(map[string][]byte)
	requirements, err := NewRootConfigPolicy().Inspect(ctx, input)
	if err != nil {
		return nil, err
	}
	selected := -1
	for i := range requirements.Providers {
		if requirements.Providers[i].Kind == kind && requirements.Providers[i].Name == name {
			if selected >= 0 {
				return nil, dataError("duplicate rule provider name")
			}
			selected = i
		}
	}
	if selected < 0 {
		return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "provider is not managed"}
	}
	sources := make(map[string]resourceSource)
	var budget policyProviderBudget
	for i, spec := range requirements.Providers {
		key := spec.ResourceID
		var b []byte
		if i == selected && spec.URL != "" {
			if p.providers == nil {
				return nil, dataError("provider downloader unavailable")
			}
			b, err = p.providers.Download(ctx, spec, mode)
		} else {
			var source resourceSource
			key, source, b, err = p.currentProvider(ctx, graph, spec)
			if err == nil {
				sources[source.path] = source
			}
		}
		if err != nil {
			return nil, err
		}
		if err = budget.source(int64(len(b))); err != nil {
			return nil, err
		}
		input.Resources[key] = b
	}
	if err = p.currentGeo(ctx, graph, input.Resources, sources, requirements.Geo); err != nil {
		return nil, err
	}
	output, err := NewRootConfigPolicy().Build(ctx, input)
	if err != nil {
		return nil, err
	}
	var selectedSpec *ProviderSpec
	for i := range output.Providers {
		if output.Providers[i].Kind == kind && output.Providers[i].Name == name {
			selectedSpec = &output.Providers[i]
			break
		}
	}
	if selectedSpec == nil {
		return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "provider is not managed"}
	}
	candidate, err := p.store.Prepare(ctx, *selectedSpec)
	if err != nil {
		return nil, err
	}
	candidate.required = sources
	return &PreparedProviderRefresh{candidate: candidate}, nil
}

func (p *PreparedProviderRefresh) Recheck(ctx context.Context) error {
	if p == nil || p.candidate == nil {
		return dataError("provider refresh preparation is unavailable")
	}
	return p.candidate.recheck(ctx)
}

func (p *PreparedProviderRefresh) Commit(ctx context.Context, reload func(context.Context) error) error {
	if p == nil || p.candidate == nil {
		return dataError("provider refresh preparation is unavailable")
	}
	return p.candidate.Commit(ctx, reload)
}

func (p *PreparedProviderRefresh) Close(ctx context.Context) error {
	if p == nil || p.candidate == nil {
		return nil
	}
	return p.candidate.Close(ctx)
}

func (p *ResourcePreparer) currentProvider(ctx context.Context, graph *ResourceGraph, spec ProviderSpec) (string, resourceSource, []byte, error) {
	authorized, ok := graph.sources[spec.ResourceID]
	if !ok {
		return "", resourceSource{}, nil, dataError("required provider cache is unavailable")
	}
	source, b, err := p.store.captureResource(ctx, authorized.path, maxDocumentBytes)
	if err != nil {
		return "", resourceSource{}, nil, err
	}
	if source != authorized {
		return "", resourceSource{}, nil, providerConflict()
	}
	key := spec.ResourceID
	if spec.SourceResourceID != "" {
		key = spec.SourceResourceID
	}
	return key, source, b, nil
}

func (p *ResourcePreparer) currentGeo(ctx context.Context, graph *ResourceGraph, resources map[string][]byte, sources map[string]resourceSource, kinds []GeoResourceKind) error {
	var budget policyGeoBudget
	for _, kind := range kinds {
		name, err := GeoResourcePath(kind)
		if err != nil {
			return err
		}
		path := "runtime/core-home/" + name
		id, err := GeoResourceID(kind)
		if err != nil {
			return err
		}
		authorized, ok := graph.sources[id]
		if !ok {
			return dataError("required Geo resource cache is unavailable")
		}
		source, b, err := p.store.captureResource(ctx, path, maxGeoResourceBytes)
		if err != nil {
			return err
		}
		if source != authorized {
			return providerConflict()
		}
		artifact, err := p.catalog(kind)
		if err != nil || !artifact.matches(b) {
			return dataError("untrusted Geo resource")
		}
		if err = budget.add(int64(len(b))); err != nil {
			return err
		}
		resources[id] = b
		sources[id] = source
	}
	return nil
}

func (p *ResourcePreparer) stageComplete(ctx context.Context, input PolicyInput, sources map[string]resourceSource) (result *PreparedResources, err error) {
	output, err := NewRootConfigPolicy().Build(ctx, input)
	if err != nil {
		return nil, err
	}
	result = &PreparedResources{store: p.store, input: input, output: output, sources: sources}
	defer func() {
		if err != nil {
			err = errors.Join(err, result.Close(context.WithoutCancel(ctx)))
			result = nil
		}
	}()
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
	result.configuration, err = p.store.prepareBytes(ctx, ProviderSpec{}, "", "runtime/config.yaml", output.YAML)
	if err != nil {
		return result, err
	}
	result.configuration.configuration = true
	return result, nil
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
		source, b, err := s.captureResource(ctx, "runtime/core-home/"+name, maxGeoResourceBytes)
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
		graph.sources[id] = source
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
	result.configuration, err = p.store.prepareBytes(ctx, ProviderSpec{}, "", "runtime/config.yaml", output.YAML)
	if err != nil {
		return result, err
	}
	result.configuration.configuration = true
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

// Identity returns the immutable subscription identity without copying the
// potentially large prepared resource byte graph.
func (p *PreparedResources) Identity() (string, uint64) {
	if p == nil {
		return "", 0
	}
	return p.input.SubscriptionID, p.input.Generation
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
	if p.configuration != nil {
		return p.configuration.recheck(ctx)
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
	err = errors.Join(err, p.configuration.Close(ctx))
	return err
}
