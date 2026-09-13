package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	proxypage "github.com/mihari-proxy/mihari/internal/tui/pages/proxies"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestProxyDuplicateWarning_FirstSuccessfulStartupCheckOnly(t *testing.T) {
	for _, initialDuplicates := range []bool{false, true} {
		m := NewModel()
		m.applySessionEvent(session.Event{Kind: session.EventProxies, Err: errors.New("failed")})
		if m.proxyNamesChecked {
			t.Fatal("failure consumed startup check")
		}
		event := session.Event{Kind: session.EventProxies}
		if initialDuplicates {
			event.Proxies.DuplicateNames = []string{"shared"}
		}
		m.modal = NewDetail("existing", "existing dialog")
		m.applySessionEvent(event)
		if m.modal.title != "existing" {
			t.Fatal("overwrote existing modal")
		}
		m.modal = nil
		m.showDuplicateNames()
		if (m.modal != nil) != initialDuplicates {
			t.Fatal("startup warning missing or invented")
		}
		m.modal = nil
		event.Proxies.DuplicateNames = []string{"later"}
		m.applySessionEvent(event)
		if m.modal != nil {
			t.Fatal("later refresh repeated startup check")
		}
	}
}

func TestProxyDuplicateWarning_StripsTerminalControls(t *testing.T) {
	m := NewModel()
	m.applySessionEvent(session.Event{Kind: session.EventProxies, Proxies: protocol.ProxyGroups{DuplicateNames: []string{"\x1b[31mHK\x1b[0m\x07"}}})
	if m.modal == nil || strings.ContainsAny(m.modal.body, "\x1b\x07") {
		t.Fatal("unsafe name in warning")
	}
}

func TestProxySnapshot_FailureRetainsDataAndRecoveryClearsError(t *testing.T) {
	m := NewModel()
	page := m.pages[ui.PageProxies].(*proxypage.Model)
	page.SetSize(100, 35)
	good := session.Event{Kind: session.EventProxies, Proxies: protocol.ProxyGroups{Groups: []protocol.ProxyGroup{{Name: "group", Now: "HK", Nodes: []protocol.ProxyNode{{Name: "HK", Type: "Trojan"}}}}}}
	m.applySessionEvent(good)
	m.applySessionEvent(session.Event{Kind: session.EventProxies, Err: protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "Failed to load provider nodes: mihomo returned HTTP 503", Details: map[string]any{"status": 503}}})
	for _, want := range []string{"group", "Stale data", "503"} {
		if !strings.Contains(page.View(), want) {
			t.Errorf("missing %q in retained view", want)
		}
	}
	m.applySessionEvent(good)
	if strings.Contains(page.View(), "Stale data") || strings.Contains(page.View(), "503") {
		t.Fatal("load error survived recovery")
	}
}

func TestProxySnapshot_FirstFailureIsErrorEmptyState(t *testing.T) {
	m := NewModel()
	page := m.pages[ui.PageProxies].(*proxypage.Model)
	page.SetSize(90, 25)
	m.applySessionEvent(session.Event{Kind: session.EventProxies, Err: protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "Failed to load provider nodes: request timed out"}})
	if !strings.Contains(page.View(), "Unable to load proxy groups") {
		t.Fatal("first load failure presented as empty success")
	}
}
