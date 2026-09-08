package subscription

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/onboarding"
	"go.yaml.in/yaml/v3"
)

// State roles are deliberately finite. The journal never supplies filesystem
// paths, nor does a participant lend a callback with independent write authority.
type resourceStateRole string

const (
	resourceSettings    resourceStateRole = "settings"
	resourceCatalog     resourceStateRole = "catalog"
	resourceSourceCache resourceStateRole = "source-cache"
	resourceOnboarding  resourceStateRole = "onboarding"
)

func stateTarget(role resourceStateRole, id string) (string, error) {
	if role == resourceSourceCache && profileIDPattern.MatchString(id) {
		return "subscriptions/cache/" + id + ".yaml", nil
	}
	if id != "" {
		return "", dataError("invalid activation state identity")
	}
	switch role {
	case resourceSettings:
		return "mihari.yaml", nil
	case resourceCatalog:
		return "subscriptions/catalog.yaml", nil
	case resourceOnboarding:
		return "onboarding.json", nil
	default:
		return "", dataError("invalid activation state role")
	}
}

func (p *PreparedResources) checkStateBinding(path string, role resourceStateRole, id string) error {
	if p == nil || p.store == nil {
		return dataError("resource preparation unavailable")
	}
	root, identity := p.store.files.storeBinding()
	relative, err := stateTarget(role, id)
	if err != nil {
		return err
	}
	if root == "" || identity == "" || filepath.Clean(path) != filepath.Join(root, filepath.FromSlash(relative)) {
		return dataError("activation state belongs to a different data root")
	}
	return nil
}

func (p *PreparedResources) stageState(ctx context.Context, role resourceStateRole, id string, content []byte) error {
	target, err := stateTarget(role, id)
	if err != nil {
		return err
	}
	for _, entry := range p.state {
		if entry.stateRole == role {
			return dataError("duplicate activation state role")
		}
	}
	limit := 1 << 20
	if role == resourceSourceCache {
		limit = 32 << 20
	}
	if len(content) > limit {
		return dataError("activation state exceeds size limit")
	}
	candidate, err := p.store.prepareBytes(ctx, ProviderSpec{SubscriptionID: id}, "", target, content)
	if err != nil {
		return err
	}
	candidate.stateRole = role
	p.state = append(p.state, candidate)
	return nil
}

func (p *PreparedResources) stageObservedState(ctx context.Context, role resourceStateRole, id string, content []byte, old providerObject) error {
	if err := p.stageState(ctx, role, id, content); err != nil {
		return err
	}
	if p.state[len(p.state)-1].old != old {
		return providerConflict()
	}
	return nil
}

func (p *PreparedResources) readState(ctx context.Context, target string, limit int64) ([]byte, providerObject, error) {
	old, err := p.store.files.inspect(ctx, target)
	if err != nil {
		return nil, old, err
	}
	raw, err := p.store.files.read(ctx, target, limit)
	if err != nil {
		return nil, old, err
	}
	if !old.Present || providerDigest(raw) != old.SHA256 {
		return nil, old, providerConflict()
	}
	return raw, old, nil
}

// StageSettings binds the exact pre-update settings and candidate to this data
// root. A stale settings candidate cannot be resealed against newer disk state.
func (p *PreparedResources) StageSettings(ctx context.Context, path string, before, after config.Settings) error {
	if err := p.checkStateBinding(path, resourceSettings, ""); err != nil {
		return err
	}
	if err := after.Validate(); err != nil {
		return err
	}
	if after.ControllerSecret == "" {
		return dataError("controller secret is required")
	}
	raw, old, err := p.readState(ctx, "mihari.yaml", 1<<20)
	if err != nil {
		return err
	}
	var disk config.Settings
	if err = yaml.Unmarshal(raw, &disk); err != nil {
		return dataError("invalid activation settings")
	}
	disk.SetLogging(disk.EffectiveLogging())
	before = before.Clone()
	before.SetLogging(before.EffectiveLogging())
	if !reflect.DeepEqual(disk, before) {
		return providerConflict()
	}
	after = after.Clone()
	after.SetLogging(after.EffectiveLogging())
	content, err := yaml.Marshal(after)
	if err != nil {
		return dataError("encode activation settings")
	}
	return p.stageObservedState(ctx, resourceSettings, "", content, old)
}

// CatalogActivation carries only a service-owned before/after catalog and the
// staged data-root participants. Publish updates memory after WAL completion.
type CatalogActivation struct {
	service       *Service
	before, after Catalog
}

// Recheck rejects changes to the captured catalog before durable completion.
func (a *CatalogActivation) Recheck() error {
	if a == nil || a.service == nil {
		return dataError("activation catalog unavailable")
	}
	if !reflect.DeepEqual(a.service.Snapshot(), a.before) {
		return providerConflict()
	}
	return nil
}

// Publish installs the committed catalog in memory without another disk write.
func (a *CatalogActivation) Publish() Catalog {
	a.service.mu.Lock()
	defer a.service.mu.Unlock()
	a.service.catalog = a.after.Clone()
	a.service.activation = nil
	return a.after.Clone()
}

// Cancel releases the participant reservation only after the shared WAL has
// restored disk, or before that WAL began. It never performs its own writes.
func (a *CatalogActivation) Cancel() {
	if a == nil || a.service == nil {
		return
	}
	a.service.mu.Lock()
	defer a.service.mu.Unlock()
	if a.service.activation == a {
		a.service.activation = nil
	}
}

func catalogActivationBusy() error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: "catalog resource activation in progress"}
}

func (s *Service) stageCatalog(ctx context.Context, resources *PreparedResources, before, after Catalog) (*CatalogActivation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activation != nil {
		return nil, catalogActivationBusy()
	}
	if !reflect.DeepEqual(s.catalog, before) {
		return nil, providerConflict()
	}
	if err := resources.checkStateBinding(s.catalogPath, resourceCatalog, ""); err != nil {
		return nil, err
	}
	raw, old, err := resources.readState(ctx, "subscriptions/catalog.yaml", maxCatalogSize)
	if err != nil {
		return nil, err
	}
	var disk Catalog
	if err = yaml.Unmarshal(raw, &disk); err != nil {
		return nil, dataError("invalid activation catalog")
	}
	if err = disk.Normalize(); err != nil {
		return nil, err
	}
	disk.fillDefaults()
	if !reflect.DeepEqual(disk, before) {
		return nil, providerConflict()
	}
	if err = after.Normalize(); err != nil {
		return nil, err
	}
	after.fillDefaults()
	content, err := yaml.Marshal(after)
	if err != nil {
		return nil, dataError("encode activation catalog")
	}
	if err = resources.stageObservedState(ctx, resourceCatalog, "", content, old); err != nil {
		return nil, err
	}
	activation := &CatalogActivation{service: s, before: before, after: after}
	s.activation = activation
	return activation, nil
}

// StageUse prepares catalog activation without publishing disk or memory.
func (s *Service) StageUse(ctx context.Context, resources *PreparedResources, id string, generation uint64) (*CatalogActivation, error) {
	before := s.Snapshot()
	after := before.Clone()
	index := after.Index(id)
	if index < 0 || !after.Profiles[index].Enabled || generation == 0 || after.Profiles[index].Generation != generation {
		return nil, providerConflict()
	}
	after.ActiveID = id
	return s.stageCatalog(ctx, resources, before, after)
}

// StageRefresh seals the source cache and catalog into the same resource WAL.
func (s *Service) StageRefresh(ctx context.Context, resources *PreparedResources, prepared PreparedRefresh, id string, generation uint64) (*CatalogActivation, error) {
	before := s.Snapshot()
	after := before.Clone()
	index := after.Index(prepared.profileID)
	if index < 0 || prepared.profileID != id || after.Profiles[index].Version != prepared.profileVersion {
		return nil, providerConflict()
	}
	profile := &after.Profiles[index]
	if !prepared.result.NotModified {
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
		profile.Upload, profile.Download, profile.Total, profile.Expire = info.Upload, info.Download, info.Total, info.Expire
	}
	if err := after.Normalize(); err != nil {
		return nil, err
	}
	after.fillDefaults()
	if after.ActiveID != id || profile.Generation != generation {
		return nil, providerConflict()
	}
	if err := resources.checkStateBinding(s.CachePath(id), resourceSourceCache, id); err != nil {
		return nil, err
	}
	if !prepared.result.NotModified {
		if err := resources.stageState(ctx, resourceSourceCache, id, prepared.result.Content); err != nil {
			return nil, err
		}
	}
	return s.stageCatalog(ctx, resources, before, after)
}

// StageOnboarding includes the service-owned completion update in the WAL.
func (p *PreparedResources) StageOnboarding(ctx context.Context, update *onboarding.PreparedUpdate) error {
	path, content, err := update.ActivationData()
	if err != nil {
		return err
	}
	if err = p.checkStateBinding(path, resourceOnboarding, ""); err != nil {
		return err
	}
	raw, old, err := p.readState(ctx, "onboarding.json", 64<<10)
	if err != nil {
		return err
	}
	if err = update.CheckPrevious(raw); err != nil {
		return err
	}
	return p.stageObservedState(ctx, resourceOnboarding, "", content, old)
}

// RecoverState settles the whole WAL before stores load. A pending settings
// participant invalidates any settings value loaded by an upstream caller.
func (p *ResourcePreparer) RecoverState(ctx context.Context) (*config.Settings, error) {
	if p == nil || p.store == nil {
		return nil, dataError("resource preparer unavailable")
	}
	journal, err := p.store.loadResourceJournal(ctx)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	reload := false
	for _, entry := range journal.Entries {
		reload = reload || entry.StateRole == resourceSettings
	}
	if err = p.Recover(ctx); err != nil {
		return nil, err
	}
	if !reload {
		return nil, nil
	}
	root, _ := p.store.files.storeBinding()
	settings, err := config.Load(filepath.Join(root, "mihari.yaml"))
	if err != nil {
		return nil, err
	}
	return &settings, nil
}
