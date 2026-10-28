package system

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"slices"
	"strings"
	"time"
)

type authClient interface {
	ProxyAuth(context.Context, bool) (protocol.ProxyAuthStatus, error)
	UpdateProxyAuth(context.Context, protocol.ProxyAuthUpdateRequest) (protocol.ProxyAuthStatus, error)
}
type revealedAuthCredential struct {
	saved, effective *protocol.ProxyAuthUser
}
type authUI struct {
	revealed                      map[string]revealedAuthCredential
	manualScroll                  bool
	open, loaded, pending, reveal bool
	conflict                      bool
	epoch                         uint64
	status                        protocol.ProxyAuthStatus
	draft                         protocol.ProxyAuthConfig
	selected, top                 int
	edit, original, err           string
	inputs                        [2]textinput.Model
	input                         int
}
type authResultMsg struct {
	status           protocol.ProxyAuthStatus
	epoch            uint64
	mutation, reveal bool
	err              error
}

func (m authResultMsg) Err() error                        { return m.err }
func (m authResultMsg) Warnings() protocol.WarningOutcome { return m.status.WarningOutcome }
func (m *Model) authLoad(reveal bool) tea.Cmd {
	client, ok := m.client.(authClient)
	if !ok {
		return nil
	}
	epoch, ctx := m.auth.epoch, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		v, e := client.ProxyAuth(ctx, reveal)
		return ui.PageResultMsg{Page: ui.PageSystem, Result: authResultMsg{status: v, epoch: epoch, reveal: reveal, err: e}}
	}
}
func (m *Model) openAuth() tea.Cmd {
	if !m.mutationsEnabled || !m.hasCapability(protocol.CapabilityProxyAuth) {
		return nil
	}
	if _, ok := m.client.(authClient); !ok {
		return nil
	}
	m.auth = authUI{open: true, epoch: m.auth.epoch + 1}
	return tea.Batch(func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputText} }, m.authLoad(false))
}
func (m *Model) handleAuthResult(r authResultMsg) tea.Cmd {
	if r.epoch != m.auth.epoch || !m.auth.open {
		return nil
	}
	m.auth.pending = false
	if r.err != nil {
		m.auth.err = actionErrorDetail(r.err, "Proxy authentication failed") + " (F2: details)"
		var api protocol.APIError
		if errors.As(r.err, &api) && api.Code == protocol.CodeRevisionConflict {
			m.auth.conflict = true
			return m.authLoad(false)
		}
		return nil
	}
	if r.reveal {
		m.auth.revealed = map[string]revealedAuthCredential{}
		for _, u := range r.status.Users {
			m.auth.revealed[u.Username] = revealedAuthCredential{effective: &u}
		}
		for _, u := range r.status.Config.Users {
			credential := m.auth.revealed[u.Username]
			credential.saved = &u
			m.auth.revealed[u.Username] = credential
		}
		m.auth.reveal = true
		return nil
	}
	m.auth.status = r.status
	m.auth.draft = r.status.Config
	m.auth.loaded = true
	m.auth.reveal = false
	m.auth.revealed = nil
	m.auth.err = ""
	if m.auth.conflict {
		m.auth.conflict = false
		m.auth.err = "Settings changed. Current configuration reloaded; review and reapply your edits."
	}
	if r.mutation {
		return func() tea.Msg { return ui.RuntimeRevisionMsg{Revision: r.status.Revision} }
	}
	return nil
}
func (m *Model) authRows() []string {
	a := &m.auth
	inactive := ""
	if a.draft.Mode == "disabled" {
		inactive = " (inactive)"
	}
	rows := []string{"Authentication: " + a.draft.Mode, "Bypass rules: " + a.draft.BypassMode + inactive, fmt.Sprintf("Local bypass: %t", a.draft.LocalBypass) + inactive}
	for _, u := range m.authUsers() {
		label := diagnostics.EscapeTerminal(u.Username) + " · " + u.Source
		if a.reveal {
			if credential, found := a.revealed[u.Username]; found {
				if credential.saved != nil {
					label += "\n  saved: " + diagnostics.EscapeTerminal(credential.saved.Password)
				}
				if credential.effective != nil {
					label += "\n  effective (" + diagnostics.EscapeTerminal(credential.effective.Source) + "): " + diagnostics.EscapeTerminal(credential.effective.Password)
				}
			}
		}
		rows = append(rows, "User: "+label)
	}
	rows = append(rows, "[ Add user ]")
	for _, p := range a.draft.BypassPrefixes {
		rows = append(rows, "Bypass CIDR: "+diagnostics.EscapeTerminal(p))
	}
	return append(rows, "[ Add bypass CIDR ]", "[ Reset Proxy Authentication ]", "[ Save ]")
}
func (m *Model) authUsers() []protocol.ProxyAuthUser {
	users := []protocol.ProxyAuthUser{}
	for _, u := range m.auth.status.Users {
		if u.Source == "added" {
			continue
		}
		if u.Source == "overridden" {
			u.Source = "subscription"
		}
		users = append(users, u)
	}
	for _, u := range m.auth.draft.Users {
		i := slices.IndexFunc(users, func(v protocol.ProxyAuthUser) bool { return v.Username == u.Username })
		if i >= 0 {
			if users[i].Source == "subscription" {
				users[i].Source = "overridden"
			}
		} else {
			u.Source = "added"
			users = append(users, u)
		}
	}
	return users
}
func (m *Model) authSave(reset bool) tea.Cmd {
	client, ok := m.client.(authClient)
	if !ok || m.auth.pending || !m.auth.loaded {
		return nil
	}
	id := m.newOperationID()
	revision := m.auth.status.Revision
	request := protocol.ProxyAuthUpdateRequest{OperationID: id, IfRevision: &revision, Config: m.auth.draft, Reset: reset}
	// Detach slices from the interactive draft before asynchronous IO.
	request.Config.Users = append([]protocol.ProxyAuthUser{}, request.Config.Users...)
	request.Config.BypassPrefixes = append([]string{}, request.Config.BypassPrefixes...)
	m.auth.epoch++
	epoch, ctx := m.auth.epoch, m.ctx
	m.auth.pending = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		v, e := client.UpdateProxyAuth(ctx, request)
		return ui.PageResultMsg{Page: ui.PageSystem, Result: authResultMsg{status: v, epoch: epoch, mutation: true, err: e}}
	}
}
func (m *Model) authEdit(kind, name string) tea.Cmd {
	a := &m.auth
	a.edit = kind
	a.original = name
	a.input = 0
	for i := range a.inputs {
		a.inputs[i] = textinput.New()
		a.inputs[i].CharLimit = 255
		a.inputs[i].SetWidth(max(10, min(60, m.width-16)))
	}
	if kind == "user" {
		a.inputs[0].Placeholder = "Username"
		a.inputs[0].SetValue(name)
		a.inputs[1].Placeholder = "Password (blank keeps existing override)"
		a.inputs[1].EchoMode = textinput.EchoPassword
		if name != "" {
			a.input = 1
		}
	}
	if kind == "prefix" {
		a.inputs[0].Placeholder = "CIDR, e.g. 192.0.2.0/24"
		a.inputs[0].SetValue(name)
	}
	return a.inputs[a.input].Focus()
}
func (m *Model) updateAuth(message tea.Msg) tea.Cmd {
	a := &m.auth
	key, ok := message.(tea.KeyPressMsg)
	if a.pending {
		return nil
	}
	if a.edit != "" {
		if ok {
			switch key.String() {
			case "esc":
				a.edit = ""
				return nil
			case "ctrl+r":
				if a.inputs[1].EchoMode == textinput.EchoPassword {
					a.inputs[1].EchoMode = textinput.EchoNormal
				} else {
					a.inputs[1].EchoMode = textinput.EchoPassword
				}
				return nil
			case "tab", "shift+tab":
				if a.edit == "user" && a.original == "" {
					a.inputs[a.input].Blur()
					a.input = 1 - a.input
					return a.inputs[a.input].Focus()
				}
				return nil
			case "enter":
				if a.edit == "prefix" {
					value := strings.TrimSpace(a.inputs[0].Value())
					if a.original != "" {
						i := slices.Index(a.draft.BypassPrefixes, a.original)
						if i >= 0 {
							a.draft.BypassPrefixes[i] = value
						}
					} else {
						a.draft.BypassPrefixes = append(a.draft.BypassPrefixes, value)
					}
					a.draft.BypassMode = "custom"
				} else {
					name := a.inputs[0].Value()
					if name == "" {
						a.err = "Username is required"
						return nil
					}
					u := protocol.ProxyAuthUser{Username: name, Password: a.inputs[1].Value()}
					i := slices.IndexFunc(a.draft.Users, func(v protocol.ProxyAuthUser) bool { return v.Username == name })
					if u.Password == "" && a.original == "" && i < 0 {
						a.err = "Password is required"
						return nil
					}
					if u.Password == "" && a.original != "" && i < 0 {
						a.edit = ""
						return nil
					}
					if i >= 0 {
						if u.Password == "" {
							u.Password = a.draft.Users[i].Password
						}
						a.draft.Users[i] = u
					} else {
						a.draft.Users = append(a.draft.Users, u)
					}
				}
				a.edit = ""
				a.err = ""
				return nil
			}
		}
		var cmd tea.Cmd
		a.inputs[a.input], cmd = a.inputs[a.input].Update(message)
		return cmd
	}
	if !ok {
		return nil
	}
	if key.String() == "esc" {
		epoch := a.epoch + 1
		*a = authUI{epoch: epoch}
		return func() tea.Msg { return ui.InputModeMsg{Mode: ui.InputNavigation} }
	}
	if !a.loaded {
		return nil
	}
	users := m.authUsers()
	n := len(users)
	prefixStart := 4 + n
	resetRow := prefixStart + len(a.draft.BypassPrefixes) + 1
	cycle := func(value string, values []string) string { return values[(slices.Index(values, value)+1)%len(values)] }
	switch key.String() {
	case "up":
		a.manualScroll = false
		a.selected = max(0, a.selected-1)
	case "down":
		a.manualScroll = false
		a.selected = min(len(m.authRows())-1, a.selected+1)
	case "pgup":
		a.manualScroll = true
		a.top = max(0, a.top-max(1, m.height-12))
	case "pgdown":
		a.manualScroll = true
		a.top += max(1, m.height-12)
	case "ctrl+s":
		return m.authSave(false)
	case "ctrl+r":
		if a.reveal {
			a.reveal = false
			a.revealed = nil
			for i := range a.status.Users {
				a.status.Users[i].Password = ""
			}
			return nil
		}
		return m.authLoad(true)
	case "d", "delete":
		if a.selected >= 3 && a.selected < 3+n {
			name := users[a.selected-3].Username
			i := slices.IndexFunc(a.draft.Users, func(u protocol.ProxyAuthUser) bool { return u.Username == name })
			if i < 0 {
				a.err = "Subscription user: add a password override; exclusion is not supported"
				return nil
			}
			a.draft.Users = slices.Delete(a.draft.Users, i, i+1)
			a.reveal = false
			a.revealed = nil
			for j := range a.status.Users {
				a.status.Users[j].Password = ""
			}
			for i := range a.status.Users {
				if a.status.Users[i].Username == name && a.status.Users[i].Source == "overridden" {
					a.status.Users[i].Source = "subscription"
				}
			}
			a.selected = 0
		}
		if a.selected >= prefixStart && a.selected < prefixStart+len(a.draft.BypassPrefixes) {
			i := a.selected - prefixStart
			a.draft.BypassPrefixes = slices.Delete(a.draft.BypassPrefixes, i, i+1)
		}
	case "enter", "space":
		switch {
		case a.selected == 0:
			a.draft.Mode = cycle(a.draft.Mode, []string{"follow", "enabled", "disabled"})
		case a.selected == 1:
			a.draft.BypassMode = cycle(a.draft.BypassMode, []string{"follow", "custom", "required"})
		case a.selected == 2:
			a.draft.LocalBypass = !a.draft.LocalBypass
			a.draft.BypassMode = "custom"
		case a.selected >= 3 && a.selected < 3+n:
			return m.authEdit("user", users[a.selected-3].Username)
		case a.selected == 3+n:
			return m.authEdit("user", "")
		case a.selected >= prefixStart && a.selected < prefixStart+len(a.draft.BypassPrefixes):
			return m.authEdit("prefix", a.draft.BypassPrefixes[a.selected-prefixStart])
		case a.selected == prefixStart+len(a.draft.BypassPrefixes):
			return m.authEdit("prefix", "")
		case a.selected == resetRow:
			return func() tea.Msg {
				return ui.ConfirmationRequestMsg{Title: "Reset Proxy Authentication", Impact: "Clear instance users and bypass rules; restore subscription authentication", Rollback: "Saved overrides will be removed", OnConfirm: func() tea.Msg { return authResetConfirmedMsg{} }}
			}
		default:
			return m.authSave(false)
		}
	}
	return nil
}

type authResetConfirmedMsg struct{}

func (m *Model) authView() string {
	a := &m.auth
	width := max(20, min(82, m.width-6))
	height := max(1, m.height-12)
	lines := []string{m.theme.Title.Render("Proxy Authentication")}
	if a.edit != "" {
		lines = append(lines, a.inputs[0].View())
		if a.edit == "user" {
			lines = append(lines, a.inputs[1].View())
		}
		lines = append(lines, "Enter accept · Tab field · Ctrl+R show/hide password · Esc cancel")
	} else {
		rows := m.authRows()
		a.selected = min(a.selected, len(rows)-1)
		var rendered []string
		var owners []int
		selectedStart, selectedEnd := 0, 1
		for i, row := range rows {
			start := len(rendered)
			parts := strings.Split(ansi.Hardwrap(row, width-4, true), "\n")
			rendered = append(rendered, parts...)
			for range parts {
				owners = append(owners, i)
			}
			if i == a.selected {
				selectedStart, selectedEnd = start, len(rendered)
			}
		}
		if a.manualScroll {
			a.top = min(a.top, max(0, len(rendered)-height))
		} else {
			a.top = ui.EnsureLineVisible(a.top, height, len(rendered), selectedStart, selectedEnd)
		}
		for i, line := range ui.SliceLines(rendered, a.top, height) {
			if owners[i+a.top] == a.selected {
				line = m.theme.RowFocus.Render("› " + line)
			}
			lines = append(lines, line)
		}
		lines = append(lines, "Enter edit · d remove · Ctrl+R reveal · PgUp/PgDn scroll", "Ctrl+S save · Esc cancel · Existing connections remain")
	}
	if !a.loaded {
		lines = append(lines, "Loading…")
	}
	if a.pending {
		lines = append(lines, "Saving…")
	}
	if a.err != "" {
		lines = append(lines, ui.TruncateVisible(a.err, width-4))
	}
	return m.theme.Content.Width(width).Render(strings.Join(lines, "\n"))
}
