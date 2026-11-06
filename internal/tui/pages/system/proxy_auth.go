package system

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
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
	picker                                      string
	pickerIndex                                 int
	sourceUsers                                 []protocol.ProxyAuthUser
	sourceID, sourceName, sourceErr             string
	sourceLoaded                                bool
	previewCancel                               context.CancelFunc
	previewRevealed                             bool
	preview                                     protocol.ProxyAuthPreview
	previewSeq                                  uint64
	previewPending, previewFocus, previewManual bool
	previewTop                                  int
	previewPage                                 int
	previewErr                                  string
	parameterPage                               int
	revealSequence                              uint64
	revealed                                    map[string]revealedAuthCredential
	manualScroll                                bool
	open, loaded, pending, reveal               bool
	conflict                                    bool
	epoch                                       uint64
	status                                      protocol.ProxyAuthStatus
	draft                                       protocol.ProxyAuthConfig
	selected, top                               int
	edit, original, err                         string
	inputs                                      [2]textinput.Model
	input                                       int
}
type authResultMsg struct {
	sourceUsers                     []protocol.ProxyAuthUser
	sourceID, sourceName, sourceErr string
	sourceLoaded                    bool
	revealSequence                  uint64
	status                          protocol.ProxyAuthStatus
	epoch                           uint64
	mutation, reveal                bool
	err                             error
}

func (m authResultMsg) Err() error                        { return m.err }
func (m authResultMsg) Warnings() protocol.WarningOutcome { return m.status.WarningOutcome }
func (m *Model) authLoad(reveal bool) tea.Cmd {
	client, ok := m.client.(authClient)
	if !ok {
		return nil
	}
	epoch, revealSequence, ctx := m.auth.epoch, m.auth.revealSequence, m.ctx
	hasPreview := m.hasCapability(protocol.CapabilityProxyAuthPreview)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		v, e := client.ProxyAuth(ctx, reveal)
		result := authResultMsg{status: v, epoch: epoch, revealSequence: revealSequence, reveal: reveal, err: e}
		if e == nil && !reveal {
			loadAuthSource(ctx, client, hasPreview, &result)
		}
		return ui.PageResultMsg{Page: ui.PageSystem, Result: result}
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
	if r.reveal && r.revealSequence != m.auth.revealSequence {
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
		return m.scheduleAuthPreview()
	}
	m.auth.status = r.status
	m.auth.sourceUsers, m.auth.sourceLoaded = r.sourceUsers, r.sourceLoaded
	m.auth.sourceID, m.auth.sourceName, m.auth.sourceErr = r.sourceID, r.sourceName, r.sourceErr
	m.auth.draft = r.status.Config
	m.auth.draft.Users = slices.Clone(r.status.Config.Users)
	m.auth.draft.BypassPrefixes = slices.Clone(r.status.Config.BypassPrefixes)
	m.auth.loaded = true
	m.hideAuthSecrets()
	m.auth.err = ""
	if m.auth.conflict {
		m.auth.conflict = false
		m.auth.err = "Settings changed. Current configuration reloaded; review and reapply your edits."
	}
	if r.mutation {
		return tea.Batch(m.scheduleAuthPreview(), func() tea.Msg { return ui.RuntimeRevisionMsg{Revision: r.status.Revision} })
	}
	return m.scheduleAuthPreview()
}
func (m *Model) authUsers() []protocol.ProxyAuthUser {
	users := []protocol.ProxyAuthUser{}
	base := m.auth.status.Users
	if m.auth.sourceLoaded {
		base = m.auth.sourceUsers
	}
	for _, u := range base {
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
	hasPreview := m.hasCapability(protocol.CapabilityProxyAuthPreview)
	m.auth.pending = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		v, e := client.UpdateProxyAuth(ctx, request)
		result := authResultMsg{status: v, epoch: epoch, mutation: true, err: e}
		if e == nil {
			loadAuthSource(ctx, client, hasPreview, &result)
		}
		return ui.PageResultMsg{Page: ui.PageSystem, Result: result}
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
		if a.reveal {
			a.inputs[1].EchoMode = textinput.EchoNormal
		}
		if name != "" {
			a.input = 1
		}
	}
	if kind == "prefix" {
		a.inputs[0].Placeholder = "CIDR, e.g. 192.0.2.0/24"
		a.inputs[0].SetValue(name)
	}
	return tea.Batch(a.inputs[a.input].Focus(), m.scheduleAuthPreview())
}
func (m *Model) updateAuthControls(message tea.Msg) tea.Cmd {
	a := &m.auth
	key, ok := message.(tea.KeyPressMsg)
	if a.pending {
		return nil
	}
	if a.picker != "" && ok {
		switch key.String() {
		case "esc":
			a.picker = ""
		case "up":
			a.pickerIndex = (a.pickerIndex + 2) % 3
		case "down":
			a.pickerIndex = (a.pickerIndex + 1) % 3
		case "enter", "space":
			choice := authModeOptions(a.picker)[a.pickerIndex].value
			if a.picker == "auth" {
				a.draft.Mode = choice
			} else {
				a.draft.BypassMode = choice
			}
			a.picker = ""
		}
		return nil
	}
	if a.edit != "" {
		if ok {
			switch key.String() {
			case "esc":
				a.edit = ""
				return nil
			case "ctrl+r":
				return m.toggleAuthReveal()
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
	if a.manualScroll && slices.Contains([]string{"enter", "space", "d", "delete"}, key.String()) {
		a.err = "Use Up/Down to select a row before editing or removing it."
		return nil
	}
	users := m.authUsers()
	n := len(users)
	prefixStart := 4 + n
	resetRow := prefixStart + len(a.draft.BypassPrefixes) + 1
	switch key.String() {
	case "up":
		a.manualScroll = false
		a.err = ""
		m.moveAuthSelection(-1)
	case "down":
		a.manualScroll = false
		a.err = ""
		m.moveAuthSelection(1)
	case "pgup":
		a.manualScroll = true
		a.top = max(0, a.top-max(1, a.parameterPage))
	case "pgdown":
		a.manualScroll = true
		a.top += max(1, a.parameterPage)
	case "ctrl+s":
		return m.authSave(false)
	case "ctrl+r":
		return m.toggleAuthReveal()
	case "d", "delete":
		if a.selected >= prefixStart && a.selected <= prefixStart+len(a.draft.BypassPrefixes) && !m.authCustomBypass() {
			a.err = m.authBypassUnavailableReason()
			return nil
		}
		if a.selected >= 3 && a.selected < 3+n {
			name := users[a.selected-3].Username
			i := slices.IndexFunc(a.draft.Users, func(u protocol.ProxyAuthUser) bool { return u.Username == name })
			if i < 0 {
				a.err = "Subscription user: add a password override; exclusion is not supported"
				return nil
			}
			a.draft.Users = slices.Delete(a.draft.Users, i, i+1)
			m.hideAuthSecrets()
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
			m.openAuthMode("auth")
		case a.selected == 1:
			m.openAuthMode("bypass")
		case a.selected == 2:
			if !m.authCustomBypass() {
				a.err = m.authBypassUnavailableReason()
				return nil
			}
			a.draft.LocalBypass = !a.draft.LocalBypass
		case a.selected >= 3 && a.selected < 3+n:
			return m.authEdit("user", users[a.selected-3].Username)
		case a.selected == 3+n:
			return m.authEdit("user", "")
		case a.selected >= prefixStart && a.selected < prefixStart+len(a.draft.BypassPrefixes):
			if !m.authCustomBypass() {
				a.err = m.authBypassUnavailableReason()
				return nil
			}
			return m.authEdit("prefix", a.draft.BypassPrefixes[a.selected-prefixStart])
		case a.selected == prefixStart+len(a.draft.BypassPrefixes):
			if !m.authCustomBypass() {
				a.err = m.authBypassUnavailableReason()
				return nil
			}
			return m.authEdit("prefix", "")
		case a.selected == resetRow:
			return func() tea.Msg {
				return ui.ConfirmationRequestMsg{Title: "Reset Proxy Authentication", Impact: "Clear users and bypass rules saved in Mihari; restore subscription authentication", Rollback: "Saved overrides will be removed", OnConfirm: func() tea.Msg { return authResetConfirmedMsg{} }}
			}
		default:
			return m.authSave(false)
		}
	}
	return nil
}

type authResetConfirmedMsg struct{}

// InvalidatesSnapshots distinguishes credential writes from read-only previews.
func (m authResultMsg) InvalidatesSnapshots() bool { return m.mutation }
