package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"encoding/json"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	setuppage "github.com/mihari-proxy/mihari/internal/tui/pages/setup"
	subscriptionspage "github.com/mihari-proxy/mihari/internal/tui/pages/subscriptions"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	webguipage "github.com/mihari-proxy/mihari/internal/tui/pages/webgui"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPageReads_OriginalFailuresReachGlobalHistory(t *testing.T) {
	for _, tc := range []struct {
		page  ui.PageID
		count int
	}{{ui.PageSetup, 2}, {ui.PageSystem, 4}, {ui.PageWebGUI, 1}, {ui.PageRules, 1}, {ui.PageSubscriptions, 1}} {
		t.Run(string(tc.page), func(t *testing.T) {
			calls := 0
			client := controlclient.NewHTTP("http://mihari", "fixture-credential", &http.Client{Transport: preparedHTTPTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if tc.page == ui.PageSetup && r.URL.Path == "/v1/onboarding" {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"schema":"mihari/v1","revision":1}`))}, nil
				}
				snapshot := protocol.Diagnostic{ID: "fixture:" + r.URL.Path, State: protocol.DiagnosticAvailable, Summary: "fixture read failed", Detail: "original token=fixture-" + r.URL.Path}
				payload, err := json.Marshal(protocol.ErrorEnvelope{Schema: "mihari.error/v1", Error: protocol.APIError{Code: protocol.CodeDataFailure, Message: snapshot.Summary, Diagnostic: &snapshot}})
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(payload)))}, nil
			})})
			model := newModelWithClientContext(context.Background(), nil, client)
			model.active = tc.page
			var cmd tea.Cmd
			switch tc.page {
			case ui.PageSetup:
				cmd = model.pages[tc.page].(*setuppage.Model).Load()
			case ui.PageSystem:
				page := model.pages[tc.page].(*systempage.Model)
				page.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityOnboarding, protocol.CapabilitySystemProxy, protocol.CapabilityTUN, protocol.CapabilityWebGUI}}, protocol.CoreStatus{})
				cmd = page.Load()
			case ui.PageWebGUI:
				page := model.pages[tc.page].(*webguipage.Model)
				page.SetCapabilities([]string{protocol.CapabilityWebGUI})
				cmd = page.Load()
			case ui.PageRules:
				_, cmd = model.pages[tc.page].Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
			case ui.PageSubscriptions:
				page := model.pages[tc.page].(*subscriptionspage.Model)
				page.SetSubscriptions(protocol.SubscriptionList{Subscriptions: []protocol.Subscription{{ID: "fixture", Name: "Fixture"}}})
				page.FocusFirst()
				_, cmd = page.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			var deliver func(tea.Cmd)
			deliver = func(command tea.Cmd) {
				if command == nil {
					return
				}
				result := command()
				if batch, ok := result.(tea.BatchMsg); ok {
					for _, child := range batch {
						deliver(child)
					}
					return
				}
				model.active = ui.PageOverview
				next, _ := model.Update(result)
				model = next.(Model)
			}
			deliver(cmd)
			if len(model.diagnosticWindow.entries) != tc.count {
				t.Fatalf("%s reads=%d history=%d want=%d", tc.page, calls, len(model.diagnosticWindow.entries), tc.count)
			}
			for _, entry := range model.diagnosticWindow.entries {
				if entry.page != tc.page || !strings.Contains(entry.snapshot.Detail, "original token=fixture-") {
					t.Fatal("page or raw original cause lost")
				}
			}
			model.active = tc.page
			next, _ := model.Update(diagnosticKey(tea.KeyF2))
			model = next.(Model)
			if model.diagnosticWindow.pinned.Detail == "" {
				t.Fatal("F2 missing original detail")
			}
		})
	}
}

func TestWebGUIActions_CommittedWarningsReachF2(t *testing.T) {
	for _, key := range []string{"i", "u", "space", "b", "x", "r"} {
		t.Run(key, func(t *testing.T) {
			mutations := 0
			warning := protocol.Diagnostic{ID: "fixture:panel-warning", State: protocol.DiagnosticAvailable, Severity: "warning", Summary: "panel committed with warning", Detail: "token=fixture-panel-sync"}
			client := controlclient.NewHTTP("http://mihari", "fixture", &http.Client{Transport: preparedHTTPTransport(func(r *http.Request) (*http.Response, error) {
				var value any
				if r.Method == http.MethodGet {
					value = protocol.WebGUIStatus{Schema: "mihari/v1", Panels: []protocol.PanelStatus{{ID: "fixture", Name: "Fixture", InstalledBuild: "old", RollbackBuild: "previous"}}}
				} else {
					mutations++
					value = protocol.MutationResult{Schema: "mihari/v1", Revision: 12, WarningOutcome: protocol.WarningOutcome{Warnings: []protocol.Warning{{Message: warning.Summary, Diagnostic: &warning}}}}
				}
				raw, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})})
			model := newModelWithClientContext(context.Background(), nil, client)
			model.active = ui.PageWebGUI
			page := model.pages[ui.PageWebGUI].(*webguipage.Model)
			page.SetCapabilities([]string{protocol.CapabilityWebGUI})
			page.Update(page.Load()())
			press := tea.KeyPressMsg{Code: rune(key[0]), Text: key}
			if key == "space" {
				press = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
			}
			_, cmd := page.Update(press)
			if cmd == nil {
				t.Fatal("action unavailable")
			}
			intent, ok := cmd().(ui.ActionIntentMsg)
			if !ok {
				t.Fatal("missing action intent")
			}
			result := intent.Execute()
			if outcome, ok := result.(interface{ Err() error }); !ok || outcome.Err() != nil {
				t.Fatal("warning changed successful action")
			}
			next, _ := model.Update(ui.PageResultMsg{Page: ui.PageWebGUI, Result: result})
			model = next.(Model)
			if mutations != 1 || len(model.diagnosticWindow.entries) != 1 || model.diagnosticWindow.entries[0].snapshot.Detail != warning.Detail {
				t.Fatalf("committed warning lost or mutation replayed (%d, %d)", mutations, len(model.diagnosticWindow.entries))
			}
		})
	}
}
