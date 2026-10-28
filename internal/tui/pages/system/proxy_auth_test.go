package system

import (
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"context"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"slices"
	"strings"
	"testing"
)

func TestProxyAuth_EntrySharesPortsSection(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityProxyAuth}}, protocol.CoreStatus{})
	ports := m.portRows()
	section := ""
	authSection := ""
	for _, r := range ports {
		if r.id == rowMixed {
			section = r.section
		}
		if r.id == "proxy-auth" {
			authSection = r.section
		}
	}
	if authSection == "" || authSection != section || section != "Ports & Authentication" {
		t.Fatalf("auth=%q ports=%q", authSection, section)
	}
}

func TestProxyAuth_DraftRemovalAndScrollDoNotApply(t *testing.T) {
	client := &authRecordingClient{}
	m := New(client, nil)
	m.SetSize(72, 22)
	m.auth = authUI{open: true, loaded: true, draft: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "custom"}}
	for i := range 25 {
		u := protocol.ProxyAuthUser{Username: fmt.Sprintf("fixture-%02d", i), Source: "added"}
		m.auth.status.Users = append(m.auth.status.Users, u)
		m.auth.draft.Users = append(m.auth.draft.Users, u)
	}
	m.auth.selected = 3
	if command := m.updateAuth(tea.KeyPressMsg{Code: 'd'}); command != nil {
		command()
	}
	if slices.ContainsFunc(m.authUsers(), func(u protocol.ProxyAuthUser) bool { return u.Username == "fixture-00" }) {
		t.Fatal("selected user was not removed")
	}
	if len(m.authUsers()) != 24 {
		t.Fatal("removed instance user remains visible")
	}
	m.auth.selected = len(m.authRows()) - 1
	view := m.authView()
	if !strings.Contains(view, "[ Save ]") || m.auth.top == 0 {
		t.Fatal("actions did not scroll into view")
	}
	if lipgloss.Width(view) > 72 || lipgloss.Height(view) > 22 {
		t.Fatalf("auth dialog exceeds viewport: %dx%d", lipgloss.Width(view), lipgloss.Height(view))
	}
	if m.auth.pending || client.updates != 0 {
		t.Fatal("draft interaction applied config")
	}
}

func TestProxyAuth_NewUserRejectsBlankPasswordBeforeSaving(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.auth = authUI{open: true, loaded: true}
	m.authEdit("user", "")
	m.auth.inputs[0].SetValue("alice")
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(m.auth.draft.Users) != 0 || m.auth.err == "" {
		t.Fatal("blank password was accepted into the draft")
	}
}
func TestProxyAuth_RemovingRevealedOverrideHidesStalePassword(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSize(80, 25)
	m.auth = authUI{open: true, loaded: true, reveal: true, selected: 3, status: protocol.ProxyAuthStatus{Users: []protocol.ProxyAuthUser{{Username: "alice", Password: "stale-override", Source: "overridden"}}}, draft: protocol.ProxyAuthConfig{Mode: "enabled", Users: []protocol.ProxyAuthUser{{Username: "alice"}}}}
	m.updateAuth(tea.KeyPressMsg{Code: 'd'})
	if m.auth.reveal || strings.Contains(m.authView(), "stale-override") {
		t.Fatal("removed override password is still shown as subscription password")
	}
}

type authRecordingClient struct {
	fakeClient
	updates int
	status  protocol.ProxyAuthStatus
}

func (c *authRecordingClient) ProxyAuth(context.Context, bool) (protocol.ProxyAuthStatus, error) {
	return c.status, nil
}
func (c *authRecordingClient) UpdateProxyAuth(context.Context, protocol.ProxyAuthUpdateRequest) (protocol.ProxyAuthStatus, error) {
	c.updates++
	return c.status, nil
}
func TestProxyAuth_RevisionConflictReloadsCurrentSettings(t *testing.T) {
	client := &authRecordingClient{status: protocol.ProxyAuthStatus{Revision: 9, Config: protocol.ProxyAuthConfig{Mode: "follow", BypassMode: "follow"}}}
	m := New(client, nil)
	m.auth = authUI{open: true, loaded: true, epoch: 1, status: protocol.ProxyAuthStatus{Revision: 3}}
	command := m.handleAuthResult(authResultMsg{epoch: 1, mutation: true, err: protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "changed"}})
	if command == nil {
		t.Fatal("conflicted dialog cannot reload its revision")
	}
	message := command().(ui.PageResultMsg).Result.(authResultMsg)
	m.handleAuthResult(message)
	if m.auth.status.Revision != 9 || m.auth.err == "" {
		t.Fatal("current settings not reloaded for review")
	}
}

func TestProxyAuth_ExplicitRevealIncludesInactiveSavedUsers(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSize(90, 28)
	m.auth = authUI{open: true, loaded: true, epoch: 1, draft: protocol.ProxyAuthConfig{Mode: "disabled", BypassMode: "follow", Users: []protocol.ProxyAuthUser{{Username: "alice"}}}}
	m.handleAuthResult(authResultMsg{epoch: 1, reveal: true, status: protocol.ProxyAuthStatus{Config: protocol.ProxyAuthConfig{Users: []protocol.ProxyAuthUser{{Username: "alice", Password: "saved-secret"}}}}})
	if !strings.Contains(m.authView(), "saved-secret") {
		t.Fatal("explicit reveal omitted inactive saved credential")
	}
}

func TestProxyAuth_RevealIncludesSavedAndEffectiveCredentialsInFollowMode(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSize(90, 28)
	m.auth = authUI{open: true, loaded: true, epoch: 1, draft: protocol.ProxyAuthConfig{Mode: "follow", Users: []protocol.ProxyAuthUser{{Username: "alice"}}}}
	m.handleAuthResult(authResultMsg{epoch: 1, reveal: true, status: protocol.ProxyAuthStatus{Users: []protocol.ProxyAuthUser{{Username: "alice", Password: "subscription-secret", Source: "subscription"}}, Config: protocol.ProxyAuthConfig{Users: []protocol.ProxyAuthUser{{Username: "alice", Password: "saved-secret"}}}}})
	view := m.authView()
	if !strings.Contains(view, "saved: saved-secret") || !strings.Contains(view, "effective (subscription): subscription-secret") {
		t.Fatal("reveal omitted the saved or effective credential and its source")
	}
}

func TestProxyAuth_LongRevealedCredentialsWrapScrollAndHide(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSize(44, 20)
	m.auth = authUI{open: true, loaded: true, epoch: 1, draft: protocol.ProxyAuthConfig{Mode: "follow", Users: []protocol.ProxyAuthUser{{Username: "alice"}}}}
	m.handleAuthResult(authResultMsg{epoch: 1, reveal: true, status: protocol.ProxyAuthStatus{
		Users:  []protocol.ProxyAuthUser{{Username: "alice", Password: strings.Repeat("e", 220) + "effective-tail", Source: "subscription"}},
		Config: protocol.ProxyAuthConfig{Users: []protocol.ProxyAuthUser{{Username: "alice", Password: strings.Repeat("s", 220) + "saved-tail"}}},
	}})
	var views strings.Builder
	for range 12 {
		view := m.authView()
		if lipgloss.Width(view) > 44 || lipgloss.Height(view) > 20 {
			t.Fatal("revealed credentials exceed viewport")
		}
		views.WriteString(view)
		m.updateAuth(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	visible := strings.Join(strings.Fields(ansi.Strip(views.String())), "")
	if !strings.Contains(visible, "saved-tail") || !strings.Contains(visible, "effective-tail") {
		t.Fatal("long saved/effective credentials cannot be scrolled into view")
	}
	m.updateAuth(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if m.auth.reveal || m.auth.revealed != nil {
		t.Fatal("hide retained revealed credentials")
	}
}
