package onboarding

import "encoding/json"

// PreparedUpdate seals an onboarding service's completion change for the
// resource activation WAL. It contains no filesystem mutation callback.
type PreparedUpdate struct {
	service       *Service
	before, after State
}

// PrepareActivation captures the service state without publishing an update.
func (s *Service) PrepareActivation(before State, complete *bool) (*PreparedUpdate, error) {
	if s.State() != before {
		return nil, dataError("onboarding changed during activation")
	}
	after := before
	if complete != nil {
		after.Complete = *complete
	}
	return &PreparedUpdate{service: s, before: before, after: after}, nil
}

// ActivationData returns only the service-owned target and canonical bytes.
func (p *PreparedUpdate) ActivationData() (string, []byte, error) {
	if p == nil || p.service == nil {
		return "", nil, dataError("onboarding activation unavailable")
	}
	raw, err := json.Marshal(persistedState{Schema: stateSchema, Complete: p.after.Complete})
	return p.service.statePath, append(raw, '\n'), err
}

// Recheck confirms that the staged update is still current before commit.
func (p *PreparedUpdate) Recheck() error {
	if p.service.State() != p.before {
		return dataError("onboarding changed during activation")
	}
	return nil
}

// CheckPrevious verifies the service snapshot against the exact disk object
// captured by the activation owner before staging its replacement.
func (p *PreparedUpdate) CheckPrevious(raw []byte) error {
	var previous persistedState
	if err := json.Unmarshal(raw, &previous); err != nil || previous.Schema != stateSchema || previous.Complete != p.before.Complete {
		return dataError("onboarding changed during activation")
	}
	return nil
}

// Publish updates memory only after the shared resource WAL commits disk.
func (p *PreparedUpdate) Publish() {
	p.service.mu.Lock()
	defer p.service.mu.Unlock()
	p.service.state = p.after
}
