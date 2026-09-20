package logs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type preferenceClientFake struct {
	requests []protocol.UpdateTUIPreferencesRequest
	err      error
}

type preferenceClientFunc func(context.Context, protocol.UpdateTUIPreferencesRequest) (protocol.TUIPreferences, error)

func (f preferenceClientFunc) UpdateTUIPreferences(ctx context.Context, request protocol.UpdateTUIPreferencesRequest) (protocol.TUIPreferences, error) {
	return f(ctx, request)
}

func TestPreferences_StopCancelsWithoutFlushingPendingEdit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	m := New(10)
	m.SetPreferenceClient(preferenceClientFunc(func(ctx context.Context, _ protocol.UpdateTUIPreferencesRequest) (protocol.TUIPreferences, error) {
		close(started)
		<-ctx.Done()
		return protocol.TUIPreferences{}, ctx.Err()
	}), func() (context.Context, context.CancelFunc) { return context.WithCancel(ctx) })
	chooseLevels(m, 3, 4)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	filterKey(m, tea.KeyEnter)
	filterKey(m, tea.KeySpace)
	filterKey(m, tea.KeyEnter)
	batch := cmd().(tea.BatchMsg)
	finished := make(chan tea.Msg, 1)
	done := make(chan struct{})
	go func() { defer close(done); finished <- batch[0]() }()
	t.Cleanup(func() { cancel(); <-done })
	<-started
	m.Stop()
	select {
	case msg := <-finished:
		if _, next := m.Update(msg.(ui.PageResultMsg).Result); next != nil {
			t.Fatal("exit flushed pending edit")
		}
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel save")
	}
}

func TestPreferences_ContinuousEditsSaveOnlyLatestAndIgnoreOldResults(t *testing.T) {
	client := &preferenceClientFake{}
	m := New(10)
	m.SetPreferenceClient(client, nil)
	chooseLevels(m, 3, 4)
	_, first := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	// While WARNING+ is in flight, choose ERROR and then add INFO.
	filterKey(m, tea.KeyEnter)
	filterKey(m, tea.KeySpace)
	_, pending := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if pending != nil {
		t.Fatal("started overlapping save")
	}
	filterKey(m, tea.KeyEnter)
	filterKey(m, tea.KeyUp)
	filterKey(m, tea.KeyUp)
	filterKey(m, tea.KeySpace)
	_, pending = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if pending != nil {
		t.Fatal("started overlapping save")
	}
	// A result may arrive while the selection dialog is open.
	filterKey(m, tea.KeyEnter)
	old := runPreferenceSave(t, first)
	_, latest := m.Update(old)
	if latest == nil || !m.HasLevelDialog() || m.levels != 0b1010 {
		t.Fatal("old result replaced current selection or blocked in dialog")
	}
	if _, cmd := m.Update(old); cmd != nil {
		t.Fatal("duplicate old result rescheduled save")
	}
	if _, cmd := m.Update(preferenceTickMsg{version: old.version, at: time.Unix(1, 0)}); cmd != nil {
		t.Fatal("stale tick rescheduled animation")
	}
	newest := runPreferenceSave(t, latest)
	if len(client.requests) != 2 || !slices.Equal(client.requests[1].LogLevels, []string{"info", "error"}) || client.requests[0].OperationID == client.requests[1].OperationID {
		t.Fatalf("requests=%+v", client.requests)
	}
	m.Update(newest)
	filterKey(m, tea.KeyEsc)
	if strings.Contains(logsStripANSI(m.View()), "Saving") || strings.Contains(logsStripANSI(m.View()), "Unsaved") || m.levels != 0b1010 {
		t.Fatal("latest save did not settle current selection")
	}
	if _, cmd := m.Update(preferenceTickMsg{version: newest.version}); cmd != nil {
		t.Fatal("completed save left animation running")
	}
}

func TestPreferences_InitialLoadCannotOverwriteConfirmedSelection(t *testing.T) {
	m := New(10)
	chooseLevels(m, 1, 3)
	filterKey(m, tea.KeyEnter)
	m.SetPreferences(protocol.TUIPreferences{LogLevels: []string{"error"}})
	if m.levels != 0b0101 {
		t.Fatal("late load overwrote local selection")
	}
}

func TestPreferences_BadgeAnimatesAndStops(t *testing.T) {
	m := New(10)
	m.SetPreferenceClient(&preferenceClientFake{}, nil)
	chooseLevels(m, 3, 4)
	_, save := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	version := m.preference.savingVersion
	var previous string
	for i := range 3 {
		_, next := m.Update(preferenceTickMsg{version: version, at: time.Unix(0, 0).Add(time.Duration(i) * 100 * time.Millisecond)})
		view := m.View()
		if next == nil || view == previous || !strings.Contains(logsStripANSI(view), "Saving…") {
			t.Fatal("saving badge did not animate")
		}
		previous = view
	}
	m.Update(runPreferenceSave(t, save))
	if _, cmd := m.Update(preferenceTickMsg{version: version}); cmd != nil {
		t.Fatal("idle animation continued")
	}
}

func TestPreferences_SavingBadgeFitsFocusedControls(t *testing.T) {
	for _, width := range []int{72, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := New(10)
			m.SetSize(width, 20)
			m.SetContentFocused(true)
			m.SetPreferenceClient(&preferenceClientFake{}, nil)
			t.Cleanup(m.Stop)
			chooseLevels(m, 1, 2, 4)
			filterKey(m, tea.KeyEnter)
			badge := ui.RenderStatusChip(m.theme, ui.StatusChipPending, ui.SpinnerLabel(m.preference.clock, "Saving…"))
			view := m.View()
			if !strings.Contains(view, badge) || lipgloss.Width(view) > width {
				t.Fatalf("focused badge lost its style or overflowed width %d:\n%s", width, view)
			}
		})
	}
}

func (f *preferenceClientFake) UpdateTUIPreferences(_ context.Context, request protocol.UpdateTUIPreferencesRequest) (protocol.TUIPreferences, error) {
	f.requests = append(f.requests, request)
	return protocol.TUIPreferences{LogLevels: request.LogLevels}, f.err
}

// runPreferenceSave executes only the save in the returned batch, leaving the
// clock under test control so there is no sleep or background goroutine.
func runPreferenceSave(t *testing.T, cmd tea.Cmd) preferenceSavedMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("confirmed change did not schedule saving")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatal("expected independent save and animation commands")
	}
	return batch[0]().(ui.PageResultMsg).Result.(preferenceSavedMsg)
}

func TestPreferences_SaveDoesNotBlockAndFailureDoesNotRetry(t *testing.T) {
	client := &preferenceClientFake{err: errors.New("synthetic write failure")}
	m := New(10)
	m.SetPreferenceClient(client, nil)
	chooseLevels(m, 3, 4)
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(client.requests) != 0 || !strings.Contains(logsStripANSI(m.View()), "WARNING+") || !strings.Contains(logsStripANSI(m.View()), "Saving…") {
		t.Fatal("confirmation must update the filter and saving state before executing IO")
	}
	m.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	if !m.wrap {
		t.Fatal("saving blocked page controls")
	}
	result := runPreferenceSave(t, cmd)
	_, next := m.Update(result)
	if next != nil || !strings.Contains(logsStripANSI(m.View()), "Unsaved") || !strings.Contains(logsStripANSI(m.View()), "WARNING+") {
		t.Fatal("failure retried or rolled back the filter")
	}
	if result.Err() != client.err || result.DiagnosticPage() != ui.PageLogs {
		t.Fatal("failure lost its diagnostic contract")
	}
	m.SetStale(true)
	m.SetStale(false)
	m.SetPreferences(protocol.TUIPreferences{LogLevels: []string{"error"}})
	filterKey(m, tea.KeyEnter)
	_, retry := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if retry != nil || len(client.requests) != 1 || m.levels != 0b1100 {
		t.Fatal("reconnect or unchanged confirmation replayed a failed save")
	}
	if client.requests[0].IfRevision != nil || client.requests[0].ConnectionsColumns != nil || !slices.Equal(client.requests[0].LogLevels, []string{"warn", "error"}) {
		t.Fatalf("save was not a log-only update: %+v", client.requests[0])
	}
}
