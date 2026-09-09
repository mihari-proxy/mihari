package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"slices"
	"strings"
)

// ReplacementCandidate identifies verified candidate bytes independently of staging paths.
type ReplacementCandidate struct{ Version, SHA256, Channel string }

// ReplacementTarget describes one actual file to be overwritten. Never render it directly.
type ReplacementTarget struct {
	Roles                []string
	Path, FileID, SHA256 string
	Exists               bool
	Version              string
}

// ReplacementSnapshot binds actual targets and the persistent service definition.
type ReplacementSnapshot struct {
	Targets                 []ReplacementTarget
	ServiceDefinitionSHA256 string
}

// ReplacementPreview owns a normalized snapshot and its opaque confirmation identifier.
type ReplacementPreview struct {
	Candidate ReplacementCandidate
	Snapshot  ReplacementSnapshot
	Risk      ReplacementRisk
	ID        string
}

// ReplacementConsent is scoped to one replacement attempt.
type ReplacementConsent struct {
	Yes             bool
	ExpectedPreview string
	// Warn reports safe compatibility text for this call; it is never fingerprinted.
	Warn func(string) error `json:"-"`
}

// ReplacementObserver discovers only files the current operation will replace.
type ReplacementObserver func(context.Context, string) (ReplacementSnapshot, error)

// NewReplacementPreview copies and normalizes observations before hashing them.
func NewReplacementPreview(c ReplacementCandidate, s ReplacementSnapshot) (ReplacementPreview, error) {
	p := ReplacementPreview{Candidate: c, Risk: ReplacementNone}
	p.Snapshot.ServiceDefinitionSHA256 = s.ServiceDefinitionSHA256
	// Observers supply canonical paths. Preserve each distinct path binding.
	targets := append([]ReplacementTarget(nil), s.Targets...)
	slices.SortFunc(targets, func(a, b ReplacementTarget) int { return strings.Compare(a.Path, b.Path) })
	for _, target := range targets {
		target.Roles = append([]string(nil), target.Roles...)
		slices.Sort(target.Roles)
		target.Roles = slices.Compact(target.Roles)
		target.Version = normalizedReplacementVersion(target.Version)
		n := len(p.Snapshot.Targets)
		if n > 0 && p.Snapshot.Targets[n-1].Path == target.Path {
			prev := &p.Snapshot.Targets[n-1]
			if prev.FileID != target.FileID || prev.SHA256 != target.SHA256 || prev.Exists != target.Exists || prev.Version != target.Version {
				return ReplacementPreview{}, replacementChanged()
			}
			prev.Roles = append(prev.Roles, target.Roles...)
			slices.Sort(prev.Roles)
			prev.Roles = slices.Compact(prev.Roles)
			continue
		}
		p.Snapshot.Targets = append(p.Snapshot.Targets, target)
	}
	for _, target := range p.Snapshot.Targets {
		if !target.Exists {
			continue
		}
		risk := ClassifyReplacementVersion(target.Version, c.Version)
		if risk == ReplacementDowngrade || (risk == ReplacementUnknown && p.Risk == ReplacementNone) {
			p.Risk = risk
		}
	}
	// A versioned fixed struct avoids map ordering and excludes transient staging paths.
	wire := struct {
		Version   int
		Candidate ReplacementCandidate
		Snapshot  ReplacementSnapshot
	}{1, p.Candidate, p.Snapshot}
	raw, err := json.Marshal(wire)
	if err != nil {
		return ReplacementPreview{}, fmt.Errorf("encode replacement preview: %w", err)
	}
	sum := sha256.Sum256(raw)
	p.ID = hex.EncodeToString(sum[:])
	return p, nil
}

func normalizedReplacementVersion(value string) string {
	value = strings.TrimSpace(value)
	if value != "" && !strings.HasPrefix(value, "v") {
		value = "v" + value
	}
	if _, ok := parseCanonicalTag(value); !ok {
		return ""
	}
	return value
}

func validPreviewID(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, ch := range value {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

func replacementChanged() error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: "installation changed; start again"}
}

// ValidateReplacementConsent enforces the public confirmation contract.
func ValidateReplacementConsent(p ReplacementPreview, c ReplacementConsent) error {
	if c.ExpectedPreview != "" && (!c.Yes || !validPreviewID(c.ExpectedPreview)) {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "--expected-preview requires --yes and a valid preview ID"}
	}
	if c.ExpectedPreview != "" && c.ExpectedPreview != p.ID {
		return replacementChanged()
	}
	if p.Risk != ReplacementNone && !c.Yes {
		return ReplacementConfirmationError(p)
	}
	return nil
}

// RecheckReplacement rejects a changed candidate or installation.
func RecheckReplacement(p ReplacementPreview, c ReplacementCandidate, s ReplacementSnapshot) error {
	current, err := NewReplacementPreview(c, s)
	if err != nil {
		return err
	}
	if !validPreviewID(p.ID) || current.ID != p.ID {
		return replacementChanged()
	}
	return nil
}

const replacementRiskWarning = "Older Mihari versions may not support settings, subscriptions, state, or generated files written by the current version. Mihari may fail to start or load data, which can look like data loss. Downgrade is not a supported configuration migration and does not roll back disk state."

type replacementDisplayTarget struct {
	Roles   []string `json:"roles"`
	Version string   `json:"version"`
}

func displayReplacementVersion(value string) string {
	if value = normalizedReplacementVersion(value); value != "" {
		return value
	}
	return "unknown"
}

func displayReplacementTargets(p ReplacementPreview) []replacementDisplayTarget {
	targets := make([]replacementDisplayTarget, 0, len(p.Snapshot.Targets))
	for _, t := range p.Snapshot.Targets {
		if !t.Exists {
			continue
		}
		roles := make([]string, 0, len(t.Roles))
		for _, role := range t.Roles {
			switch role {
			case "binary", "path", "service", "managed":
				roles = append(roles, role)
			}
		}
		if len(roles) == 0 {
			roles = []string{"binary"}
		}
		targets = append(targets, replacementDisplayTarget{Roles: roles, Version: displayReplacementVersion(t.Version)})
	}
	return targets
}

// ReplacementWarning renders safe compatibility information, without file paths.
func ReplacementWarning(p ReplacementPreview) string {
	if p.Risk == ReplacementNone {
		return ""
	}
	var parts []string
	if p.Risk == ReplacementUnknown {
		parts = append(parts, "Mihari could not determine version compatibility for this replacement.")
	}
	parts = append(parts, replacementRiskWarning)
	for _, target := range displayReplacementTargets(p) {
		parts = append(parts, strings.Join(target.Roles, "/")+": "+target.Version+" -> "+displayReplacementVersion(p.Candidate.Version)+".")
	}
	return strings.Join(parts, " ")
}

// ReplacementConfirmationError returns the existing usage error with safe risk details.
func ReplacementConfirmationError(p ReplacementPreview) error {
	return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: ReplacementWarning(p), Details: map[string]any{
		"reason": "replacement_confirmation_required", "risk": p.Risk, "targets": displayReplacementTargets(p),
		"target_version": displayReplacementVersion(p.Candidate.Version), "preview_id": p.ID,
	}}
}
