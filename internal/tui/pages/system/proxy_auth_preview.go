package system

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type authPreviewClient interface {
	PreviewProxyAuth(context.Context, protocol.ProxyAuthPreviewRequest) (protocol.ProxyAuthPreview, error)
}
type authPreviewDueMsg struct{ epoch, sequence uint64 }
type authPreviewResultMsg struct {
	reveal          bool
	epoch, sequence uint64
	value           protocol.ProxyAuthPreview
	err             error
}

func (m *Model) toggleAuthReveal() tea.Cmd {
	a := &m.auth
	if a.reveal {
		m.hideAuthSecrets()
		return nil
	}
	a.reveal = true
	a.revealSequence++
	a.inputs[1].EchoMode = textinput.EchoNormal
	return m.authLoad(true)
}

func (m *Model) hideAuthSecrets() {
	a := &m.auth
	a.reveal = false
	a.revealSequence++
	a.inputs[1].EchoMode = textinput.EchoPassword
	a.revealed = nil
	a.preview.Lines = nil
	a.previewRevealed = false
	for i := range a.status.Users {
		a.status.Users[i].Password = ""
	}
}

func (m *Model) scheduleAuthPreview() tea.Cmd {
	a := &m.auth
	if a.previewCancel != nil {
		a.previewCancel()
		a.previewCancel = nil
	}
	a.previewSeq++
	if !a.reveal && a.previewRevealed {
		a.preview.Lines = nil
		a.previewRevealed = false
	}
	a.previewErr = ""
	if _, ok := m.client.(authPreviewClient); !ok {
		a.previewPending = false
		a.previewErr = "Preview unavailable. Update the daemon and TUI."
		return nil
	}
	if !m.hasCapability(protocol.CapabilityProxyAuthPreview) {
		a.previewPending = false
		a.previewErr = "Preview unavailable. Update the daemon and TUI."
		return nil
	}
	if !a.open || !a.loaded {
		return nil
	}
	a.previewPending = true
	epoch, sequence := a.epoch, a.previewSeq
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return ui.PageResultMsg{Page: ui.PageSystem, Result: authPreviewDueMsg{epoch, sequence}}
	})
}

func (m *Model) authPreviewDraft() protocol.ProxyAuthConfig {
	a := &m.auth
	draft := a.draft
	draft.Users = slices.Clone(draft.Users)
	draft.BypassPrefixes = slices.Clone(draft.BypassPrefixes)
	switch a.edit {
	case "user":
		user := protocol.ProxyAuthUser{Username: a.inputs[0].Value(), Password: a.inputs[1].Value()}
		i := slices.IndexFunc(draft.Users, func(u protocol.ProxyAuthUser) bool { return u.Username == user.Username })
		if i >= 0 {
			if user.Password != "" {
				draft.Users[i].Password = user.Password
			}
		} else if a.original == "" || user.Password != "" {
			draft.Users = append(draft.Users, user)
		}
	case "prefix":
		value := strings.TrimSpace(a.inputs[0].Value())
		i := slices.Index(draft.BypassPrefixes, a.original)
		if a.original != "" && i >= 0 {
			draft.BypassPrefixes[i] = value
		} else {
			draft.BypassPrefixes = append(draft.BypassPrefixes, value)
		}
		draft.BypassMode = "custom"
	}
	return draft
}

func (m *Model) loadAuthPreview(due authPreviewDueMsg) tea.Cmd {
	a := &m.auth
	if !a.open || !a.loaded || a.epoch != due.epoch || a.previewSeq != due.sequence {
		return nil
	}
	client, ok := m.client.(authPreviewClient)
	if !ok {
		return nil
	}
	request := protocol.ProxyAuthPreviewRequest{Config: m.authPreviewDraft(), Reveal: a.reveal}
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	a.previewCancel = cancel
	return func() tea.Msg {
		defer cancel()
		value, err := client.PreviewProxyAuth(ctx, request)
		return ui.PageResultMsg{Page: ui.PageSystem, Result: authPreviewResultMsg{epoch: due.epoch, sequence: due.sequence, reveal: request.Reveal, value: value, err: err}}
	}
}

func (m *Model) handleAuthPreview(result authPreviewResultMsg) {
	a := &m.auth
	if !a.open || !a.loaded || result.epoch != a.epoch || result.sequence != a.previewSeq {
		return
	}
	a.previewPending = false
	if result.err != nil {
		a.previewErr = actionErrorDetail(result.err, "Preview unavailable")
		return
	}
	if result.value.Revision != a.status.Revision {
		a.previewErr = "Settings or subscription changed. Reopen to review current settings."
		return
	}
	a.preview = result.value
	a.previewRevealed = result.reveal
	a.previewErr = ""
	a.previewManual = false
}

func (m *Model) updateAuth(message tea.Msg) tea.Cmd {
	a := &m.auth
	if key, ok := message.(tea.KeyPressMsg); ok && a.edit == "" && a.picker == "" && a.loaded && !a.pending {
		if key.String() == "tab" || key.String() == "shift+tab" {
			a.previewFocus = !a.previewFocus
			return nil
		}
		if a.previewFocus {
			delta := 0
			switch key.String() {
			case "up":
				delta = -1
			case "down":
				delta = 1
			case "pgup":
				delta = -m.authPreviewPageSize()
			case "pgdown":
				delta = m.authPreviewPageSize()
			}
			if delta != 0 {
				a.previewManual = true
				a.previewTop = max(0, a.previewTop+delta)
				return nil
			}
			if key.String() != "esc" && key.String() != "ctrl+s" && key.String() != "ctrl+r" {
				return nil
			}
		}
	}
	before, edit, reveal, sequence, epoch, cancel, selected := m.authPreviewDraft(), a.edit, a.reveal, a.previewSeq, a.epoch, a.previewCancel, a.selected
	cmd := m.updateAuthControls(message)
	if a.selected != selected {
		a.previewManual = false
	}
	if (!a.open || a.epoch != epoch) && cancel != nil {
		cancel()
	}
	if !a.reveal && reveal {
		a.preview.Lines = nil
	}
	if a.open && a.loaded && (!reflect.DeepEqual(before, m.authPreviewDraft()) || edit != a.edit || reveal != a.reveal) && sequence == a.previewSeq {
		return tea.Batch(cmd, m.scheduleAuthPreview())
	}
	return cmd
}

func (m *Model) authPreviewPageSize() int { return max(1, m.auth.previewPage) }

func (m *Model) authPreviewSelected(line protocol.ProxyAuthPreviewLine) bool {
	a := &m.auth
	users := m.authUsers()
	prefixStart := 4 + len(users)
	if a.edit == "user" {
		return line.Field == "authentication" && line.Target == a.inputs[0].Value()
	}
	if a.edit == "prefix" {
		return line.Field == "skip-auth-prefixes" && (line.Target == strings.TrimSpace(a.inputs[0].Value()) || line.Target == a.original)
	}
	switch {
	case a.selected == 0:
		return line.Field == "authentication" || (line.Field == "skip-auth-prefixes" && line.Change != "" && (a.draft.Mode == "disabled" || a.status.Config.Mode == "disabled"))
	case a.selected == 1:
		return line.Field == "skip-auth-prefixes"
	case a.selected == 2:
		return line.Field == "skip-auth-prefixes" && (line.Target == "127.0.0.0/8" || line.Target == "::1/128")
	case a.selected >= 3 && a.selected < 3+len(users):
		return line.Field == "authentication" && line.Target == users[a.selected-3].Username
	case a.selected >= prefixStart && a.selected < prefixStart+len(a.draft.BypassPrefixes):
		return line.Field == "skip-auth-prefixes" && line.Target == a.draft.BypassPrefixes[a.selected-prefixStart]
	}
	return false
}

func (m *Model) authPreviewLines(width, height int) []string {
	a := &m.auth
	header := "Preview only"
	if a.previewPending {
		header += " · updating"
	} else if a.previewErr != "" {
		header += " · stale"
	}
	if a.previewFocus {
		header += " · focused"
	}
	lines := []string{m.theme.Title.Render(ui.TruncateVisible(header, width))}
	var notes []string
	if a.previewPending {
		notes = append(notes, "Updating… Previous preview is stale.")
	}
	if a.previewErr != "" {
		notes = append(notes, "Preview stale: "+diagnostics.EscapeTerminal(a.previewErr))
	}
	if !a.previewPending && a.previewErr == "" && a.preview.ActiveID == "" {
		notes = append(notes, "No active subscription. Preview uses settings saved in Mihari only.")
	}
	if !a.reveal {
		notes = append(notes, "Passwords masked; placeholders are display only.")
	}
	if a.draft.Mode == "disabled" && a.selected > 0 {
		notes = append(notes, "Authentication disabled. Saved parameters do not change authentication.")
	}
	// Notes and YAML share the scrollable pane so even short terminals can
	// inspect the complete error and the configuration fragment.
	var body []string
	for _, note := range notes {
		for _, part := range strings.Split(ansi.Wrap(note, max(1, width), ""), "\n") {
			body = append(body, m.theme.Muted.Render(part))
		}
	}
	selectedStart, selectedEnd := -1, -1
	for _, line := range a.preview.Lines {
		mark := "  "
		if line.Change != "" {
			mark = line.Change + " "
		}
		selected := m.authPreviewSelected(line)
		start := len(body)
		for _, part := range strings.Split(ansi.Hardwrap(diagnostics.EscapeTerminal(line.Text), max(1, width-2), true), "\n") {
			text := mark + part
			if line.Change == "+" {
				text = m.theme.Success.Render(text)
			} else if line.Change == "-" {
				text = m.theme.Danger.Render(text)
			} else if strings.HasPrefix(line.Text, "#") {
				text = m.theme.Muted.Render(text)
			}
			if selected {
				text = lipgloss.NewStyle().Background(lipgloss.Color("236")).Width(width).Render(text)
			}
			body = append(body, text)
		}
		if selected && selectedStart < 0 {
			selectedStart, selectedEnd = start, len(body)
		}
	}
	body = append(body, "")
	for _, part := range strings.Split(ansi.Wrap("+ added · - removed · focused parameter shaded", max(1, width), ""), "\n") {
		body = append(body, m.theme.Muted.Render(part))
	}
	for _, part := range strings.Split(ansi.Wrap("To edit this YAML directly, use a local YAML file in Subscriptions.", max(1, width), ""), "\n") {
		body = append(body, m.theme.Muted.Render(part))
	}
	budget := max(1, height-1)
	a.previewPage = budget
	if !a.previewManual && selectedStart >= 0 {
		a.previewTop = ui.EnsureLineVisible(a.previewTop, budget, len(body), selectedStart, selectedEnd)
	}
	a.previewTop = min(a.previewTop, max(0, len(body)-budget))
	return append(lines, ui.SliceLines(body, a.previewTop, budget)...)
}
