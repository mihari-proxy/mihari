package system

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"github.com/mihari-proxy/mihari/internal/update"
	"strings"
	"testing"
)

type replacementUpdater struct {
	fakeSelfUpdater
	prepared update.PreparedUpdate
	calls    int
}

func (f *replacementUpdater) Prepare(context.Context, string, string, string) (update.PreparedUpdate, error) {
	f.calls++
	return f.prepared, nil
}
func (*replacementUpdater) ApplyPrepared(context.Context, update.PreparedUpdate) (update.Result, error) {
	panic("apply before TUI exit")
}
func replacementFixture(t *testing.T) (*Model, *replacementUpdater) {
	t.Helper()
	preview, err := update.NewReplacementPreview(update.ReplacementCandidate{Version: "v1.0.0", SHA256: strings.Repeat("a", 64), Channel: "main"}, update.ReplacementSnapshot{Targets: []update.ReplacementTarget{{Roles: []string{"binary"}, Path: "/fixture/mihari", FileID: "old", Exists: true, SHA256: strings.Repeat("b", 64), Version: "v2.0.0"}}})
	if err != nil {
		t.Fatal(err)
	}
	f := &replacementUpdater{prepared: update.PreparedUpdate{Available: true, Version: "v1.0.0", Channel: "main", Preview: preview}}
	m := New(nil, nil)
	m.SetSelfUpdater(f, "v2.0.0", "/fixture/mihari", func() bool { return true })
	m.mihariChannel = "main"
	m.mihariChannelLoaded = true
	m.selfCheckLoaded = true
	m.selfCheckResult = update.CheckResult{Latest: "v3.0.0", Available: true}
	m.focusID = rowMihariUpdate
	return m, f
}
func TestPreparedConsent_PreparesBeforeDisplayingActualCandidate(t *testing.T) {
	m, f := replacementFixture(t)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("no preparation")
	}
	msg := cmd()
	if _, ok := msg.(ui.ActionIntentMsg); ok {
		t.Fatal("confirmation uses stale Check before Prepare")
	}
	// Commands are routed through the same PageResult envelope as Run.
	var intent *ui.ActionIntentMsg
	var consume func(tea.Msg)
	consume = func(msg tea.Msg) {
		switch v := msg.(type) {
		case tea.BatchMsg:
			for _, c := range v {
				if c != nil {
					consume(c())
				}
			}
		case ui.PageResultMsg:
			_, next := m.Update(v.Result)
			if next != nil {
				consume(next())
			}
		case ui.ActionIntentMsg:
			intent = &v
		}
	}
	consume(msg)
	if f.calls != 1 || intent == nil || !strings.Contains(intent.Object, "v1.0.0") || !strings.Contains(intent.Impact, "data loss") {
		t.Fatalf("actual preview missing: calls=%d intent=%+v", f.calls, intent)
	}
	if f.prepared.Consent.Yes {
		t.Fatal("prepared before consent")
	}
	_, next := m.Update(intent.Execute())
	if next == nil {
		t.Fatal("confirmed candidate not routed")
	}
	request, ok := next().(ui.RelaunchRequestMsg)
	if !ok || request.Prepared == nil || !request.Prepared.Consent.Yes || request.Prepared.Consent.ExpectedPreview != f.prepared.Preview.ID {
		t.Fatalf("consent not bound: %#v", request)
	}
}

func TestPreparedConsent_CancelAndChannelChangeDiscardCandidate(t *testing.T) {
	for _, kind := range []string{"cancel", "channel", "late-confirm"} {
		t.Run(kind, func(t *testing.T) {
			m, _ := replacementFixture(t)
			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			msg := cmd().(ui.PageResultMsg)
			_, cmd = m.Update(msg.Result)
			intent := cmd().(ui.ActionIntentMsg)
			switch kind {
			case "cancel":
				_, cmd = m.Update(intent.Cancel().(ui.PageResultMsg).Result)
			case "channel":
				m.Update(mihariChannelResultMsg{channel: "dev"})
				_, cmd = m.Update(intent.Execute())
			case "late-confirm":
				m.CancelMihariPreparation()
				_, cmd = m.Update(intent.Execute())
			}
			if cmd == nil {
				t.Fatal("candidate not discarded")
			}
			if _, ok := cmd().(ui.DiscardPreparedUpdateMsg); !ok {
				t.Fatalf("cancelled update escaped: %T", cmd())
			}
			if kind == "cancel" && !strings.Contains(m.View(), "Update cancelled") {
				t.Fatal("cancellation not shown on update row")
			}
			if m.pendingPrepared != nil {
				t.Fatal("cancelled candidate retained by page")
			}
		})
	}
}
func TestPreparedConsent_RepeatedEnterAndLoadDoNotPrepareAgain(t *testing.T) {
	m, f := replacementFixture(t)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, again := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if again != nil {
		t.Fatal("duplicate preparation while downloading")
	}
	msg := cmd().(ui.PageResultMsg)
	m.Update(msg.Result)
	_, again = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if again != nil {
		t.Fatal("duplicate while confirmation queued")
	}
	if c := m.Load(); c != nil {
		t.Fatal("Load restarted display check during confirmation")
	}
	if f.calls != 1 {
		t.Fatal("unexpected preparation count")
	}
}

type cancelReplacementUpdater struct {
	replacementUpdater
	started, canceled, release chan struct{}
}

func (f *cancelReplacementUpdater) Prepare(ctx context.Context, _, _, _ string) (update.PreparedUpdate, error) {
	close(f.started)
	<-ctx.Done()
	close(f.canceled)
	<-f.release
	return f.prepared, ctx.Err()
}
func TestPreparedConsent_EscapeCancelsDownloadAndDiscardsLateResult(t *testing.T) {
	m, f := replacementFixture(t)
	blocking := &cancelReplacementUpdater{replacementUpdater: *f, started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	m.selfUpdater = blocking
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-blocking.started
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	<-blocking.canceled
	close(blocking.release)
	msg := (<-result).(ui.PageResultMsg)
	_, discard := m.Update(msg.Result)
	if _, ok := discard().(ui.DiscardPreparedUpdateMsg); !ok || m.pending {
		t.Fatal("late download revived canceled update")
	}
}

func TestPreparedConsent_OrdinaryUpgradeLabelsActualTargetVersions(t *testing.T) {
	m, f := replacementFixture(t)
	preview, err := update.NewReplacementPreview(update.ReplacementCandidate{Version: "v3.0.0", Channel: "main", SHA256: strings.Repeat("a", 64)}, update.ReplacementSnapshot{Targets: []update.ReplacementTarget{
		{Roles: []string{"binary"}, Path: "/fixture/bin", Exists: true, Version: "v1.0.0"},
		{Roles: []string{"service"}, Path: "/fixture/service", Exists: true, Version: "v2.0.0"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.prepared.Preview = preview
	f.prepared.Version = "v3.0.0"
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	message := cmd().(ui.PageResultMsg)
	_, cmd = m.Update(message.Result)
	intent := cmd().(ui.ActionIntentMsg)
	for _, want := range []string{"binary: v1.0.0", "service: v2.0.0", "v3.0.0"} {
		if !strings.Contains(intent.Object, want) {
			t.Fatalf("actual target identity %q missing from %q", want, intent.Object)
		}
	}
	if strings.Contains(intent.Impact, "data loss") {
		t.Fatal("ordinary upgrade was presented as a downgrade")
	}
}
