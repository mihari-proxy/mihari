package setup

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// FooterHints keeps actions accurate for the current step and work state.
func (m *Model) FooterHints() string {
	if m.loading {
		if m.settling || m.cancelRequested {
			return "Waiting for settlement  Ctrl+C quit (saved work is kept)"
		}
		if m.cancelExecution == nil {
			return "Reading saved state  Ctrl+C quit"
		}
		return "Esc cancel operation  Ctrl+C quit"
	}
	hints := "Enter continue  Esc back  Ctrl+Q exit"
	if m.resultUnknown {
		hints = "Enter recheck result  Ctrl+Q exit"
		if m.errorDetail != "" {
			hints += "  F2 details"
		}
		return hints
	}
	if m.waitingRestart {
		return "Enter recheck daemon  Esc edit ports  Ctrl+Q exit"
	}
	if m.step == stepCore {
		action := "install"
		if !m.coreLocalLoaded {
			action = "recheck"
		} else if m.coreLocal.LocalReady {
			action = "verify and reuse"
		}
		hints = "Enter " + action + "  Esc back  Ctrl+Q exit"
	}
	if m.step == stepEndpoints || (m.step == stepSubscription && !m.hasSubscriptions() && m.subscriptionsErr == nil) {
		hints += "  Tab fields"
	}
	if m.step == stepSubscription {
		if m.subscriptionsErr != nil {
			hints = "Enter recheck  Ctrl+S skip  Esc back  Ctrl+Q exit"
		} else if m.subscriptionNeedsRetry() {
			hints = "Enter retry download  Ctrl+S skip  Esc back  Ctrl+Q exit"
		} else if !m.hasSubscriptions() {
			hints += "  Ctrl+S skip"
		}
	}
	if m.step == stepGeoIP {
		hints += "  s skip"
	}
	if m.errorDetail != "" {
		hints += "  F2 details"
	}
	return hints
}

// renderFrame bounds the themed step layout while reserving space for progress and diagnostics.
func (m *Model) renderFrame(body []string) string {
	width := max(1, min(86, m.width))
	height := max(1, m.height)
	if width < 40 || height < 12 {
		return ui.TruncateVisible(ui.ResizeRequired, width)
	}
	inner := width - 4
	steps := []string{"Endpoints", "Core", "Subscription", "GeoIP", "Review"}
	for i, name := range steps {
		if step(i) == m.step {
			steps[i] = m.theme.Title.Render(name)
		} else {
			steps[i] = m.theme.Muted.Render(name)
		}
	}
	header := m.theme.Title.Render(ui.SetupTitle) + "\n" + ui.TruncateVisible(strings.Join(steps, " › "), inner)
	var content []string
	for _, line := range body {
		switch line {
		case ui.SetupEndpointHelp, ui.SetupEnterInstall, ui.SetupSubscriptionHelp, ui.SetupEnterOrSkip, ui.SetupCompleteHelp:
			continue
		}
		content = append(content, ui.TruncateVisible(line, inner))
	}
	var tail []string
	if m.settlementNotice != "" {
		tail = append(tail, m.theme.Info.Render(ui.TruncateVisible(m.settlementNotice, inner)))
	}
	if m.loading {
		tail = append(tail, m.theme.Info.Render(ui.TruncateVisible(m.executionText(), inner)))
	}
	if m.lastError != "" {
		tail = append(tail, m.theme.Danger.Render(ui.TruncateVisible(m.lastError, inner)))
		if m.errorAdvice != "" {
			tail = append(tail, m.theme.Muted.Render(ui.TruncateVisible(m.errorAdvice, inner)))
		}
		if m.errorDetail != "" {
			tail = append(tail, m.theme.Info.Render("F2  Open error details"))
		}
	}
	rows := max(1, height-6-len(tail))
	start := min(m.scroll, max(0, len(content)-rows))
	end := min(len(content), start+rows)
	visible := append([]string{}, content[start:end]...)
	if end < len(content) && len(visible) > 0 {
		visible[len(visible)-1] = m.theme.Muted.Render("↓ More · PgDn/PgUp scroll")
	}
	for len(visible) < rows {
		visible = append(visible, "")
	}
	text := header + "\n\n" + strings.Join(visible, "\n") + "\n" + strings.Join(tail, "\n")
	box := m.theme.Content.Border(lipgloss.RoundedBorder()).BorderForeground(m.theme.ColorSurfaceBorder).Padding(0, 1).Width(width).MaxWidth(width).MaxHeight(height).Render(text)
	return box
}
