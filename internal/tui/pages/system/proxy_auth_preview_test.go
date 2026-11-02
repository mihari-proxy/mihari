package system

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestProxyAuthPreview_DialogBorderAndModeDescriptions(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSize(150, 38)
	m.auth = authUI{open: true, loaded: true, draft: protocol.ProxyAuthConfig{Mode: "disabled", BypassMode: "follow"}}
	view := ansi.Strip(m.authView())
	for _, want := range []string{"╭", "╯", "Preview only", "Authentication disabled", "local YAML file in Subscriptions"} {
		if !strings.Contains(view, want) {
			t.Errorf("dialog missing %q", want)
		}
	}
	if !strings.Contains(view, "Use skip-auth-prefixes from the active subscription.") {
		t.Fatal("bypass mode explanation missing")
	}
}

func TestProxyAuthPreview_OlderDaemonKeepsEditingWithoutPreviewRequests(t *testing.T) {
	m := New(&authPreviewRecordingClient{}, nil)
	m.auth = authUI{open: true, loaded: true}
	if command := m.scheduleAuthPreview(); command != nil || !strings.Contains(m.auth.previewErr, "Update") {
		t.Fatal("older daemon received an unsupported preview request")
	}
}

type authPreviewRecordingClient struct {
	authRecordingClient
	previews []protocol.ProxyAuthPreviewRequest
}

func newAuthPreviewTestModel(client *authPreviewRecordingClient) *Model {
	m := New(client, nil)
	m.status.Capabilities = []string{protocol.CapabilityProxyAuthPreview}
	return m
}

func (c *authPreviewRecordingClient) PreviewProxyAuth(_ context.Context, request protocol.ProxyAuthPreviewRequest) (protocol.ProxyAuthPreview, error) {
	c.previews = append(c.previews, request)
	return protocol.ProxyAuthPreview{Revision: 3, Lines: []protocol.ProxyAuthPreviewLine{{Text: "authentication: []", Field: "authentication"}}}, nil
}

func TestProxyAuthPreview_LiveEditCancelAndLatestRequest(t *testing.T) {
	c := &authPreviewRecordingClient{}
	m := newAuthPreviewTestModel(c)
	m.auth = authUI{open: true, loaded: true, epoch: 1, status: protocol.ProxyAuthStatus{Revision: 3}, draft: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "follow"}}
	m.authEdit("user", "")
	oldSequence := m.auth.previewSeq
	m.auth.inputs[0].SetValue("fixture")
	m.auth.input = 1
	m.auth.inputs[1].Focus()
	m.updateAuth(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.auth.previewSeq <= oldSequence || len(m.auth.draft.Users) != 0 {
		t.Fatal("typing did not schedule an isolated preview")
	}
	if m.loadAuthPreview(authPreviewDueMsg{epoch: 1, sequence: oldSequence}) != nil {
		t.Fatal("superseded timer sent a request")
	}
	command := m.loadAuthPreview(authPreviewDueMsg{epoch: 1, sequence: m.auth.previewSeq})
	if command == nil {
		t.Fatal("latest draft cannot be previewed")
	}
	result := command().(ui.PageResultMsg).Result.(authPreviewResultMsg)
	if len(c.previews) != 1 || len(c.previews[0].Config.Users) != 1 || c.previews[0].Config.Users[0].Password != "x" {
		t.Fatalf("requests=%+v", c.previews)
	}
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.handleAuthPreview(result)
	if len(m.auth.preview.Lines) != 0 || len(m.auth.draft.Users) != 0 {
		t.Fatal("cancel accepted an obsolete edit preview")
	}
	if c.updates != 0 {
		t.Fatal("preview applied the draft")
	}
}

func TestProxyAuthPreview_RevealDuringEditingSynchronizesBothPanes(t *testing.T) {
	m := newAuthPreviewTestModel(&authPreviewRecordingClient{})
	m.auth = authUI{open: true, loaded: true, epoch: 1, draft: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "follow"}}
	m.authEdit("user", "")
	m.updateAuth(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if !m.auth.reveal {
		t.Fatal("input reveal did not reveal the preview")
	}
	m.auth.preview.Lines = []protocol.ProxyAuthPreviewLine{{Text: "fixture:synthetic-secret"}}
	oldSequence := m.auth.previewSeq
	m.updateAuth(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if m.auth.reveal || len(m.auth.preview.Lines) != 0 || m.auth.previewSeq <= oldSequence {
		t.Fatal("hiding retained a plaintext preview")
	}
	m.handleAuthResult(authResultMsg{epoch: 1, reveal: true, revealSequence: m.auth.revealSequence - 1, status: protocol.ProxyAuthStatus{Users: []protocol.ProxyAuthUser{{Username: "fixture", Password: "late-secret"}}}})
	if m.auth.reveal || len(m.auth.revealed) != 0 {
		t.Fatal("late reveal response undid hide")
	}
}

func TestProxyAuthPreview_StaleErrorAndSourceRevisionAreVisible(t *testing.T) {
	m := newAuthPreviewTestModel(&authPreviewRecordingClient{})
	m.SetSize(140, 35)
	m.auth = authUI{open: true, loaded: true, epoch: 2, previewSeq: 3, status: protocol.ProxyAuthStatus{Revision: 4}, draft: protocol.ProxyAuthConfig{Mode: "follow"}}
	m.auth.preview.Lines = []protocol.ProxyAuthPreviewLine{{Text: "# old preview"}}
	m.handleAuthPreview(authPreviewResultMsg{epoch: 2, sequence: 2, value: protocol.ProxyAuthPreview{Revision: 4}})
	if len(m.auth.preview.Lines) != 1 {
		t.Fatal("obsolete response was consumed")
	}
	m.handleAuthPreview(authPreviewResultMsg{epoch: 2, sequence: 3, err: errors.New("invalid CIDR fixture")})
	if !strings.Contains(ansi.Strip(m.authView()), "stale") {
		t.Fatal("invalid draft silently shows old preview")
	}
	m.handleAuthPreview(authPreviewResultMsg{epoch: 2, sequence: 3, value: protocol.ProxyAuthPreview{Revision: 5}})
	if !strings.Contains(m.auth.previewErr, "changed") {
		t.Fatal("source revision change was accepted")
	}
}

func TestProxyAuthPreview_StatusReloadDoesNotRetainRevealedPreview(t *testing.T) {
	m := newAuthPreviewTestModel(&authPreviewRecordingClient{})
	m.auth = authUI{open: true, loaded: true, reveal: true, epoch: 1, previewSeq: 1, status: protocol.ProxyAuthStatus{Revision: 3}}
	m.handleAuthPreview(authPreviewResultMsg{epoch: 1, sequence: 1, reveal: true, value: protocol.ProxyAuthPreview{Revision: 3, Lines: []protocol.ProxyAuthPreviewLine{{Text: "fixture:synthetic-secret"}}}})
	m.handleAuthResult(authResultMsg{epoch: 1, status: protocol.ProxyAuthStatus{Revision: 3, Config: protocol.ProxyAuthConfig{Mode: "follow", BypassMode: "follow"}}})
	if m.auth.reveal || len(m.auth.preview.Lines) != 0 {
		t.Fatal("status reload hid credentials but retained plaintext preview")
	}
}

func TestProxyAuthPreview_ResponsiveLayoutFocusAndScrolling(t *testing.T) {
	for _, size := range [][2]int{{150, 40}, {80, 28}, {44, 20}} {
		m := newAuthPreviewTestModel(&authPreviewRecordingClient{})
		m.SetSize(size[0], size[1])
		m.auth = authUI{open: true, loaded: true, draft: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "custom"}, preview: protocol.ProxyAuthPreview{ActiveID: "fixture"}}
		for range 40 {
			m.auth.preview.Lines = append(m.auth.preview.Lines, protocol.ProxyAuthPreviewLine{Text: "  - fixture:********", Field: "authentication", Target: "fixture"})
		}
		view := m.authView()
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("viewport %v exceeded: %dx%d", size, lipgloss.Width(view), lipgloss.Height(view))
		}
		m.updateAuth(tea.KeyPressMsg{Code: tea.KeyTab})
		m.updateAuth(tea.KeyPressMsg{Code: tea.KeyPgDown})
		if !m.auth.previewFocus || m.auth.previewTop == 0 || m.auth.selected != 0 {
			t.Fatal("preview navigation changed parameter selection")
		}
		if !m.authPreviewSelected(protocol.ProxyAuthPreviewLine{Field: "authentication"}) || m.authPreviewSelected(protocol.ProxyAuthPreviewLine{Field: "skip-auth-prefixes"}) {
			t.Fatal("mode focus mapped to wrong YAML field")
		}
	}
}

func TestProxyAuthPreview_ParameterNavigationRestoresAutomaticScroll(t *testing.T) {
	m := newAuthPreviewTestModel(&authPreviewRecordingClient{})
	m.auth = authUI{open: true, loaded: true, previewManual: true, draft: protocol.ProxyAuthConfig{Mode: "follow", BypassMode: "follow"}}
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.auth.previewManual {
		t.Fatal("new parameter selection did not restore preview following")
	}
}

func TestProxyAuthPreview_DisconnectMasksBothPanesAndEditInput(t *testing.T) {
	m := newAuthPreviewTestModel(&authPreviewRecordingClient{})
	m.SetSize(140, 35)
	m.mutationsEnabled = true
	m.auth = authUI{open: true, loaded: true, reveal: true, previewRevealed: true, draft: protocol.ProxyAuthConfig{Mode: "enabled"}, preview: protocol.ProxyAuthPreview{Lines: []protocol.ProxyAuthPreviewLine{{Text: "fixture:synthetic-secret"}}}}
	m.authEdit("user", "")
	m.auth.inputs[1].SetValue("synthetic-secret")
	m.SetMutationsEnabled(false)
	if m.auth.reveal || m.auth.previewRevealed || len(m.auth.preview.Lines) != 0 || strings.Contains(ansi.Strip(m.authView()), "synthetic-secret") {
		t.Fatal("disconnect retained revealed credentials")
	}
}

func TestProxyAuthPreview_DeletionInvalidatesPendingReveal(t *testing.T) {
	m := newAuthPreviewTestModel(&authPreviewRecordingClient{})
	m.auth = authUI{open: true, loaded: true, reveal: true, epoch: 1, revealSequence: 3, selected: 3, draft: protocol.ProxyAuthConfig{Mode: "enabled", Users: []protocol.ProxyAuthUser{{Username: "fixture", Password: "synthetic-secret"}}}}
	m.updateAuth(tea.KeyPressMsg{Code: 'd'})
	m.handleAuthResult(authResultMsg{epoch: 1, reveal: true, revealSequence: 3, status: protocol.ProxyAuthStatus{Users: []protocol.ProxyAuthUser{{Username: "fixture", Password: "synthetic-secret"}}}})
	if m.auth.reveal || len(m.auth.revealed) != 0 {
		t.Fatal("late reveal undid deletion's hide")
	}
}

func TestProxyAuthPreview_PreviewPagingPreservesVisibleParameterSelection(t *testing.T) {
	m := newAuthPreviewTestModel(&authPreviewRecordingClient{})
	m.SetSize(140, 35)
	m.auth = authUI{open: true, loaded: true, selected: 3, draft: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "follow", Users: []protocol.ProxyAuthUser{{Username: "fixture"}}}}
	for range 40 {
		m.auth.preview.Lines = append(m.auth.preview.Lines, protocol.ProxyAuthPreviewLine{Text: "authentication: []"})
	}
	m.authView()
	top, selected := m.auth.top, m.auth.selected
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyTab})
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyPgDown})
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyTab})
	view := ansi.Strip(m.authView())
	if m.auth.top != top || m.auth.selected != selected || !strings.Contains(view, "› fixture") {
		t.Fatal("preview paging changed or hid parameter selection")
	}
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.auth.edit != "user" || m.auth.original != "fixture" {
		t.Fatal("visible selection cannot be edited after preview paging")
	}
}
