package ui

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
)

// StatusTone maps a discrete state onto one of the four semantic colors the
// status-shell palette already defines. Color carries meaning, not partition:
// ToneNeutral is a legitimate off/idle state (not a fault) — only "should be on
// but isn't" or an error maps to ToneNegative.
type StatusTone uint8

const (
	// ToneNeutral (Muted) — off / stopped / unknown / not installed / neutral.
	ToneNeutral StatusTone = iota
	// TonePositive (Success) — running / enabled / ready / healthy / connected.
	TonePositive
	// ToneCaution (Warning) — pending / stale / foreign / cached / needs attention.
	ToneCaution
	// ToneNegative (Danger) — failed / error / offline / unhealthy / fault.
	ToneNegative
)

const statusDotGlyph = "●" // U+25CF, solid circle — matches statusbar/subscriptions dots.

// ToneStyle maps a tone onto an existing theme semantic style. It reuses the
// palette (no new colors): Positive→Success, Caution→Warning, Negative→Danger,
// default→Muted. Callers that want a tone color without a leading dot (e.g. a
// table cell where a dot would crowd the column) use this directly.
func ToneStyle(theme Theme, tone StatusTone) lipgloss.Style {
	switch tone {
	case TonePositive:
		return theme.Success
	case ToneCaution:
		return theme.Warning
	case ToneNegative:
		return theme.Danger
	default:
		return theme.Muted
	}
}

// StatusDot renders "● label": the glyph and the text share one tone color as a
// pure foreground (ANSI-safe — the trailing reset clears it before any padding).
// label is preserved verbatim (no case change, no added spaces) so assertions
// like strings.Contains(view, "running") keep holding. An empty label renders
// just the glyph, for marker-only columns.
func StatusDot(theme Theme, tone StatusTone, label string) string {
	if label == "" {
		return ToneStyle(theme, tone).Render(statusDotGlyph)
	}
	return ToneStyle(theme, tone).Render(statusDotGlyph + " " + label)
}

// ClassifyStatusTone is the single source of truth mapping a status-enum word to
// a tone. Exact word matches win first; a substring fallback keeps it forgiving
// for compound phrases ("Service running · Connected"). Explicit negative
// phrases ("not installed", "not_installed") resolve to Neutral so the
// "installed" substring inside them cannot flip them to Positive.
func ClassifyStatusTone(value string) StatusTone {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return ToneNeutral
	}
	// Exact-match the short/ambiguous enum words first.
	switch lower {
	// Positive
	case "on", "running", "ready", "ok", "healthy", "active", "live",
		"enabled", "owned", "connected", "installed", "complete", "success",
		"available", "applied", "succeeded":
		return TonePositive
	// Neutral — legitimately off / not present / not installed
	case "off", "disabled", "stopped", "inactive", "unknown", "unmanaged",
		"manual", "direct", "not installed", "not_installed",
		"not running", "not_running":
		return ToneNeutral
	// Caution — waiting / stale / partial / needs attention
	case "pending", "updating", "loading", "fetching", "applying", "working",
		"stale", "foreign", "cached", "missing", "reconnecting", "drift",
		"needs admin", "retry pending", "cache missing":
		return ToneCaution
	// Negative — failure / fault / link down
	case "failed", "error", "unhealthy", "degraded", "offline", "reject",
		"block", "disconnected":
		return ToneNegative
	}
	// Substring fallback — check distinctive failure/caution tokens before the
	// optimistic Positive ones so compounds read correctly.
	switch {
	case containsAny(lower,
		"fail", "error", "unhealthy", "degrad", "offline", "reject", "block", "disconnect"):
		return ToneNegative
	case containsAny(lower,
		"missing", "pending", "updating", "loading", "fetch", "applying", "working",
		"stale", "foreign", "cached", "reconnect", "drift"):
		return ToneCaution
	case containsAny(lower,
		"not installed", "not_installed", "disabled", "stopped", "inactive",
		"unknown", "unmanaged", "manual", "direct"):
		return ToneNeutral
	case containsAny(lower,
		"running", "ready", "healthy", "active", "enabled", "owned", "connected",
		"installed", "complete", "succeed", "available", "applied"):
		return TonePositive
	}
	return ToneNeutral
}

// containsAny reports whether s contains any of the substrings. Substring order
// is the caller's responsibility (ClassifyStatusTone orders by precedence).
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
