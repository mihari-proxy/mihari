package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"github.com/charmbracelet/x/ansi"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"github.com/mihari-proxy/mihari/internal/update"
	"strings"
	"testing"
)

type rootReplacementUpdater struct {
	rootSelfUpdater
	calls   int
	preview *update.ReplacementPreview
}

func TestPreparedConsent_RootRejectsMissingPreparationKey(t *testing.T) {
	m, _, prepare := rootReplacementFixture(t)
	next, command := m.Update(prepare())
	m = next.(Model)
	intent := command().(ui.ActionIntentMsg)
	// Even a prepared candidate from this page needs its ownership key.
	confirmed := intent.Execute()
	next, _ = m.Update(actionCompletedMsg{Intent: intent, Result: confirmed})
	m = next.(Model)
	p := update.PreparedUpdate{Available: true, Version: "v1.0.0"}
	next, discard := m.Update(ui.RelaunchRequestMsg{Prepared: &p})
	m = next.(Model)
	if m.relaunchRequested || m.preparedUpdate != nil {
		t.Fatal("unkeyed prepared update requested application")
	}
	if discard == nil {
		t.Fatal("unkeyed candidate was not discarded")
	}
	msg, ok := discard().(ui.DiscardPreparedUpdateMsg)
	if !ok || msg.Prepared.Version != p.Version {
		t.Fatal("discard did not retain the rejected candidate")
	}
}

func (f *rootReplacementUpdater) Prepare(context.Context, string, string, string) (update.PreparedUpdate, error) {
	f.calls++
	if f.preview != nil {
		return update.PreparedUpdate{Available: true, Version: f.preview.Candidate.Version, Channel: f.preview.Candidate.Channel, Preview: *f.preview}, nil
	}
	return update.PreparedUpdate{Available: true, Version: "v1.0.0", Channel: "main", Preview: update.ReplacementPreview{ID: strings.Repeat("a", 64), Candidate: update.ReplacementCandidate{Version: "v1.0.0"}, Risk: update.ReplacementDowngrade, Snapshot: update.ReplacementSnapshot{Targets: []update.ReplacementTarget{{Roles: []string{"binary"}, Version: "v2.0.0"}}}}}, nil
}
func rootReplacementFixture(t *testing.T, previews ...update.ReplacementPreview) (Model, *systempage.Model, tea.Cmd) {
	t.Helper()
	t.Setenv("MIHARI_DATA", t.TempDir())
	m := NewModel()
	m.active = ui.PageSystem
	m.focus = ui.Focus{Area: ui.FocusContent, Page: ui.PageSystem}
	f := &rootReplacementUpdater{rootSelfUpdater: rootSelfUpdater{result: update.CheckResult{Latest: "v3.0.0", Available: true}}}
	if len(previews) > 0 {
		f.preview = &previews[0]
	}
	p := m.pages[ui.PageSystem].(*systempage.Model)
	p.SetSelfUpdater(f, "v2.0.0", "/fixture/mihari", func() bool { return true })
	p.SetSize(180, 100)
	var route func(tea.Cmd)
	route = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch v := c().(type) {
		case tea.BatchMsg:
			for _, child := range v {
				route(child)
			}
		case ui.PageResultMsg:
			if strings.Contains(p.View(), ui.MihariProgressChecking) {
				p.Update(v.Result)
			}
		}
	}
	route(p.Load())
	for n := 0; n < 64; n++ {
		if strings.Contains(ansi.Strip(p.View()), ui.FocusMarker+ui.UpdateMihariLabel) {
			_, c := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			return m, p, c
		}
		p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	t.Fatal("missing update row")
	return m, p, nil
}
func leaveAndReturnSystem(t *testing.T, m Model) Model {
	t.Helper()
	for i, p := range m.rail {
		if p == ui.PageOverview {
			m.railIndex = i
		}
	}
	next, _ := m.landRailPage(ui.PageSystem)
	m = next.(Model)
	for i, p := range m.rail {
		if p == ui.PageSystem {
			m.railIndex = i
		}
	}
	next, _ = m.landRailPage(ui.PageOverview)
	return next.(Model)
}
func TestPreparedConsent_RootRejectsLateRelaunchAfterLeavingAndReturning(t *testing.T) {
	m, _, prepare := rootReplacementFixture(t)
	next, c := m.Update(prepare())
	m = next.(Model)
	intent := c().(ui.ActionIntentMsg)
	// Drive the existing action-completion route, then delay its relaunch command.
	next, c = m.Update(actionCompletedMsg{Intent: intent, Result: intent.Execute()})
	m = next.(Model)
	var relaunch tea.Msg
	if batch, ok := c().(tea.BatchMsg); ok {
		for _, child := range batch {
			if child != nil {
				if msg := child(); msg != nil {
					if _, ok := msg.(ui.RelaunchRequestMsg); ok {
						relaunch = msg
					}
				}
			}
		}
	} else {
		relaunch = c()
	}
	if relaunch == nil {
		t.Fatal("missing relaunch")
	}
	m = leaveAndReturnSystem(t, m)
	next, _ = m.Update(relaunch)
	m = next.(Model)
	if m.relaunchRequested || m.preparedUpdate != nil {
		t.Fatal("late confirmed relaunch survived leaving and returning")
	}
}

func TestPreparedConsent_RootCancelsModalAndDiscardsThroughRunOwner(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEscape}, {Code: tea.KeyEnter}} {
		t.Run(key.String(), func(t *testing.T) {
			m, _, prepare := rootReplacementFixture(t)
			next, cmd := m.Update(prepare())
			m = next.(Model)
			intent := cmd().(ui.ActionIntentMsg)
			next, _ = m.Update(intent)
			m = next.(Model)
			if m.modal == nil {
				t.Fatal("no confirmation")
			}
			calls := 0
			m.discardPrepared = func(p update.PreparedUpdate) error {
				calls++
				if p.Consent.Yes {
					t.Error("cancellation granted consent")
				}
				return nil
			}
			next, cmd = m.Update(key)
			m = next.(Model)
			if cmd == nil {
				t.Fatal("cancel not routed")
			}
			next, cmd = m.Update(cmd())
			m = next.(Model)
			next, cmd = m.Update(cmd())
			m = next.(Model)
			m.Update(cmd())
			if calls != 1 || m.relaunchRequested || m.modal != nil {
				t.Fatalf("cancel cleanup calls=%d relaunch=%v", calls, m.relaunchRequested)
			}
		})
	}
}
func TestPreparedConsent_RootRejectsLateIntentAndExecute(t *testing.T) {
	for _, kind := range []string{"result", "intent", "execute"} {
		t.Run(kind, func(t *testing.T) {
			m, _, prepare := rootReplacementFixture(t)
			msg := prepare()
			if kind != "result" {
				next, c := m.Update(msg)
				m = next.(Model)
				msg = c()
				if kind == "execute" {
					msg = actionExecuteMsg{Intent: msg.(ui.ActionIntentMsg)}
				}
			}
			m = leaveAndReturnSystem(t, m)
			next, c := m.Update(msg)
			m = next.(Model)
			if m.modal != nil || m.relaunchRequested || len(m.pendingActions) != 0 || c == nil {
				t.Fatal("late update not discarded")
			}
		})
	}
}

func TestPreparedConsent_RootDoesNotRecordStaleConfirmationAsSuccess(t *testing.T) {
	m, _, prepare := rootReplacementFixture(t)
	next, c := m.Update(prepare())
	m = next.(Model)
	intent := c().(ui.ActionIntentMsg)
	m = leaveAndReturnSystem(t, m)
	next, _ = m.Update(actionCompletedMsg{Intent: intent, Result: intent.Execute()})
	m = next.(Model)
	if len(m.operations) != 0 {
		t.Fatal("discarded confirmation recorded as successful update")
	}
}
func TestPreparedConsent_RootSurfacesDiscardFailure(t *testing.T) {
	m := NewModel()
	m.discardPrepared = func(update.PreparedUpdate) error { return errors.New("raw secret cleanup detail") }
	next, c := m.Update(ui.DiscardPreparedUpdateMsg{})
	m = next.(Model)
	next, _ = m.Update(c())
	m = next.(Model)
	if len(m.operations) != 1 || m.operations[0].State != ui.FailedLabel || strings.Contains(m.operations[0].Detail, "secret") {
		t.Fatalf("unsafe cleanup outcome: %+v", m.operations)
	}
}

func TestPreparedConsent_CompactConfirmationKeepsButtonsVisible(t *testing.T) {
	preview, err := update.NewReplacementPreview(update.ReplacementCandidate{Version: "v0.1.0-dev.124", Channel: "dev", SHA256: strings.Repeat("a", 64)}, update.ReplacementSnapshot{Targets: []update.ReplacementTarget{
		{Roles: []string{"binary"}, Path: "/fixture/binary", Exists: true},
		{Roles: []string{"service"}, Path: "/fixture/service", Exists: true, Version: "v0.1.0-dev.123"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// The page fixture's channel is main; channel choice does not determine risk.
	preview.Candidate.Channel = "main"
	m, _, prepare := rootReplacementFixture(t, preview)
	_, command := m.Update(prepare())
	intent := command().(ui.ActionIntentMsg)
	view := ansi.Strip(NewConfirmation(intent.Title, intent.Object, intent.Impact, intent.Rollback).View(72, 22))
	visible := strings.Join(strings.Split(view, "\n")[:min(22, len(strings.Split(view, "\n")))], "\n")
	for _, required := range []string{ui.ConfirmLabel, ui.CancelLabel, "data loss", "disk state."} {
		if !strings.Contains(visible, required) {
			t.Fatalf("compact confirmation clips %q:\n%s", required, view)
		}
	}
}
