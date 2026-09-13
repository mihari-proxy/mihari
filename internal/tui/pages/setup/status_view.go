package setup

import (
	"fmt"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// statusRow aligns a muted label with a value styled by the existing theme.
func (m *Model) statusRow(label, value string) string {
	return "  " + m.theme.Muted.Render(fmt.Sprintf("%-12s", label)) + "  " + value
}

// statusText normalizes safe metadata to one line and labels missing values as unknown.
func (m *Model) statusText(value string) string {
	value = strings.Join(strings.Fields(m.safeText(value)), " ")
	if value == "" {
		return "Unknown"
	}
	return value
}

// coreStatusLines distinguishes unconfirmed, missing and reusable local core state.
func (m *Model) coreStatusLines() []string {
	lines := []string{m.theme.Title.Render(ui.SetupCoreTitle), ui.SetupCoreBody, "", m.theme.Title.Render("Current status")}
	if !m.coreLocalLoaded {
		return append(lines, m.statusRow("Local core", m.theme.Warning.Render("Not confirmed")), "", m.theme.Info.Render("Next · Enter recheck local core status."))
	}
	state, next := m.theme.Warning.Render("Not ready"), ui.SetupCoreWillDownload
	if m.coreLocal.LocalReady {
		state = m.theme.Success.Render("Ready")
		next = "Enter verify and reuse the local core; no download needed."
	}
	lines = append(lines, m.statusRow("Local core", state),
		m.statusRow("Version", m.statusText(m.coreLocal.LocalVersion)),
		m.statusRow("Channel", m.statusText(m.coreLocal.Channel)),
		m.statusRow("Runtime", m.statusText(m.coreLocal.Status)),
		"", m.theme.Info.Render("Next · "+next))
	return lines
}

// databaseTime displays a confirmed timestamp in local time or an unknown placeholder.
func databaseTime(value time.Time) string {
	if value.IsZero() {
		return "Unknown"
	}
	return value.Local().Format("2006-01-02 15:04 MST")
}

// databaseStatus distinguishes usable saved data from the result of its latest update.
func (m *Model) databaseStatus(value protocol.GeoIPDatabaseStatus) string {
	if value.Available {
		if value.Error != "" {
			return m.theme.Warning.Render("Ready · update failed")
		}
		return m.theme.Success.Render("Ready")
	}
	return m.theme.Warning.Render("Unavailable")
}

// geoipStatusLines summarizes Country and ASN availability, update times and the next action.
func (m *Model) geoipStatusLines() []string {
	lines := []string{m.theme.Title.Render(ui.SetupGeoIPTitle), ui.SetupGeoIPBody, "", m.theme.Title.Render("Current status")}
	if !m.geoipLocalLoaded {
		return append(lines, m.statusRow("Country", m.theme.Warning.Render("Not confirmed")),
			m.statusRow("ASN", m.theme.Warning.Render("Not confirmed")), "",
			m.theme.Info.Render("Next · Enter check and prepare databases, or s skip."))
	}
	lines = append(lines, m.statusRow("Country", m.databaseStatus(m.geoipLocal.Country)),
		m.statusRow("Updated", databaseTime(m.geoipLocal.Country.UpdatedAt)),
		m.statusRow("ASN", m.databaseStatus(m.geoipLocal.ASN)),
		m.statusRow("Updated", databaseTime(m.geoipLocal.ASN.UpdatedAt)))
	next := ui.SetupGeoIPWillDownload
	if m.geoipLocal.Country.Available && m.geoipLocal.ASN.Available {
		next = ui.SetupGeoIPLocalReady
	}
	return append(lines, "", m.theme.Info.Render("Next · "+next))
}

// hasSubscriptions includes both the loaded catalog and a profile saved in this session.
func (m *Model) hasSubscriptions() bool {
	return len(m.subscriptions.Subscriptions) > 0 || (m.addedSubscription != nil && m.addedSubscription.ID != "")
}

// subscriptionNeedsRetry identifies a newly saved profile whose download still needs attention.
func (m *Model) subscriptionNeedsRetry() bool {
	return m.addedSubscription != nil && m.addedSubscription.ID != "" && (!m.addedSubscription.Cached || m.addedSubscription.LastError != "")
}

// rememberSubscription updates the displayed catalog by ID without adding duplicate entries.
func (m *Model) rememberSubscription(value protocol.Subscription) {
	if value.ID == "" {
		return
	}
	for i, profile := range m.subscriptions.Subscriptions {
		if profile.ID == value.ID {
			m.subscriptions.Subscriptions[i] = value
			return
		}
	}
	m.subscriptions.Subscriptions = append(m.subscriptions.Subscriptions, value)
}

// subscriptionCounts summarizes cached profiles without errors separately from those needing attention.
func (m *Model) subscriptionCounts() string {
	ready := 0
	for _, profile := range m.subscriptions.Subscriptions {
		if profile.Cached && profile.LastError == "" {
			ready++
		}
	}
	count := len(m.subscriptions.Subscriptions)
	noun := "subscriptions"
	if count == 1 {
		noun = "subscription"
	}
	return fmt.Sprintf("%d %s · %d ready · %d need attention", count, noun, ready, count-ready)
}

// subscriptionStatusLines shows saved profiles or the initial form and directs further additions to Subscriptions.
func (m *Model) subscriptionStatusLines() []string {
	lines := []string{m.theme.Title.Render(ui.SetupSubscriptionTitle)}
	if m.subscriptionsErr != nil {
		return append(lines, "", m.theme.Warning.Render("Current status · Not confirmed"), "Enter recheck subscriptions, or Ctrl+S skip.")
	}
	if !m.hasSubscriptions() {
		lines = append(lines, ui.SetupSubscriptionBody, "", m.theme.Muted.Render("Current status · No subscriptions"), "")
		lines = append(lines, renderInputs([]string{"Name", "URL"}, m.subscriptionInputs, m.focusedField)...)
		return append(lines, "", ui.SetupSubscriptionHelp)
	}
	lines = append(lines, "", m.theme.Title.Render("Current status"), "  "+m.subscriptionCounts(),
		"", m.theme.Info.Render("To add more, open the Subscriptions page."),
		m.theme.Muted.Render("Manage refresh, enable/disable and selection there too."), "")
	if m.subscriptions.ActiveID == "" {
		lines = append(lines, m.statusRow("Current", "None selected"))
	}
	for _, profile := range m.subscriptions.Subscriptions {
		name := m.statusText(profile.Name)
		if profile.ID == m.subscriptions.ActiveID {
			name = m.theme.Success.Render("Current · ") + name
		}
		state := m.theme.Warning.Render("Not downloaded")
		switch {
		case profile.Cached && profile.LastError != "":
			state = m.theme.Warning.Render("Cached · refresh failed")
		case profile.Cached:
			state = m.theme.Success.Render("Ready")
		case profile.LastError != "":
			state = m.theme.Warning.Render("Download failed")
		}
		enabled := "Disabled"
		if profile.Enabled {
			enabled = "Enabled"
		}
		lines = append(lines, "  "+name, "    "+m.theme.Muted.Render(enabled)+" · "+state)
	}
	if m.subscriptionNeedsRetry() {
		lines = append(lines, "", m.theme.Warning.Render("Saved this session; Enter retries its download."))
	}
	return lines
}
