package ui

import "github.com/mihari-proxy/mihari/internal/update"

// MihariInstalledVersion describes safe display information for one actual copy.
type MihariInstalledVersion struct {
	Role    string
	Version string
	Unknown bool
}

// MihariUpdateConfirmation contains no paths, file identities or executable data.
// It is an in-process TUI message, not a control or installation protocol DTO.
type MihariUpdateConfirmation struct {
	Installed         []MihariInstalledVersion
	TargetVersion     string
	Risk              update.ReplacementRisk
	Compatibility     string
	AfterConfirmation string
}

const (
	UpdateInstalledHeading            = "Installed"
	UpdateTargetHeading               = "Target"
	UpdateCompatibilityUnknownHeading = "Compatibility unknown"
	UpdateDowngradeHeading            = "Downgrade detected"
	UpdateAfterHeading                = "After confirmation"
	UpdateUnknownBuild                = "The installed build uses an unrecognized version label. Mihari cannot determine whether this is an upgrade or downgrade, or confirm data compatibility."
	UpdateUnknownVersion              = "No usable version label is available for one or more installed copies. Mihari cannot determine whether this is an upgrade or downgrade, or confirm data compatibility."
	UpdateDowngradeCompatibility      = "Older Mihari versions may not support settings, subscriptions, state, or generated files written by the current version. Mihari may fail to start or load data, which can look like data loss.\n\nDowngrade is not a supported configuration migration and does not roll back disk state."
	UpdateAfterService                = "Replace Mihari, synchronize and restart the installed service, verify its version, then reopen the TUI."
	UpdateAfterStandalone             = "Replace Mihari, then reopen the TUI."
	UpdateRetryNote                   = "This TUI closes after confirmation. If installation fails, reopen Mihari to retry."
	UpdateScrollHint                  = "↑/↓ PgUp/PgDn scroll"
	UpdateSelectHint                  = "←/→ Tab select · Enter accept · Esc cancel"
)
