package system

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestProxyAuthDesign_ModePickerRequiresConfirmation(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSize(160, 60)
	m.auth = authUI{open: true, loaded: true, draft: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "custom"}}
	closedHeight := lipgloss.Height(m.authView())
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.auth.draft.Mode != "enabled" {
		t.Fatal("opening picker changed the draft")
	}
	view := ansi.Strip(m.authView())
	if lipgloss.Height(view) != closedHeight {
		t.Fatal("mode popup expanded the dialog")
	}
	for _, label := range []string{"Follow subscription", "Enabled", "Disabled", "Use the subscription's users and passwords unchanged."} {
		if !strings.Contains(view, label) {
			t.Errorf("open picker missing %q", label)
		}
	}
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyDown})
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.auth.open || m.auth.draft.Mode != "enabled" {
		t.Fatal("Escape applied mode or closed the dialog")
	}
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyDown})
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.auth.draft.Mode != "disabled" {
		t.Fatal("confirmed choice was not applied")
	}
}

type authSourceClient struct {
	authRecordingClient
	sourceRevision uint64
	requests       []protocol.ProxyAuthPreviewRequest
}

func (c *authSourceClient) PreviewProxyAuth(_ context.Context, r protocol.ProxyAuthPreviewRequest) (protocol.ProxyAuthPreview, error) {
	c.requests = append(c.requests, r)
	return protocol.ProxyAuthPreview{Revision: c.sourceRevision, ActiveID: "source-id", Lines: []protocol.ProxyAuthPreviewLine{
		{Field: "authentication", Target: "alice", Text: "  - alice:********"},
		{Field: "authentication", Target: "bob", Text: "  - bob:********", Change: "+"},
		{Field: "authentication", Target: "removed", Text: "  - removed:********", Change: "-"},
	}}, nil
}
func (c *authSourceClient) Subscriptions(context.Context) (protocol.SubscriptionList, error) {
	return protocol.SubscriptionList{Revision: c.sourceRevision, ActiveID: "source-id", Subscriptions: []protocol.Subscription{{ID: "source-id", Name: "kanata"}}}, nil
}

func TestProxyAuthDesign_SourceNamesRemainAvailableWhenAuthenticationDisabled(t *testing.T) {
	client := &authSourceClient{sourceRevision: 3}
	client.status = protocol.ProxyAuthStatus{Revision: 3, Config: protocol.ProxyAuthConfig{Mode: "disabled", BypassMode: "follow", Users: []protocol.ProxyAuthUser{{Username: "bob"}, {Username: "charlie"}}}}
	m := New(client, nil)
	m.SetSize(170, 70)
	m.status.Capabilities = []string{protocol.CapabilityProxyAuthPreview}
	m.auth = authUI{open: true, epoch: 1}
	result := m.authLoad(false)().(ui.PageResultMsg).Result.(authResultMsg)
	m.handleAuthResult(result)
	users := m.authUsers()
	if len(users) != 3 || users[0].Username != "alice" || users[1].Source != "overridden" || users[2].Source != "added" {
		t.Fatalf("sources=%+v", users)
	}
	view := ansi.Strip(m.authView())
	for _, label := range []string{"Subscription: kanata", "User from subscription: kanata", "Added in Mihari"} {
		if !strings.Contains(view, label) {
			t.Errorf("missing %q", label)
		}
	}
	if len(client.requests) != 1 || client.requests[0].Reveal || client.requests[0].Config.Mode != "follow" {
		t.Fatal("source lookup must not reveal or mutate credentials")
	}
}

func TestProxyAuthDesign_SourceRevisionMismatchDoesNotMislabelUsers(t *testing.T) {
	client := &authSourceClient{sourceRevision: 4}
	client.status = protocol.ProxyAuthStatus{Revision: 3, Config: protocol.ProxyAuthConfig{Mode: "disabled", Users: []protocol.ProxyAuthUser{{Username: "bob"}}}}
	m := New(client, nil)
	m.status.Capabilities = []string{protocol.CapabilityProxyAuthPreview}
	m.auth = authUI{open: true, epoch: 1}
	result := m.authLoad(false)().(ui.PageResultMsg).Result.(authResultMsg)
	m.handleAuthResult(result)
	if m.auth.sourceLoaded || m.auth.sourceErr == "" || len(m.authUsers()) != 1 {
		t.Fatal("stale source was accepted")
	}
	content := m.authParameterContent(70)
	var labels strings.Builder
	for _, line := range content {
		labels.WriteString(line.text)
	}
	if strings.Contains(labels.String(), "Added in Mihari") || !strings.Contains(labels.String(), "membership unavailable") {
		t.Fatal("unknown source presented as added user")
	}
}

func TestProxyAuthDesign_DraftEditsPreserveSavedBaseline(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.auth = authUI{open: true, epoch: 1}
	m.handleAuthResult(authResultMsg{epoch: 1, status: protocol.ProxyAuthStatus{Config: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "custom", Users: []protocol.ProxyAuthUser{{Username: "one"}, {Username: "two"}}}}})
	m.auth.selected = 3
	m.updateAuth(tea.KeyPressMsg{Code: 'd'})
	if len(m.auth.status.Config.Users) != 2 || m.auth.status.Config.Users[0].Username != "one" {
		t.Fatal("draft deletion altered saved baseline")
	}
}

func TestProxyAuthDesign_CompactLayoutAndLocalExplanation(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSize(170, 70)
	m.auth = authUI{open: true, loaded: true, draft: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "custom", Users: []protocol.ProxyAuthUser{{Username: "charlie"}}}}
	view := ansi.Strip(m.authView())
	for _, label := range []string{"Authentication mode", "Bypass mode", "Additional bypass CIDRs", "CREDENTIAL SOURCE", "Added in Mihari"} {
		if !strings.Contains(view, label) {
			t.Errorf("missing %q", label)
		}
	}
	if lipgloss.Height(view) > 36 {
		t.Fatalf("small form expanded to %d lines", lipgloss.Height(view))
	}
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if strings.Contains(line, "Local bypass") && !strings.Contains(line, "Use Local bypass") {
			if i+2 >= len(lines) || !strings.Contains(lines[i+1], "Allow loopback clients to connect without authentication.") || !strings.Contains(lines[i+2], "127.0.0.0/8") {
				t.Fatal("Local bypass must have explanation, then prefix list")
			}
			return
		}
	}
	t.Fatal("Local bypass label missing")
}

func TestProxyAuthDesign_NonCustomBypassIsVisibleButCannotBeChanged(t *testing.T) {
	for _, config := range []protocol.ProxyAuthConfig{{Mode: "enabled", BypassMode: "follow"}, {Mode: "enabled", BypassMode: "required"}, {Mode: "disabled", BypassMode: "custom"}} {
		m := New(&fakeClient{}, nil)
		m.SetSize(160, 60)
		m.auth = authUI{open: true, loaded: true, selected: 2, draft: config}
		m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEnter})
		if m.auth.draft.LocalBypass || m.auth.draft.BypassMode != config.BypassMode {
			t.Fatal("unavailable bypass control mutated settings")
		}
		view := ansi.Strip(m.authView())
		for _, label := range []string{"Local bypass", "Additional bypass CIDRs"} {
			if !strings.Contains(view, label) {
				t.Errorf("hidden %s", label)
			}
		}
	}
}

func TestProxyAuthDesign_NarrowPickerFollowsHighlightedChoice(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSize(44, 20)
	m.auth = authUI{open: true, loaded: true, draft: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "follow"}}
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyDown})
	if !strings.Contains(ansi.Strip(m.authView()), "› Disabled") {
		t.Fatal("highlighted choice is outside the picker viewport")
	}
}

func TestProxyAuthDesign_EveryPreviewPageFitsNarrowViewport(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSize(44, 20)
	m.auth = authUI{open: true, loaded: true, previewFocus: true, draft: protocol.ProxyAuthConfig{Mode: "follow", BypassMode: "follow"}}
	for range 12 {
		m.auth.preview.Lines = append(m.auth.preview.Lines, protocol.ProxyAuthPreviewLine{Text: "authentication: []"})
	}
	for range 15 {
		view := m.authView()
		if lipgloss.Width(view) > 44 || lipgloss.Height(view) > 20 {
			t.Fatalf("scrolled preview exceeds viewport: %dx%d", lipgloss.Width(view), lipgloss.Height(view))
		}
		m.updateAuth(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
}

func TestProxyAuthDesign_NavigationReachesHeaderActionBeforeUserRows(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.auth = authUI{open: true, loaded: true, draft: protocol.ProxyAuthConfig{Mode: "enabled", Users: []protocol.ProxyAuthUser{{Username: "existing"}}}}
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyDown})
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.auth.edit != "user" || m.auth.original != "" {
		t.Fatal("down from Authentication mode did not reach the Add user header action")
	}
}

func TestProxyAuthDesign_BypassPickerDoesNotExpandEmptyForm(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSize(160, 60)
	m.auth = authUI{open: true, loaded: true, selected: 1, draft: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "custom"}}
	height := lipgloss.Height(m.authView())
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEnter})
	if lipgloss.Height(m.authView()) != height {
		t.Fatal("Bypass picker expanded the empty form")
	}
}

func TestProxyAuthDesign_StatusRowsDoNotMakePagingSkipContent(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.auth = authUI{open: true, loaded: true, err: "fixture validation error", draft: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "custom"}}
	for i := range 20 {
		m.auth.draft.Users = append(m.auth.draft.Users, protocol.ProxyAuthUser{Username: strings.Repeat("u", i+1)})
	}
	first := m.authParameterLines(65, 8)
	last := ansi.Strip(first[len(first)-2])
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyPgDown})
	second := m.authParameterLines(65, 8)
	all := m.authParameterContent(65)
	next := ""
	for i, line := range all {
		if ansi.Strip(line.text) == last && i+1 < len(all) {
			next = ansi.Strip(all[i+1].text)
			break
		}
	}
	if next == "" || ansi.Strip(second[0]) != next {
		t.Fatal("paging skipped the first content line after the previous page")
	}
}

func TestProxyAuthDesign_PreviewUsesThemeFocusStyle(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.theme.PreviewFocus = lipgloss.NewStyle().Background(lipgloss.Color("52"))
	m.auth = authUI{loaded: true, draft: protocol.ProxyAuthConfig{Mode: "enabled"}, preview: protocol.ProxyAuthPreview{ActiveID: "fixture", Lines: []protocol.ProxyAuthPreviewLine{{Text: "authentication: []", Field: "authentication"}}}}
	got := strings.Join(m.authPreviewLines(65, 10), "\n")
	if !strings.Contains(got, m.theme.PreviewFocus.Width(65).Render("  authentication: []")) {
		t.Fatal("preview ignored configured theme focus style")
	}
}

func TestProxyAuthDesign_UnavailableCIDRRemovalExplainsReason(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.auth = authUI{open: true, loaded: true, selected: 4, draft: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "follow", BypassPrefixes: []string{"192.0.2.0/24"}}}
	m.updateAuth(tea.KeyPressMsg{Code: 'd'})
	if !strings.Contains(m.auth.err, "Choose Custom") || len(m.auth.draft.BypassPrefixes) != 1 {
		t.Fatal("unavailable removal did not explain why it cannot apply")
	}
}
