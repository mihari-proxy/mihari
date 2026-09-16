package system

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"strings"
	"testing"
)

func TestSystemLocalFailures_PreserveCauseForGlobalDetails(t *testing.T) {
	for _, kind := range []string{"clipboard", "browser", "channel_path", "channel_read"} {
		t.Run(kind, func(t *testing.T) {
			cause := errors.New("token=fixture-original /private/channel.json")
			model := New(nil, nil)
			model.channelPath = func() (string, error) { return "fixture-channel", nil }
			model.loadChannel = func(string) (string, error) { return "main", nil }
			updater := &fakeSelfUpdater{}
			model.SetSelfUpdater(updater, "fixture-version", "fixture-binary", func() bool { return false })
			var cmd tea.Cmd
			switch kind {
			case "clipboard":
				model.writeClipboard = func(string) error { return cause }
				cmd = model.copyDirectoryRow(rowLogDirectory, "fixture-path")
			case "browser":
				model.SetOpenBrowser(func(string) error { return cause })
				cmd = model.openGitHub()
			case "channel_path":
				model.channelPath = func() (string, error) { return "", cause }
				cmd = model.checkMihariVersion()
			case "channel_read":
				model.loadChannel = func(string) (string, error) { return "", cause }
				cmd = model.checkMihariVersion()
			}
			if cmd == nil {
				t.Fatal("original local failure was discarded")
			}
			msg, ok := cmd().(ui.DiagnosticMsg)
			if !ok || msg.Page != ui.PageSystem || !errors.Is(msg.Err, cause) {
				t.Fatal("diagnostic message lost original cause or page")
			}
			if updater.checkCalls != 0 {
				t.Fatal("failed precondition still started update check")
			}
			if got := diagnostics.Capture(msg.Err); got.Text != cause.Error() {
				t.Fatal("diagnostic detail missing")
			}
		})
	}
}

func TestSystemChannelDiscoveryFailure_ReachesShellWithoutLogger(t *testing.T) {
	for _, kind := range []string{"discovery", "path", "read"} {
		t.Run(kind, func(t *testing.T) {
			cause := errors.New("channel token=fixture-discovery")
			m := New(nil, nil)
			m.channelPath = func() (string, error) { return "fixture-channel", nil }
			m.loadChannel = func(string) (string, error) { return "main", nil }
			switch kind {
			case "discovery":
				m.selfUpdateChannel = func(context.Context) (string, error) { return "", cause }
			case "path":
				m.channelPath = func() (string, error) { return "", cause }
			case "read":
				m.loadChannel = func(string) (string, error) { return "", cause }
			}
			_ = m.View()
			_, cmd := m.Update(nil)
			if cmd == nil {
				t.Fatal("channel discovery failed without a shell diagnostic")
			}
			result, ok := cmd().(ui.DiagnosticMsg)
			if !ok || result.Page != ui.PageSystem || !errors.Is(result.Err, cause) {
				t.Fatalf("diagnostic = %#v", result)
			}
			_ = m.View()
			_, cmd = m.Update(nil)
			if cmd != nil {
				t.Fatal("cached channel failure published repeatedly")
			}
		})
	}
}

func TestSystemValidation_RetainsOriginalInputFailure(t *testing.T) {
	for _, kind := range []string{"size", "files", "ports"} {
		t.Run(kind, func(t *testing.T) {
			m := New(nil, nil)
			m.editInput = textinput.New()
			m.editInput.SetValue("token=fixture-validation")
			var cmd tea.Cmd
			if kind == "ports" {
				m.editID = rowMixed
				m.onboarding = protocol.OnboardingStatus{MixedAddr: "127.0.0.1:7890", ControllerAddr: "127.0.0.1:9090", WebAddr: "127.0.0.1:8080"}
				cmd = m.confirmPortEdit()
			} else {
				m.editID = rowLogMaxSize
				if kind == "files" {
					m.editID = rowLogMaxFiles
				}
				cmd = m.confirmLoggingEdit()
			}
			if cmd == nil {
				t.Fatal("validation did not publish details")
			}
			msg, ok := cmd().(ui.DiagnosticMsg)
			if !ok || msg.Err == nil || !strings.Contains(diagnostics.Capture(msg.Err).Text, "fixture-validation") {
				t.Fatal("validation cause missing")
			}
			if m.editID == "" || m.pending {
				t.Fatal("invalid edit was cleared or submitted")
			}
		})
	}
}

func TestSystemPortProbe_ReportsOriginalFailure(t *testing.T) {
	m := New(nil, nil)
	m.onboarding.MixedAddr = "token=fixture-invalid-endpoint"
	result := m.probePortHolds()()
	failures, ok := result.(interface{ DiagnosticErrors() []error })
	if !ok || len(failures.DiagnosticErrors()) != 1 {
		t.Fatal("port probe discarded its original failure")
	}
	if !strings.Contains(diagnostics.Capture(failures.DiagnosticErrors()[0]).Text, m.onboarding.MixedAddr) {
		t.Fatal("port probe lost original endpoint")
	}
	source, ok := result.(interface{ DiagnosticPage() ui.PageID })
	if !ok || source.DiagnosticPage() != ui.PageSystem {
		t.Fatal("port probe lost page source")
	}
}

func TestSystemLocalRejection_IsInspectable(t *testing.T) {
	m := New(nil, nil)
	cmd := m.previewCompleteUninstall()
	if cmd == nil {
		t.Fatal("unavailable uninstaller has no diagnostic")
	}
	result, ok := cmd().(ui.DiagnosticMsg)
	if !ok || result.Err == nil || result.Page != ui.PageSystem {
		t.Fatal("unavailable uninstaller lost its failure")
	}
}

func TestSystemErrorSummary_EscapesTerminalControls(t *testing.T) {
	cause := errors.New("uninstall token=fixture\x1b[2J\x07")
	text := uninstallPreviewDetail(cause)
	if strings.ContainsAny(text, "\x1b\x07") || !strings.Contains(text, "token=fixture") {
		t.Fatal("uninstall summary executes controls or hides original text")
	}
	m := New(nil, nil)
	m.markRowOutcome(rowCompleteUninstall, false, cause.Error())
	if strings.ContainsAny(m.outcomeDetail, "\x1b\x07") || !strings.Contains(m.outcomeDetail, "token=fixture") {
		t.Fatal("row summary executes controls or hides original text")
	}
}
