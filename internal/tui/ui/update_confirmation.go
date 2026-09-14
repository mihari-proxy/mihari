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
	// UpdateInstalledHeading labels the installed copies section.
	UpdateInstalledHeading = "Installed"
	// UpdateTargetHeading labels the verified candidate version.
	UpdateTargetHeading = "Target"
	// UpdateCompatibilityUnknownHeading labels an incomparable version warning.
	UpdateCompatibilityUnknownHeading = "Compatibility unknown"
	// UpdateDowngradeHeading labels a confirmed downgrade warning.
	UpdateDowngradeHeading = "Downgrade detected"
	// UpdateAfterHeading labels the post-confirmation actions.
	UpdateAfterHeading = "After confirmation"
	// UpdateUnknownBuild explains a safe but incomparable installed label.
	UpdateUnknownBuild = "The installed build uses an unrecognized version label. Mihari cannot determine whether this is an upgrade or downgrade, or confirm data compatibility."
	// UpdateUnknownVersion explains a missing usable installed label.
	UpdateUnknownVersion = "No usable version label is available for one or more installed copies. Mihari cannot determine whether this is an upgrade or downgrade, or confirm data compatibility."
	// UpdateDowngradeCompatibility preserves the data compatibility and rollback risks.
	UpdateDowngradeCompatibility = "Older Mihari versions may not support settings, subscriptions, state, or generated files written by the current version. Mihari may fail to start or load data, which can look like data loss.\n\nDowngrade is not a supported configuration migration and does not roll back disk state."
	// UpdateAfterService describes updating a registered service and reopening the TUI.
	UpdateAfterService = "Replace Mihari, synchronize and restart the installed service, verify its version, then reopen the TUI."
	// UpdateAfterStandalone describes updating a standalone binary.
	UpdateAfterStandalone = "Replace Mihari, then reopen the TUI."
	// UpdateRetryNote explains how to recover after the old TUI closes.
	UpdateRetryNote = "This TUI closes after confirmation. If installation fails, reopen Mihari to retry."
	// UpdateScrollHint lists the confirmation body scroll keys.
	UpdateScrollHint = "↑/↓ PgUp/PgDn scroll"
	// UpdateSelectHint lists the confirmation selection and cancellation keys.
	UpdateSelectHint = "←/→ Tab select · Enter accept · Esc cancel"
)
