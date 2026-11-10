package system

import (
	"context"
	"fmt"
	"slices"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type authModeOption struct{ value, label, help string }

func authModeOptions(kind string) []authModeOption {
	if kind == "auth" {
		return []authModeOption{
			{"follow", "Follow subscription", "Use the subscription's users and passwords unchanged."},
			{"enabled", "Enabled", "Merge subscription users with users saved in Mihari."},
			{"disabled", "Disabled", "Disable authentication; retain users saved in Mihari."},
		}
	}
	return []authModeOption{
		{"follow", "Follow subscription", "Use skip-auth-prefixes from the active subscription."},
		{"custom", "Custom", "Use Local bypass and Additional bypass CIDRs below."},
		{"required", "Require all", "Require authentication for every client, including localhost."},
	}
}

// Resolve provenance through existing read-only IPC, including when saved
// authentication is disabled. Never infer subscription membership from a saved
// effective credential, and never request source passwords.
func loadAuthSource(ctx context.Context, client authClient, hasPreview bool, result *authResultMsg) {
	previewClient, ok := client.(authPreviewClient)
	if !ok || !hasPreview {
		result.sourceErr = "Subscription source unavailable. Update the daemon and TUI."
		return
	}
	preview, err := previewClient.PreviewProxyAuth(ctx, protocol.ProxyAuthPreviewRequest{Config: protocol.ProxyAuthConfig{Mode: "follow", BypassMode: "follow", Users: []protocol.ProxyAuthUser{}, BypassPrefixes: []string{}}})
	if err != nil {
		result.sourceErr = actionErrorDetail(err, "Subscription source unavailable")
		return
	}
	if preview.Revision != result.status.Revision {
		result.sourceErr = "Subscription changed while loading. Reopen to refresh user sources."
		return
	}
	result.sourceLoaded = true
	result.sourceID = preview.ActiveID
	seen := map[string]bool{}
	for _, line := range preview.Lines {
		if line.Field == "authentication" && line.Target != "" && line.Change != "-" && !seen[line.Target] {
			result.sourceUsers = append(result.sourceUsers, protocol.ProxyAuthUser{Username: line.Target, Source: "subscription"})
			seen[line.Target] = true
		}
	}
	if catalogClient, ok := client.(interface {
		Subscriptions(context.Context) (protocol.SubscriptionList, error)
	}); ok && preview.ActiveID != "" {
		catalog, err := catalogClient.Subscriptions(ctx)
		if err != nil {
			result.sourceErr = actionErrorDetail(err, "Subscription name unavailable")
			return
		}
		if catalog.Revision == preview.Revision && catalog.ActiveID == preview.ActiveID {
			for _, sub := range catalog.Subscriptions {
				if sub.ID == preview.ActiveID {
					result.sourceName = sub.Name
					break
				}
			}
		}
	}
}

func (m *Model) authSubscriptionLabel() string {
	a := &m.auth
	if a.sourceName != "" {
		return diagnostics.EscapeTerminal(a.sourceName)
	}
	if a.sourceID != "" {
		return "ID " + diagnostics.EscapeTerminal(a.sourceID)
	}
	if a.preview.ActiveID != "" {
		return "ID " + diagnostics.EscapeTerminal(a.preview.ActiveID)
	}
	if a.sourceLoaded {
		return "none"
	}
	return "source unavailable"
}

func (m *Model) authCustomBypass() bool {
	return m.auth.draft.Mode != "disabled" && m.auth.draft.BypassMode == "custom"
}

func (m *Model) authBypassUnavailableReason() string {
	if m.auth.draft.Mode == "disabled" {
		return "Authentication disabled. Saved bypass settings are retained."
	}
	if m.auth.draft.BypassMode == "follow" {
		return "Using subscription bypass rules. Choose Custom to edit."
	}
	if m.auth.draft.BypassMode == "required" {
		return "All clients require authentication. Choose Custom to edit."
	}
	return ""
}

// Row identities stay stable while navigation follows the visual parameter order.
func (m *Model) authNavigation() []int {
	n := len(m.authUsers())
	rows := []int{0, 3 + n}
	for i := range n {
		rows = append(rows, 3+i)
	}
	rows = append(rows, 1, 2)
	rows = append(rows, 4+n+len(m.auth.draft.BypassPrefixes))
	for i := range len(m.auth.draft.BypassPrefixes) {
		rows = append(rows, 4+n+i)
	}
	rows = append(rows, 5+n+len(m.auth.draft.BypassPrefixes), 7+n+len(m.auth.draft.BypassPrefixes), 6+n+len(m.auth.draft.BypassPrefixes))
	return rows
}

func (m *Model) moveAuthSelection(delta int) {
	rows := m.authNavigation()
	i := slices.Index(rows, m.auth.selected)
	if i < 0 {
		i = 0
	}
	m.auth.selected = rows[max(0, min(len(rows)-1, i+delta))]
}

func (m *Model) openAuthMode(kind string) {
	a := &m.auth
	a.picker = kind
	a.pickerIndex = 0
	value := a.draft.Mode
	if kind == "bypass" {
		value = a.draft.BypassMode
	}
	for i, option := range authModeOptions(kind) {
		if option.value == value {
			a.pickerIndex = i
		}
	}
}

type authParameterLine struct {
	text        string
	owner       int
	pickerFocus bool
}

func (m *Model) authParameterContent(width int) []authParameterLine {
	a := &m.auth
	width = max(1, width)
	var lines []authParameterLine
	var popup []string
	popupStart, popupOwner := 0, 0
	popupField := 0
	popupFocusStart, popupFocusEnd := 0, 0
	add := func(text string, owner int) {
		for _, part := range strings.Split(ansi.Hardwrap(text, width, true), "\n") {
			lines = append(lines, authParameterLine{text: part, owner: owner})
		}
	}
	muted := func(text string, owner int) {
		for _, part := range strings.Split(ansi.Wrap(text, max(1, width-2), ""), "\n") {
			add("  "+m.theme.Muted.Render(part), owner)
		}
	}
	focus := func(text string, row int) string {
		if a.selected == row {
			return m.theme.RowSelected.Render(text)
		}
		return text
	}
	pair := func(label, value string, row int) {
		pointer := "  "
		if a.selected == row {
			pointer = m.theme.Title.Render("› ")
		}
		gap := max(1, width-2-ansi.StringWidth(label)-ansi.StringWidth(value))
		add(pointer+lipgloss.NewStyle().Bold(true).Render(label)+strings.Repeat(" ", gap)+focus(value, row), row)
	}
	mode := func(kind, label string, row int) {
		fieldStart := len(lines)
		value := a.draft.Mode
		if kind == "bypass" {
			value = a.draft.BypassMode
		}
		opts := authModeOptions(kind)
		current := opts[0]
		for _, option := range opts {
			if option.value == value {
				current = option
			}
		}
		pair(label, current.label+" ▾", row)
		muted(current.help, row)
		if a.picker == kind {
			popupStart, popupOwner = len(lines), row
			popupField = fieldStart
			var menu []string
			menuWidth := max(1, width-4)
			for i, option := range opts {
				choiceStart := len(menu) + 1 // bordered menu's first line
				marker := "○ "
				title := option.label
				if option.value == value {
					marker = "● "
				}
				if i == a.pickerIndex {
					title = m.theme.RowSelected.Render(title)
					marker = m.theme.Title.Render("› ")
				}
				menu = append(menu, marker+title)
				for _, part := range strings.Split(ansi.Wrap(option.help, max(1, menuWidth-2), ""), "\n") {
					menu = append(menu, "  "+m.theme.Muted.Render(part))
				}
				if i == a.pickerIndex {
					popupFocusStart = choiceStart
					popupFocusEnd = len(menu) + 1
				}
			}
			box := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(m.theme.ColorSurfaceBorder).Width(menuWidth + 2).Render(strings.Join(menu, "\n"))
			for _, part := range strings.Split(box, "\n") {
				popup = append(popup, "  "+part)
			}
		}
	}
	mode("auth", "Authentication mode", 0)
	add("", 0)
	users := m.authUsers()
	n := len(users)
	prefixStart := 4 + n
	pair(fmt.Sprintf("Users (%d)", n), "[ + Add user ]", 3+n)
	muted("Active subscription: "+m.authSubscriptionLabel(), 3+n)
	userWidth := min(15, max(8, width/4))
	add("  "+m.theme.Muted.Render(fmt.Sprintf("%-*s %s", userWidth, "USERNAME", "CREDENTIAL SOURCE")), 3+n)
	for i, u := range users {
		row := 3 + i
		name := diagnostics.EscapeTerminal(u.Username)
		source := "Subscription: " + m.authSubscriptionLabel()
		detail := ""
		switch u.Source {
		case "overridden":
			source = "Password set in Mihari"
			detail = "User from subscription: " + m.authSubscriptionLabel()
		case "added":
			source = "Added in Mihari"
			detail = "Saved across subscriptions"
		}
		if a.sourceErr != "" && !a.sourceLoaded {
			source = "Saved in Mihari"
			if u.Source == "subscription" {
				source = "Subscription source unavailable"
			}
			detail = "Subscription membership unavailable"
		}
		marker := "  "
		if a.selected == row {
			marker = m.theme.Title.Render("› ")
		}
		nameLines := strings.Split(ansi.Hardwrap(name, userWidth, true), "\n")
		add(marker+focus(nameLines[0], row)+strings.Repeat(" ", max(1, userWidth-ansi.StringWidth(nameLines[0])+1))+source, row)
		for _, part := range nameLines[1:] {
			add("  "+focus(part, row), row)
		}
		if detail != "" {
			muted(strings.Repeat(" ", userWidth+1)+detail, row)
		}
		if a.draft.Mode == "disabled" {
			muted("Authentication disabled", row)
		} else if a.draft.Mode == "follow" && u.Source != "subscription" {
			muted("Mihari settings not applied in Follow mode", row)
		}
		if a.selected == row {
			action := "Enter: Override password"
			if u.Source == "overridden" {
				action = "Enter: Edit password · d: Remove override"
			}
			if u.Source == "added" {
				action = "Enter: Edit password · d: Remove user"
			}
			muted(action, row)
			if u.Source == "overridden" {
				muted("Removing the override restores the subscription password.", row)
			}
		}
		if a.reveal {
			for _, draft := range a.draft.Users {
				if draft.Username == u.Username && draft.Password != "" {
					muted("draft: "+diagnostics.EscapeTerminal(draft.Password), row)
				}
			}
			if credential, found := a.revealed[u.Username]; found {
				if credential.saved != nil {
					muted("saved: "+diagnostics.EscapeTerminal(credential.saved.Password), row)
				}
				if credential.effective != nil {
					muted("effective ("+diagnostics.EscapeTerminal(credential.effective.Source)+"): "+diagnostics.EscapeTerminal(credential.effective.Password), row)
				}
			}
		}
	}
	if n == 0 {
		muted("No users configured", 3+n)
	}
	if a.sourceErr != "" {
		muted(diagnostics.EscapeTerminal(a.sourceErr), 3+n)
	}
	if a.edit != "" {
		add("", a.selected)
		if a.edit == "user" {
			title := "Add user in Mihari"
			if a.original != "" {
				title = "Edit password for " + diagnostics.EscapeTerminal(a.original)
			}
			add("  "+title, a.selected)
		} else {
			add("  Edit bypass CIDR", a.selected)
		}
		for i := range a.inputs {
			a.inputs[i].SetWidth(max(1, width-13))
		}
		if a.edit == "user" {
			add("  Username   "+a.inputs[0].View(), a.selected)
			add("  Password   "+a.inputs[1].View(), a.selected)
		} else {
			add("  CIDR       "+a.inputs[0].View(), a.selected)
		}
		muted("Enter accept · Esc cancel edit", a.selected)
	}
	add("", 1)
	mode("bypass", "Bypass mode", 1)
	add("", 2)
	value := "[ ] Disabled"
	if a.draft.LocalBypass {
		value = "[x] Enabled"
	}
	if !m.authCustomBypass() {
		value = m.theme.Muted.Render(value)
	}
	pair("Local bypass", value, 2)
	muted("Allow loopback clients to connect without authentication.", 2)
	muted("127.0.0.0/8 · ::1/128", 2)
	if !m.authCustomBypass() {
		muted(m.authBypassUnavailableReason(), 2)
	}
	add("", prefixStart)
	addCIDR := "[ + Add CIDR ]"
	if !m.authCustomBypass() {
		addCIDR = m.theme.Muted.Render(addCIDR)
	}
	pair("Additional bypass CIDRs", addCIDR, prefixStart+len(a.draft.BypassPrefixes))
	for i, prefix := range a.draft.BypassPrefixes {
		action := "[ d: Remove ]"
		if !m.authCustomBypass() {
			action = m.theme.Muted.Render("Not applied")
		}
		pair(diagnostics.EscapeTerminal(prefix), action, prefixStart+i)
	}
	if len(a.draft.BypassPrefixes) == 0 {
		muted("None configured", prefixStart)
	}
	// The dropdown covers the rows below its field, keeping the dialog's size
	// stable while choosing. Row ownership keeps scrolling attached to the menu.
	if popupStart+len(popup) > len(lines) {
		popupStart = max(0, popupField-len(popup))
	}
	for i, text := range popup {
		index := popupStart + i
		if index < len(lines) {
			lines[index] = authParameterLine{text: text, owner: popupOwner, pickerFocus: i >= popupFocusStart && i < popupFocusEnd}
		} else {
			lines = append(lines, authParameterLine{text: text, owner: popupOwner, pickerFocus: i >= popupFocusStart && i < popupFocusEnd})
		}
	}
	return lines
}

func (m *Model) authParameterLines(width, height int) []string {
	return m.authParameterWindow(m.authParameterContent(width), width, height)
}

func (m *Model) authParameterWindow(content []authParameterLine, width, height int) []string {
	a := &m.auth
	height = max(1, height)
	start, end := 0, 1
	found := false
	for i, line := range content {
		if line.owner == a.selected {
			if !found {
				start = i
				found = true
			}
			end = i + 1
		}
	}
	if a.selected >= 5+len(m.authUsers())+len(a.draft.BypassPrefixes) {
		start = max(0, len(content)-1)
		end = len(content)
	}
	if a.edit != "" {
		for i, line := range content {
			if strings.Contains(ansi.Strip(line.text), "Enter accept") {
				start = max(0, i-3)
				end = i + 1
			}
		}
	}
	if a.picker != "" {
		found = false
		for i, line := range content {
			if line.pickerFocus {
				if !found {
					start = i
					found = true
				}
				end = i + 1
			}
		}
	}
	var status []string
	if !a.loaded {
		status = append(status, "Loading…")
	}
	if len(status) >= height {
		a.parameterPage = 1
		return ui.SliceLines(status, 0, height)
	}
	height -= len(status)
	a.parameterPage = height
	if a.manualScroll {
		a.top = min(a.top, max(0, len(content)-height))
	} else {
		a.top = ui.EnsureLineVisible(a.top, height, len(content), start, end)
	}
	var lines []string
	for _, line := range content[a.top:min(len(content), a.top+height)] {
		lines = append(lines, line.text)
	}
	return append(lines, status...)
}
