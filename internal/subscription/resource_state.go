package subscription

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/mihari-proxy/mihari/internal/config"
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

// RecoverState settles the whole WAL before stores load. A pending settings
// participant invalidates any settings value loaded by an upstream caller.
func (p *ProviderStore) RecoverState(ctx context.Context) (*config.Settings, error) {
	if p == nil || p.files == nil {
		return nil, dataError("resource recovery unavailable")
	}
	journal, err := p.loadResourceJournal(ctx)
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
	root, _ := p.files.storeBinding()
	settings, err := config.Load(filepath.Join(root, "mihari.yaml"))
	if err != nil {
		return nil, err
	}
	return &settings, nil
}
