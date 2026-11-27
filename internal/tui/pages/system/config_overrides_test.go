package system

import (
	lipgloss "charm.land/lipgloss/v2"
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestConfigOverrides_OpensDedicatedViewWithThreeEditors(t *testing.T) {
	m := New(&fakeClient{}, nil)
	m.SetSize(110, 32)
	m.SetSnapshot(protocol.Status{Capabilities: []string{"config-overrides-v1", protocol.CapabilityProxyAuth}}, protocol.CoreStatus{})
	m.SetMutationsEnabled(true)
	m.focusID = "config-overrides"
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view := m.View()
	for _, want := range []string{"Configuration Overrides", "Authentication", "DNS Override", "YAML fragments"} {
		if !strings.Contains(view, want) {
			t.Fatalf("opening override page: missing %q", want)
		}
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.HelpMode() == "config-overrides" {
		t.Fatal("Escape did not return to System")
	}
}

type overrideRecordingClient struct {
	authRecordingClient
	fail     error
	lines    []protocol.OverridePreviewLine
	saved    protocol.ConfigOverridesStatus
	previews []protocol.ConfigOverridesPreviewRequest
	writes   []protocol.ConfigOverridesUpdateRequest
}

func (c *overrideRecordingClient) ConfigOverrides(context.Context) (protocol.ConfigOverridesStatus, error) {
	return c.saved, nil
}
func (c *overrideRecordingClient) UpdateConfigOverrides(_ context.Context, kind string, r protocol.ConfigOverridesUpdateRequest) (protocol.ConfigOverridesStatus, error) {
	c.writes = append(c.writes, r)
	if c.fail != nil {
		return protocol.ConfigOverridesStatus{}, c.fail
	}
	if kind == "dns" {
		c.saved.Config.DNS = r.DNS
	} else {
		c.saved.Config.Fragments = r.Fragments
	}
	c.saved.Revision++
	return c.saved, nil
}
func (c *overrideRecordingClient) PreviewConfigOverrides(_ context.Context, r protocol.ConfigOverridesPreviewRequest) (protocol.ConfigOverridesPreview, error) {
	c.previews = append(c.previews, r)
	if c.lines != nil {
		return protocol.ConfigOverridesPreview{Revision: c.saved.Revision, Lines: c.lines}, nil
	}
	return protocol.ConfigOverridesPreview{Revision: c.saved.Revision, Lines: []protocol.OverridePreviewLine{{Text: "nameserver: [fixture]", Source: "fixture"}}}, nil
}

func runOverrideCmd(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			runOverrideCmd(t, m, c)
		}
	case ui.PageResultMsg:
		_, next := m.Update(msg.Result)
		runOverrideCmd(t, m, next)
	}
}
func pressOverride(t *testing.T, m *Model, key tea.KeyPressMsg) {
	t.Helper()
	_, c := m.Update(key)
	runOverrideCmd(t, m, c)
}
func openOverrideTest(t *testing.T, c *overrideRecordingClient, category int) *Model {
	m := New(c, func() string { return "op" })
	m.SetSize(112, 32)
	m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityConfigOverrides, protocol.CapabilityProxyAuth}}, protocol.CoreStatus{})
	m.SetMutationsEnabled(true)
	m.focusID = "config-overrides"
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	for range category {
		pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	return m
}
func TestConfigOverrides_DNSFormPreviewsBeforeSavingWholeDraft(t *testing.T) {
	c := &overrideRecordingClient{saved: protocol.ConfigOverridesStatus{Revision: 8}}
	m := openOverrideTest(t, c, 1)
	if !strings.Contains(m.View(), "enhanced-mode") {
		t.Fatalf("DNS editor missing: %s", m.View())
	}
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	pressOverride(t, m, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	if len(c.previews) == 0 || !c.previews[len(c.previews)-1].Full || len(c.writes) != 0 {
		t.Fatalf("previews=%v writes=%v", c.previews, c.writes)
	}
	pressOverride(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if len(c.writes) != 1 || c.writes[0].IfRevision == nil || *c.writes[0].IfRevision != 8 || c.writes[0].DNS["enable"].Bool == nil || !*c.writes[0].DNS["enable"].Bool {
		t.Fatalf("saved=%+v", c.writes)
	}
}

func (c *overrideRecordingClient) PreviewProxyAuth(_ context.Context, _ protocol.ProxyAuthPreviewRequest) (protocol.ProxyAuthPreview, error) {
	return protocol.ProxyAuthPreview{Revision: c.saved.Revision, Lines: []protocol.ProxyAuthPreviewLine{{Field: "authentication", Target: "lab", Source: "Office", Text: "  - lab:••••"}}}, nil
}

func TestConfigOverrides_BothAuthenticationEntriesToggleFullPreview(t *testing.T) {
	for _, entry := range []string{"config-overrides", "proxy-auth"} {
		t.Run(entry, func(t *testing.T) {
			c := &overrideRecordingClient{}
			c.status = protocol.ProxyAuthStatus{Revision: 14, Config: protocol.ProxyAuthConfig{Mode: "follow", BypassMode: "follow"}}
			c.saved.Revision = 14
			m := New(c, nil)
			m.SetSize(140, 45)
			m.SetMutationsEnabled(true)
			m.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityConfigOverrides, protocol.CapabilityProxyAuth, protocol.CapabilityProxyAuthPreview}}, protocol.CoreStatus{})
			m.focusID = entry
			pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			if entry == "config-overrides" {
				pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			if !strings.Contains(m.View(), "Office") {
				t.Fatalf("missing fragment source in %s", entry)
			}
			pressOverride(t, m, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
			if len(c.previews) == 0 {
				t.Fatal("full authentication preview was not requested")
			}
			req := c.previews[len(c.previews)-1]
			if req.Category != "auth" || req.Authentication == nil || !req.Full || req.Reveal {
				t.Fatalf("full request=%+v", req)
			}
			if !strings.Contains(m.View(), "Ctrl+O Focused preview") {
				t.Fatal("missing return shortcut")
			}
		})
	}
}

func TestConfigOverrides_ImportPasteReorderAndToggleOneListSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Lab.yaml")
	content := "# source comment\r\ndns:\r\n  enable: true\r\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	c := &overrideRecordingClient{saved: protocol.ConfigOverridesStatus{Revision: 3, Config: protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{{ID: "old", Name: "Home", Enabled: true, YAML: "rules: []\n"}}}}}
	m := openOverrideTest(t, c, 2)
	pressOverride(t, m, tea.KeyPressMsg{Code: 'i'})
	paste := func(s string) { _, cmd := m.Update(tea.PasteMsg{Content: s}); runOverrideCmd(t, m, cmd) }
	paste(path)
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(m.View(), "Lab.yaml") {
		t.Fatalf("import: %s", m.View())
	}
	pressOverride(t, m, tea.KeyPressMsg{Code: 'K', Text: "K"})
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeySpace})
	pressOverride(t, m, tea.KeyPressMsg{Code: 'r'})
	// Rename without touching the imported YAML.
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
	paste(" edited")
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	pressOverride(t, m, tea.KeyPressMsg{Code: 'a'})
	paste("DNS")
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	paste("dns:\n  nameserver: [1.1.1.1]\n")
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if len(c.writes) != 0 {
		t.Fatalf("draft changed persisted list: %v", c.writes)
	}
	pressOverride(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	f := c.writes[0].Fragments
	if len(f) != 3 || f[0].YAML != content || f[0].Enabled || f[0].Name != "Lab.yaml edited" || f[1].ID != "old" || f[2].YAML != "dns:\n  nameserver: [1.1.1.1]\n" {
		t.Fatalf("saved fragments=%#v", f)
	}
}

func TestConfigOverrides_FailedSaveKeepsEditedYAML(t *testing.T) {
	c := &overrideRecordingClient{fail: errors.New("invalid fragment"), saved: protocol.ConfigOverridesStatus{Revision: 3}}
	m := openOverrideTest(t, c, 2)
	pressOverride(t, m, tea.KeyPressMsg{Code: 'a'})
	_, cmd := m.Update(tea.PasteMsg{Content: "Fixture"})
	runOverrideCmd(t, m, cmd)
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	_, cmd = m.Update(tea.PasteMsg{Content: "dns: [bad]"})
	runOverrideCmd(t, m, cmd)
	pressOverride(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if !strings.Contains(m.View(), "invalid fragment") {
		t.Fatalf("missing failure: %s", m.View())
	}
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(m.View(), "dns: [bad]") {
		t.Fatalf("draft discarded: %s", m.View())
	}
}

func TestConfigOverrides_OpenTextEditorKeepsViewportOnResize(t *testing.T) {
	c := &overrideRecordingClient{saved: protocol.ConfigOverridesStatus{Config: protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{{ID: "test", Name: "test", YAML: "dns: {enable: true}", Enabled: true}}}}}
	m := openOverrideTest(t, c, 2)
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m.SetSize(56, 19)
	if w, h := lipgloss.Width(m.View()), lipgloss.Height(m.View()); w > 56 || h > 19 {
		t.Fatalf("editor outgrew resize: %dx%d", w, h)
	}
}

func TestConfigOverrides_FullDNSPreviewLocatesFocusedField(t *testing.T) {
	c := &overrideRecordingClient{saved: protocol.ConfigOverridesStatus{Revision: 3, ActiveName: "Office"}}
	for i := 0; i < 60; i++ {
		c.lines = append(c.lines, protocol.OverridePreviewLine{Field: "proxies", Text: "- proxy"})
	}
	c.lines = append(c.lines, protocol.OverridePreviewLine{Field: "dns.enable", Text: "  enable: true"})
	m := openOverrideTest(t, c, 1)
	m.SetSize(100, 16)
	pressOverride(t, m, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	view := m.View()
	if !strings.Contains(view, "enable: true") || !strings.Contains(view, "Office") {
		t.Fatalf("full preview lost selection or subscription: %s", view)
	}
}

func TestConfigOverrides_ConflictRetainsDraftForReviewedRetry(t *testing.T) {
	c := &overrideRecordingClient{saved: protocol.ConfigOverridesStatus{Revision: 3}, fail: protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "changed"}}
	m := openOverrideTest(t, c, 1)
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	c.saved.Revision = 4
	pressOverride(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	c.fail = nil
	pressOverride(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	req := c.writes[len(c.writes)-1]
	if *req.IfRevision != 4 || req.DNS["enable"].Bool == nil || !*req.DNS["enable"].Bool {
		t.Fatalf("retry lost draft or fresh revision: %+v", req)
	}
}

func TestConfigOverrides_RawYAMLPasteAndUnchangedEditorKeepFormatting(t *testing.T) {
	c := &overrideRecordingClient{saved: protocol.ConfigOverridesStatus{Revision: 3, Config: protocol.ConfigOverrides{Fragments: []protocol.OverrideFragment{{ID: "f", Name: "Example", Enabled: true}}}}}
	m := openOverrideTest(t, c, 2)
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	text := "# original\r\nexample: |\r\n  spaces\tthen tab\r\n"
	_, cmd := m.Update(tea.PasteMsg{Content: text})
	runOverrideCmd(t, m, cmd)
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	pressOverride(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	pressOverride(t, m, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if got := c.writes[0].Fragments[0].YAML; got != text {
		t.Fatalf("source changed: %q", got)
	}
}

func TestConfigOverrides_AuthenticationConflictKeepsUnsavedCredentials(t *testing.T) {
	c := &authRecordingClient{status: protocol.ProxyAuthStatus{Revision: 8, Config: protocol.ProxyAuthConfig{Mode: "follow", BypassMode: "follow"}}}
	m := New(c, nil)
	m.auth = authUI{open: true, loaded: true, epoch: 2, status: protocol.ProxyAuthStatus{Revision: 7}, draft: protocol.ProxyAuthConfig{Mode: "enabled", Users: []protocol.ProxyAuthUser{{Username: "lab", Password: "pending-fixture"}}}}
	runOverrideCmd(t, m, m.handleAuthResult(authResultMsg{epoch: 2, mutation: true, err: protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "changed"}}))
	if len(m.auth.draft.Users) != 1 || m.auth.draft.Users[0].Password != "pending-fixture" || m.auth.status.Revision != 8 {
		t.Fatal("conflict lost the authentication draft")
	}
}
