package system

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func feedbackAuthModel() *Model {
	m := New(&authRecordingClient{}, nil)
	m.SetSize(160, 50)
	baseline := protocol.ProxyAuthConfig{Mode: "follow", BypassMode: "follow"}
	m.auth = authUI{open: true, loaded: true, epoch: 1, status: protocol.ProxyAuthStatus{Config: baseline}, draft: baseline}
	return m
}

func TestProxyAuthFeedback_ReplacedPreviewTimerDoesNotEmitObsoleteMessages(t *testing.T) {
	m := newAuthPreviewTestModel(&authPreviewRecordingClient{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.ctx = ctx
	m.auth = authUI{open: true, loaded: true, epoch: 1}
	old := m.scheduleAuthPreview()
	latest := m.scheduleAuthPreview()
	if old() == nil { // The canceled command must finish without publishing a message.
		cancel()
		if latest() != nil {
			t.Fatal("closed preview timer still emitted a UI message")
		}
		return
	}
	t.Fatal("superseded debounce timer still emitted an obsolete UI message")
}

func TestProxyAuthFeedback_EscapeRetainsUnsavedChangesUntilDecision(t *testing.T) {
	m := feedbackAuthModel()
	m.auth.draft.Mode = "disabled"
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.auth.open || m.auth.draft.Mode != "disabled" {
		t.Fatal("Escape discarded an unsaved draft")
	}
	for _, label := range []string{"Save changes?", "Discard", "Keep editing"} {
		if !strings.Contains(ansi.Strip(m.authView()), label) {
			t.Errorf("confirmation missing %q", label)
		}
	}
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.auth.open || m.auth.draft.Mode != "disabled" {
		t.Fatal("dismissing confirmation lost edits")
	}
}

func TestProxyAuthFeedback_DiscardIsExplicitAndDoesNotSave(t *testing.T) {
	m := feedbackAuthModel()
	m.auth.draft.Mode = "disabled"
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.auth.open {
		t.Fatal("Escape closed without an explicit discard")
	}
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyLeft}) // Keep editing -> Discard
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.auth.open {
		t.Fatal("explicit discard did not close the dialog")
	}
	if m.client.(*authRecordingClient).updates != 0 {
		t.Fatal("discard saved a draft")
	}
}

func TestProxyAuthFeedback_SaveFromExitClosesOnlyOnSuccessfulCommit(t *testing.T) {
	m := feedbackAuthModel()
	m.auth.draft.Mode = "disabled"
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyLeft})
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyLeft})
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.auth.open || !m.auth.pending {
		t.Fatal("exit did not wait for the save result")
	}
	m.handleAuthResult(authResultMsg{epoch: m.auth.epoch, mutation: true, status: protocol.ProxyAuthStatus{Config: m.auth.draft}})
	if m.auth.open {
		t.Fatal("successful save did not complete the requested exit")
	}
}

func TestProxyAuthFeedback_CleanEscapeClosesAndDefaultDecisionKeepsEdits(t *testing.T) {
	m := feedbackAuthModel()
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.auth.open {
		t.Fatal("unchanged form asked for confirmation")
	}
	m = feedbackAuthModel()
	m.auth.draft.Mode = "disabled"
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.auth.open || m.auth.exitOpen || m.auth.draft.Mode != "disabled" {
		t.Fatal("default confirmation did not keep editing")
	}
}

func TestProxyAuthFeedback_HideClearsRenderedPlaintextCache(t *testing.T) {
	m := feedbackAuthModel()
	m.auth.reveal = true
	m.auth.preview = protocol.ProxyAuthPreview{ActiveID: "synthetic", Lines: []protocol.ProxyAuthPreviewLine{{Field: "authentication", Text: "  - fixture:synthetic-only"}}}
	m.authPreviewLines(60, 15)
	if !strings.Contains(strings.Join(m.auth.previewRendered, "\n"), "synthetic-only") {
		t.Fatal("fixture did not build the revealed render cache")
	}
	m.hideAuthSecrets()
	if m.auth.previewRendered != nil || m.auth.previewRenderSource != nil {
		t.Fatal("hide retained cached plaintext")
	}
}

func TestProxyAuthFeedback_EscapeCancelsEditorBeforeAuthenticationExit(t *testing.T) {
	for _, editor := range []struct{ name, kind, original, value string }{
		{"new user", "user", "", "synthetic-user"},
		{"password override", "user", "existing-user", "synthetic-password"},
		{"bypass CIDR", "prefix", "", "192.0.2.0/24"},
	} {
		for _, dirty := range []bool{false, true} {
			name := editor.name + "/clean draft"
			if dirty {
				name = editor.name + "/dirty draft"
			}
			t.Run(name, func(t *testing.T) {
				m := feedbackAuthModel()
				if dirty {
					m.auth.draft.Mode = "disabled"
				}
				draft := m.auth.draft
				m.authEdit(editor.kind, editor.original)
				m.auth.inputs[m.auth.input].SetValue(editor.value)
				m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEscape})
				if !m.auth.open || m.auth.edit != "" || m.auth.exitOpen {
					t.Fatal("first Escape must cancel only the active editor")
				}
				if !reflect.DeepEqual(m.auth.draft, draft) {
					t.Fatal("canceling the editor changed previously accepted authentication edits")
				}
				if strings.Contains(ansi.Strip(m.authView()), "Save changes?") {
					t.Fatal("canceling the editor showed the authentication exit confirmation")
				}
				m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEscape})
				if dirty {
					if !m.auth.open || !m.auth.exitOpen {
						t.Fatal("second Escape must protect the dirty authentication draft")
					}
				} else if m.auth.open {
					t.Fatal("second Escape must close unchanged authentication")
				}
				if m.client.(*authRecordingClient).updates != 0 {
					t.Fatal("canceling saved authentication")
				}
			})
		}
	}
}

func TestProxyAuthFeedback_SaveFromExitFailureKeepsDraft(t *testing.T) {
	m := feedbackAuthModel()
	m.auth.draft.Mode = "disabled"
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEscape})
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyLeft})
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyLeft})
	m.updateAuth(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.auth.pending {
		t.Fatal("Save from exit confirmation did not start a save")
	}
	m.handleAuthResult(authResultMsg{epoch: m.auth.epoch, mutation: true, err: errors.New("synthetic save failure")})
	if !m.auth.open || m.auth.draft.Mode != "disabled" {
		t.Fatal("failed save closed or discarded the draft")
	}
}

func TestProxyAuthFeedback_ErrorWrapsBelowTitleAndFooterFollowsReset(t *testing.T) {
	m := feedbackAuthModel()
	m.auth.err = strings.Repeat("synthetic error detail ", 8) + "complete-error-tail"
	lines := strings.Split(ansi.Strip(m.authView()), "\n")
	title, reset, cancel, save := -1, -1, -1, -1
	for i, line := range lines {
		if strings.Contains(line, "Proxy Authentication") {
			title = i
			if !strings.Contains(line, "Failed") {
				t.Error("error badge is not beside the title")
			}
		}
		if strings.Contains(line, "[ Reset authentication ]") {
			reset = i
		}
		if strings.Contains(line, "[Cancel]") {
			cancel = i
			if !strings.Contains(line, "Esc") {
				t.Error("Cancel shortcut missing")
			}
		}
		if strings.Contains(line, "[Save]") {
			save = i
			if !strings.Contains(line, "Ctrl+S") {
				t.Error("Save shortcut missing")
			}
		}
	}
	if title < 0 || !strings.Contains(lines[title+1], "synthetic error detail") {
		t.Fatal("error does not start below the title")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "complete-error-tail") {
		t.Fatal("wrapped error was truncated")
	}
	if reset < 0 || cancel <= reset || save <= reset {
		t.Fatal("Cancel/Save must be below Reset authentication")
	}
}

func TestProxyAuthFeedback_SaveUsesAnimatedSharedBadge(t *testing.T) {
	m := feedbackAuthModel()
	m.authSave(false)
	if !m.rowSpinning {
		t.Fatal("saving did not schedule animation")
	}
	m.rowSpinClock = time.Unix(0, 0)
	first := m.authView()
	badge := ui.RenderStatusChip(m.theme, ui.StatusChipPending, ui.SpinnerLabel(m.rowSpinClock, "Saving"))
	if !strings.Contains(first, badge) {
		t.Fatal("save does not use the shared Saving badge")
	}
	m.rowSpinClock = m.rowSpinClock.Add(100 * time.Millisecond)
	if first == m.authView() {
		t.Fatal("Saving badge frame does not advance")
	}
}
