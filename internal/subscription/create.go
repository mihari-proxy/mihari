package subscription

import "context"

// PreparedAdd holds a fetched and parsed new source without publishing it.
type PreparedAdd struct {
	profile Profile
	refresh PreparedRefresh
}

func (p PreparedAdd) Document() Document { return p.refresh.document }
func (p PreparedAdd) ProfileID() string  { return p.profile.ID }

// PrepareAdd validates and fetches before any catalog or cache is written.
func (s *Service) PrepareAdd(ctx context.Context, name, source, mode string) (PreparedAdd, error) {
	id, err := newProfileID()
	if err != nil {
		return PreparedAdd{}, err
	}
	p := Profile{ID: id, Name: name, URL: source, ProxyMode: mode, Enabled: true, AutoRefresh: true, Version: 1}
	c := Defaults()
	c.Profiles = []Profile{p}
	if err := c.Normalize(); err != nil {
		return PreparedAdd{}, err
	}
	p = c.Profiles[0]
	result, err := s.fetch(ctx, p)
	if err != nil {
		return PreparedAdd{}, err
	}
	if result.NotModified {
		return PreparedAdd{}, dataError("new subscription returned not-modified without a cache")
	}
	document, err := ParseDocument(result.Content)
	if err != nil {
		return PreparedAdd{}, err
	}
	ResolveFileReferences(document, result.BaseDir)
	return PreparedAdd{profile: p, refresh: PreparedRefresh{profileID: id, profileVersion: 1, result: result, document: document}}, nil
}

// CommitAdd publishes a prepared source and its first cache in one receipt.
func (s *Service) CommitAdd(prepared PreparedAdd) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.catalog.Clone()
	s.catalog.Profiles = append(s.catalog.Profiles, prepared.profile)
	receipt, err := s.commitRefreshLocked(prepared.refresh)
	if err != nil {
		s.catalog = before
		return Receipt{}, err
	}
	receipt.Before = before
	return receipt, nil
}
