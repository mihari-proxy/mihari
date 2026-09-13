package config

import (
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"unicode/utf8"
)

// RoutingSettings stores global routing intent and subscription-scoped exits.
type RoutingSettings struct {
	Mode             string            `yaml:"mode"`
	GlobalSelections map[string]string `yaml:"global-selections,omitempty"`
	BootstrapGlobal  string            `yaml:"bootstrap-global,omitempty"`
}

// RoutingMode returns the effective daemon-owned mode, defaulting to rule.
func (s Settings) RoutingMode() string {
	if s.Routing == nil || s.Routing.Mode == "" {
		return "rule"
	}
	return s.Routing.Mode
}

// GlobalSelection returns the exit saved for one subscription (empty means bootstrap).
func (s Settings) GlobalSelection(id string) string {
	if s.Routing == nil {
		return ""
	}
	if id == "" {
		return s.Routing.BootstrapGlobal
	}
	return s.Routing.GlobalSelections[id]
}

// SetRoutingMode records mode without changing saved exits.
func (s *Settings) SetRoutingMode(mode string) {
	if s.Routing == nil {
		s.Routing = &RoutingSettings{}
	}
	s.Routing.Mode = mode
}

// SetGlobalSelection records an exit without changing the routing mode.
func (s *Settings) SetGlobalSelection(id, name string) {
	if s.Routing == nil {
		s.Routing = &RoutingSettings{Mode: s.RoutingMode()}
	}
	if id == "" {
		s.Routing.BootstrapGlobal = name
		return
	}
	if name == "" {
		delete(s.Routing.GlobalSelections, id)
		return
	}
	if s.Routing.GlobalSelections == nil {
		s.Routing.GlobalSelections = make(map[string]string)
	}
	s.Routing.GlobalSelections[id] = name
}

func validateRouting(r *RoutingSettings) error {
	if r == nil {
		return nil
	}
	if r.Mode != "" && !protocol.ValidRoutingMode(r.Mode) {
		return dataError("invalid routing mode")
	}
	validName := func(name string) bool { return len(name) <= 4096 && utf8.ValidString(name) }
	if !validName(r.BootstrapGlobal) || len(r.GlobalSelections) > 4096 {
		return dataError("invalid GLOBAL selections")
	}
	for id, name := range r.GlobalSelections {
		if id == "" || len(id) > 256 || !utf8.ValidString(id) || !validName(name) {
			return dataError("invalid GLOBAL selection")
		}
	}
	return nil
}
