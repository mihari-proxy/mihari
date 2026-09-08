package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

// InstallationStatus describes installation completeness independently of service health.
type InstallationStatus struct {
	Schema       string `json:"schema"`
	Kind         string `json:"kind"`
	ServiceState string `json:"service_state"`
	StartFailed  bool   `json:"start_failed"`
	Reason       string `json:"reason"`
	ID           string `json:"id"`
}

// Validate checks the bounded status contract without interpreting future reasons.
func (s InstallationStatus) Validate() error {
	invalid := APIError{Code: CodeDataFailure, Message: "invalid installation status"}
	if s.Schema != "mihari.install-status/v1" || s.StartFailed || len(s.Reason) > 64 {
		return invalid
	}
	switch s.Kind {
	case "not_installed", "in_progress", "interrupted", "installed", "unknown", "permission_required":
	default:
		return invalid
	}
	switch s.ServiceState {
	case "running", "stopped", "not_installed", "unknown":
	default:
		return invalid
	}
	for _, ch := range s.Reason {
		if ch < 32 || ch > 126 {
			return invalid
		}
	}
	if s.ID != "" {
		if len(s.ID) != 32 {
			return invalid
		}
		for _, ch := range s.ID {
			if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
				return invalid
			}
		}
	}
	return nil
}

// UnmarshalJSON rejects ambiguous or incomplete installation observations.
func (s *InstallationStatus) UnmarshalJSON(data []byte) error {
	invalid := APIError{Code: CodeDataFailure, Message: "invalid installation status"}
	if len(data) > 4096 || !utf8.Valid(data) {
		return invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return invalid
	}
	var next InstallationStatus
	fields := map[string]any{"schema": &next.Schema, "kind": &next.Kind, "service_state": &next.ServiceState, "start_failed": &next.StartFailed, "reason": &next.Reason, "id": &next.ID}
	seen := make(map[string]bool, len(fields))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return invalid
		}
		key, ok := token.(string)
		field, exists := fields[key]
		if !ok || !exists || seen[key] {
			return invalid
		}
		seen[key] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return invalid
		}
		if err := json.Unmarshal(raw, field); err != nil {
			return invalid
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || len(seen) != len(fields) {
		return invalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return invalid
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*s = next
	return nil
}
