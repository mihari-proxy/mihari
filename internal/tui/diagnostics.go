package tui

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/session"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type diagnosticClient interface {
	Diagnostic(context.Context, string) (protocol.DiagnosticResult, error)
}

type diagnosticEntry struct {
	snapshot protocol.Diagnostic
	page     ui.PageID
}

type diagnosticWindow struct {
	pageWarnings        map[ui.PageID]string
	resourceFailures    map[session.EventKind]string
	localSequence       uint64
	localNotice         string
	remoteNotice        string
	historyQueryFailure *protocol.Diagnostic
	preferred           map[ui.PageID]string
	entries             []diagnosticEntry
	open                bool
	detailFocus         bool
	selected            string
	pinned              protocol.Diagnostic
	scroll              int
	sequence            uint64
	generation          uint64
	copyStatus          string
	copyText            func(string) error
	client              diagnosticClient
	cancel              context.CancelFunc
	maxRecords          int
	maxBytes            int
}

func newDiagnosticWindow() *diagnosticWindow {
	return &diagnosticWindow{pageWarnings: make(map[ui.PageID]string), resourceFailures: make(map[session.EventKind]string), preferred: make(map[ui.PageID]string), copyText: clipboard.WriteAll, maxRecords: 384, maxBytes: 16 << 20}
}

func (w *diagnosticWindow) add(snapshot protocol.Diagnostic, page ui.PageID) string {
	if snapshot.ID == "" {
		w.sequence++
		snapshot.ID = fmt.Sprintf("tui-ui:%d", w.sequence)
	}
	if snapshot.Time.IsZero() {
		snapshot.Time = time.Now().UTC()
	}
	for i, entry := range w.entries {
		if entry.snapshot.ID != snapshot.ID {
			continue
		}
		if page == "" {
			page = entry.page
		}
		if snapshot.State == protocol.DiagnosticReference && entry.snapshot.State != protocol.DiagnosticReference {
			snapshot = entry.snapshot
		}
		w.entries[i] = diagnosticEntry{snapshot: snapshot, page: page}
		w.trim()
		return snapshot.ID
	}
	w.entries = append(w.entries, diagnosticEntry{snapshot: snapshot, page: page})
	slices.SortStableFunc(w.entries, func(a, b diagnosticEntry) int { return b.snapshot.Time.Compare(a.snapshot.Time) })
	w.trim()
	return snapshot.ID
}

func (w *diagnosticWindow) trim() {
	bytes := 0
	for _, entry := range w.entries {
		bytes += diagnosticEntryBytes(entry)
	}
	for len(w.entries) > 0 && (len(w.entries) > w.maxRecords || bytes > w.maxBytes) {
		last := len(w.entries) - 1
		bytes -= diagnosticEntryBytes(w.entries[last])
		w.entries[last] = diagnosticEntry{}
		w.entries = w.entries[:last]
	}
}

func diagnosticEntryBytes(entry diagnosticEntry) int {
	s := entry.snapshot
	return len(entry.page) + len(s.ID) + len(s.InstanceID) + len(s.Severity) + len(s.Component) + len(s.Event) + len(s.OperationID) + len(s.Operation) + len(s.Object) + len(s.Code) + len(s.Summary) + len(s.Detail) + len(s.State) + len(s.TruncationReason) + len(s.RetrievalError)
}

func (w *diagnosticWindow) close() {
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	w.open = false
	w.pinned = protocol.Diagnostic{}
	w.selected = ""
	w.generation++
}

func (w *diagnosticWindow) selectedIndex() int {
	for i, entry := range w.entries {
		if entry.snapshot.ID == w.selected {
			return i
		}
	}
	return -1
}

func (w *diagnosticWindow) show(ctx context.Context, page ui.PageID, operationID string) tea.Cmd {
	w.open, w.detailFocus = true, false
	w.copyStatus, w.scroll = "", 0
	index := 0
	for i, entry := range w.entries {
		if entry.page == page {
			index = i
			break
		}
	}
	if operationID != "" {
		for i, entry := range w.entries {
			if entry.snapshot.ID == operationID {
				index = i
				break
			}
		}
	}
	return w.selectRecord(ctx, index)
}

type diagnosticDetailMsg struct {
	generation uint64
	id         string
	result     protocol.DiagnosticResult
	err        error
}
type diagnosticCopyMsg struct {
	generation uint64
	id         string
	err        error
}

func (w *diagnosticWindow) selectRecord(ctx context.Context, index int) tea.Cmd {
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	w.generation++
	w.copyStatus, w.scroll = "", 0
	if index < 0 || index >= len(w.entries) {
		return nil
	}
	w.pinned = w.entries[index].snapshot
	w.selected = w.pinned.ID
	if w.pinned.State != protocol.DiagnosticReference {
		return nil
	}
	if w.client == nil {
		w.pinned.State = protocol.DiagnosticUnavailable
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	queryCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	generation, id, client := w.generation, w.selected, w.client
	return func() tea.Msg {
		defer cancel()
		result, err := client.Diagnostic(queryCtx, id)
		return diagnosticDetailMsg{generation: generation, id: id, result: result, err: err}
	}
}

func (model *Model) recordDiagnostic(err error, snapshot *protocol.Diagnostic, page ui.PageID) string {
	if model.diagnosticWindow == nil {
		model.diagnosticWindow = newDiagnosticWindow()
	}
	if api, ok := diagnostics.Classification(err); ok {
		model.recordWarnings(api.WarningOutcome, page)
	}
	var record protocol.Diagnostic
	if snapshot != nil {
		record = *snapshot
	} else {
		if err == nil || diagnostics.NormalCancellation(model.diagnosticContext(), err) {
			return ""
		}
		var captured bool
		record, captured = diagnostics.Snapshot(err)
		if !captured {
			record = diagnostics.Describe(model.diagnosticContext(), diagnostics.Record{Component: "tui", Event: "operation.failed", Level: slog.LevelError, Err: err})
		}
	}
	if record.ID == "" && model.localDiagnosticHistory != nil {
		record = model.localDiagnosticHistory.Add(record)
	}
	return model.diagnosticWindow.add(record, page)
}

func (model *Model) recordWarnings(outcome protocol.WarningOutcome, page ui.PageID) string {
	var last string
	for _, warning := range outcome.Warnings {
		snapshot := warning.Diagnostic
		if snapshot == nil {
			snapshot = &protocol.Diagnostic{Code: warning.Code, Summary: warning.Message, Severity: "warning", State: protocol.DiagnosticUnsupported}
		}
		last = model.recordDiagnostic(nil, snapshot, page)
		model.diagnosticWindow.pageWarnings[page] = "Warning: " + diagnosticSingleLine(warning.Message)
	}
	if outcome.WarningsOmitted > 0 {
		model.diagnosticWindow.pageWarnings[page] = fmt.Sprintf("Warning collection limit reached (%d omitted)", outcome.WarningsOmitted)
	}
	return last
}

type diagnosticTickMsg struct{}

func (model Model) scheduleDiagnosticPoll() tea.Cmd {
	if model.localDiagnosticHistory == nil || model.diagnosticContext().Err() != nil {
		return nil
	}
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return diagnosticTickMsg{} })
}

func (model *Model) syncLocalDiagnostics() {
	if model.localDiagnosticHistory == nil {
		return
	}
	w := model.diagnosticWindow
	// At most two pages cover the 128-record local history. Concurrent publishers
	// are picked up on the next tick, so the UI never chases a moving tail.
	for range 2 {
		page := model.localDiagnosticHistory.List("", w.localSequence, 100)
		if page.LostBefore {
			w.localNotice = "Older local diagnostic records were evicted"
		}
		for _, reference := range page.Records {
			result := model.localDiagnosticHistory.Get(reference.ID)
			if result.Diagnostic != nil {
				w.add(*result.Diagnostic, diagnosticRecordPage(*result.Diagnostic))
			}
		}
		w.localSequence = page.NextSequence
		if !page.HasMore {
			w.localSequence = page.LatestSequence
			break
		}
	}
}

func diagnosticRecordPage(record protocol.Diagnostic) ui.PageID {
	name := record.Operation
	if name == "" {
		name = record.Component + "." + record.Event
	}
	for _, match := range []struct {
		text string
		page ui.PageID
	}{
		{"onboarding", ui.PageSetup}, {"setup", ui.PageSetup},
		{"subscription", ui.PageSubscriptions}, {"connection", ui.PageConnections},
		{"sysproxy", ui.PageSystem}, {"system_proxy", ui.PageSystem},
		{"proxy", ui.PageProxies}, {"routing", ui.PageProxies},
		{"rule", ui.PageRules}, {"panel", ui.PageWebGUI}, {"webgui", ui.PageWebGUI},
		{"core", ui.PageSystem}, {"geoip", ui.PageSystem}, {"service", ui.PageSystem},
		{"install", ui.PageSystem}, {"self", ui.PageSystem}, {"logging", ui.PageSystem},
		{"tun", ui.PageSystem}, {"log", ui.PageLogs},
	} {
		if strings.Contains(name, match.text) {
			return match.page
		}
	}
	return ui.PageOverview
}

func (model Model) diagnosticContext() context.Context {
	if model.pageCtx != nil {
		return model.pageCtx
	}
	return context.Background()
}

func (model *Model) updateDiagnostics(message tea.Msg) (tea.Cmd, bool) {
	if model.diagnosticWindow == nil {
		model.diagnosticWindow = newDiagnosticWindow()
	}
	w := model.diagnosticWindow
	switch typed := message.(type) {
	case diagnosticTickMsg:
		model.syncLocalDiagnostics()
		return model.scheduleDiagnosticPoll(), true
	case ui.DiagnosticMsg:
		model.recordDiagnostic(typed.Err, typed.Diagnostic, typed.Page)
		return nil, true
	case diagnosticDetailMsg:
		if !w.open || typed.generation != w.generation || typed.id != w.selected {
			return nil, true
		}
		w.cancel = nil
		if typed.err != nil {
			w.pinned.State = protocol.DiagnosticUnavailable
			w.pinned.RetrievalError = diagnostics.Capture(typed.err).Text
		} else if typed.result.State == protocol.DiagnosticAvailable && typed.result.Diagnostic != nil && typed.result.Diagnostic.ID == typed.id {
			w.pinned = *typed.result.Diagnostic
		} else {
			w.pinned.State = typed.result.State
			if w.pinned.State == "" || w.pinned.State == protocol.DiagnosticAvailable {
				w.pinned.State = protocol.DiagnosticUnavailable
				w.pinned.RetrievalError = "Invalid diagnostic detail response"
			}
		}
		return nil, true
	case diagnosticCopyMsg:
		if w.open && typed.generation == w.generation && typed.id == w.selected {
			w.copyStatus = "Copied"
			if typed.err != nil {
				w.copyStatus = "Copy failed"
			}
		}
		if typed.err != nil {
			model.recordDiagnostic(typed.err, nil, model.active)
		}
		return nil, true
	case tea.KeyPressMsg:
		if typed.String() == "f2" {
			if w.open {
				w.close()
				return nil, true
			}
			model.syncLocalDiagnostics()
			return w.show(model.diagnosticContext(), model.active, w.preferred[model.active]), true
		}
		if !w.open {
			return nil, false
		}
		switch typed.String() {
		case "esc":
			w.close()
		case "tab", "shift+tab":
			w.detailFocus = !w.detailFocus
		case "enter":
			w.detailFocus = true
		case "c":
			body := w.pinned.Detail
			if w.selected == "" && w.historyQueryFailure != nil {
				body = w.historyQueryFailure.Detail
			} else if w.pinned.State != protocol.DiagnosticAvailable {
				w.copyStatus = "Details unavailable"
				return nil, true
			}
			copyText, generation, id := w.copyText, w.generation, w.selected
			return func() tea.Msg { return diagnosticCopyMsg{generation: generation, id: id, err: copyText(body)} }, true
		case "up", "down", "pgup", "pgdown", "home", "end":
			step := 1
			if typed.String() == "pgup" || typed.String() == "pgdown" {
				step = max(1, model.height-9)
				if !w.detailFocus {
					step = max(1, step/2)
				}
			}
			if w.detailFocus {
				lines := w.detailLines(model.width)
				last := max(0, len(lines)-max(1, model.height-9))
				switch typed.String() {
				case "up", "pgup":
					w.scroll = max(0, w.scroll-step)
				case "down", "pgdown":
					w.scroll = min(last, w.scroll+step)
				case "home":
					w.scroll = 0
				case "end":
					w.scroll = last
				}
			} else {
				index := max(0, w.selectedIndex())
				switch typed.String() {
				case "up", "pgup":
					index = max(0, index-step)
				case "down", "pgdown":
					index = min(len(w.entries)-1, index+step)
				case "home":
					index = 0
				case "end":
					index = len(w.entries) - 1
				}
				return w.selectRecord(model.diagnosticContext(), index), true
			}
		}
		return nil, true
	}
	return nil, false
}

func (w *diagnosticWindow) widths(width int) (int, int) {
	inner := max(1, min(110, width-4)-6)
	if width < 72 {
		return inner, inner
	}
	list := min(34, max(1, inner/3))
	return list, max(1, inner-list-3)
}

func (w *diagnosticWindow) detailLines(width int) []string {
	_, detailWidth := w.widths(width)
	text := diagnostics.TerminalText("Diagnostic", w.pinned)
	if w.selected == "" {
		text = "No diagnostic records"
		if w.remoteNotice != "" {
			text += "\n" + w.remoteNotice
		}
		if w.localNotice != "" {
			text += "\n" + w.localNotice
		}
	}
	if w.historyQueryFailure != nil {
		text += "\n\n" + diagnostics.TerminalText("History query", *w.historyQueryFailure)
	}
	return strings.Split(ansi.Hardwrap(text, detailWidth, true), "\n")
}

func (w *diagnosticWindow) view(width, height int) string {
	theme := ui.DefaultTheme()
	listWidth, detailWidth := w.widths(width)
	rows := max(1, height-9)
	selected := w.selectedIndex()
	start := max(0, selected-max(1, rows/2)+1)
	var list []string
	for i := start; i < len(w.entries) && len(list) < rows; i++ {
		s := w.entries[i].snapshot
		prefix := "  "
		if s.ID == w.selected {
			prefix = "> "
		}
		list = append(list, ui.TruncateVisible(prefix+s.Time.Local().Format("15:04:05")+" "+diagnosticSingleLine(s.Severity+" "+s.Component), listWidth))
		if len(list) < rows {
			list = append(list, ui.TruncateVisible("  "+diagnosticSingleLine(s.Summary), listWidth))
		}
	}
	if len(list) == 0 {
		list = []string{"No diagnostic records"}
	}
	lines := w.detailLines(width)
	detailStart := min(w.scroll, max(0, len(lines)-rows))
	detail := strings.Join(lines[detailStart:min(len(lines), detailStart+rows)], "\n")
	body := detail
	if width >= 72 {
		body = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(listWidth).Render(strings.Join(list, "\n")), " │ ", lipgloss.NewStyle().Width(detailWidth).Render(detail))
	} else if !w.detailFocus {
		body = strings.Join(list, "\n")
	}
	mode := "list"
	if w.detailFocus {
		mode = "details"
	}
	footer := "Tab pane · ↑/↓ · PgUp/PgDn · Home/End · c copy · Esc back"
	if w.copyStatus != "" {
		footer = w.copyStatus + " · " + footer
	}
	if w.remoteNotice != "" {
		footer = w.remoteNotice + " · " + footer
	}
	if w.localNotice != "" {
		footer = w.localNotice + " · " + footer
	}
	boxWidth := max(1, min(110, width-4))
	content := theme.Title.Render("Diagnostics · "+mode) + "\n\n" + body + "\n\n" + theme.Muted.Render(ui.TruncateVisible(footer, max(1, boxWidth-6)))
	box := theme.Dialog.Width(boxWidth).MaxWidth(boxWidth).MaxHeight(max(1, height-2)).Render(content)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// observeDiagnosticMessage captures explicit result contracts before the owning
// page consumes them. It does not infer errors from rendered status strings.
func (model *Model) observeDiagnosticMessage(message tea.Msg) {
	capture := func(result tea.Msg, page ui.PageID) string {
		if origin, ok := result.(interface{ DiagnosticPage() ui.PageID }); ok {
			page = origin.DiagnosticPage()
		}
		var last string
		if outcome, ok := result.(interface {
			Warnings() protocol.WarningOutcome
		}); ok {
			last = model.recordWarnings(outcome.Warnings(), page)
		}
		if outcome, ok := result.(interface{ DiagnosticErrors() []error }); ok {
			for _, err := range outcome.DiagnosticErrors() {
				if id := model.recordDiagnostic(err, nil, page); id != "" {
					last = id
				}
			}
			return last
		}
		if outcome, ok := result.(resultErr); ok {
			if id := model.recordDiagnostic(outcome.Err(), nil, page); id != "" {
				last = id
			}
		}
		return last
	}
	switch typed := message.(type) {
	case actionCompletedMsg:
		delete(model.diagnosticWindow.preferred, typed.Intent.Page)
		delete(model.diagnosticWindow.pageWarnings, typed.Intent.Page)
		if id := capture(typed.Result, typed.Intent.Page); id != "" {
			model.diagnosticWindow.preferred[typed.Intent.Page] = id
		}
	case ui.PageResultMsg:
		capture(typed.Result, typed.Page)
	case sessionEventMsg:
		if typed.Open {
			var warnings protocol.WarningOutcome
			switch typed.Event.Kind {
			case session.EventSubscriptions:
				warnings = typed.Event.Subscriptions.WarningOutcome
			case session.EventRouting:
				warnings = typed.Event.Routing.WarningOutcome
			case session.EventPreferences:
				warnings = typed.Event.Preferences.WarningOutcome
			case session.EventWebGUI:
				warnings = typed.Event.WebGUI.WarningOutcome
			case session.EventLogging:
				warnings = typed.Event.Logging.WarningOutcome
			}
			model.recordWarnings(warnings, diagnosticEventPage(typed.Event.Kind))
			switch typed.Event.Kind {
			case session.EventCore, session.EventSubscriptions, session.EventProxies, session.EventRouting, session.EventRules, session.EventRuleProviders, session.EventPreferences, session.EventWebGUI, session.EventLogging:
				if typed.Event.Err == nil {
					delete(model.diagnosticWindow.resourceFailures, typed.Event.Kind)
				} else {
					capture := diagnostics.Describe(model.diagnosticContext(), diagnostics.Record{Err: typed.Event.Err})
					model.diagnosticWindow.resourceFailures[typed.Event.Kind] = diagnosticSingleLine(capture.Summary)
				}
			}
			if typed.Event.Kind == session.EventDiagnostics && typed.Event.DiagnosticQuery {
				page := typed.Event.Diagnostics
				w := model.diagnosticWindow
				w.historyQueryFailure = nil
				switch {
				case typed.Event.Err != nil:
					w.remoteNotice = "Daemon diagnostic history is unavailable"
					failure := diagnostics.Describe(model.diagnosticContext(), diagnostics.Record{Summary: w.remoteNotice, Err: typed.Event.Err})
					w.historyQueryFailure = &failure
				case page.State == protocol.DiagnosticUnsupported:
					w.remoteNotice = diagnostics.AvailabilityText(page.State)
				case page.State == protocol.DiagnosticRestarted:
					w.remoteNotice = "Daemon restarted; previous history is unavailable"
				case page.LostBefore:
					w.remoteNotice = "Older daemon diagnostic records were evicted"
				default:
					if w.remoteNotice == "Daemon diagnostic history is unavailable" || w.remoteNotice == diagnostics.AvailabilityText(protocol.DiagnosticUnsupported) {
						w.remoteNotice = ""
					}
				}
				for _, record := range page.Records {
					w.add(record, diagnosticRecordPage(record))
				}
				// Query failures occupy one transient display slot, never the
				// history being queried or the local occurrence history.
				return
			}
			model.recordDiagnostic(typed.Event.Err, nil, diagnosticEventPage(typed.Event.Kind))
		}
	case rootServiceStatusMsg:
		model.recordDiagnostic(typed.err, nil, ui.PageSystem)
	case networkStatusMsg:
		model.recordDiagnostic(typed.proxyErr, nil, ui.PageSystem)
		model.recordDiagnostic(typed.tunErr, nil, ui.PageSystem)
	default:
		capture(message, model.active)
	}
}

func diagnosticSingleLine(text string) string {
	return strings.NewReplacer("\n", " ↵ ", "\t", " ⇥ ").Replace(diagnostics.EscapeTerminal(text))
}

func (model Model) diagnosticResourceSummary() string {
	if model.diagnosticWindow == nil {
		return ""
	}
	// Deterministic ordering avoids a changing footer when several independent
	// resources fail in one poll. All occurrences remain in the F2 list.
	for _, kind := range []session.EventKind{session.EventCore, session.EventSubscriptions, session.EventProxies, session.EventRouting, session.EventRules, session.EventRuleProviders, session.EventPreferences, session.EventWebGUI, session.EventLogging} {
		if summary := model.diagnosticWindow.resourceFailures[kind]; summary != "" && diagnosticEventPage(kind) == model.active {
			return summary
		}
	}
	return model.diagnosticWindow.pageWarnings[model.active]
}

func diagnosticEventPage(kind session.EventKind) ui.PageID {
	switch kind {
	case session.EventCore, session.EventLogging:
		return ui.PageSystem
	case session.EventSubscriptions:
		return ui.PageSubscriptions
	case session.EventProxies, session.EventRouting:
		return ui.PageProxies
	case session.EventRules, session.EventRuleProviders:
		return ui.PageRules
	case session.EventPreferences, session.EventConnections:
		return ui.PageConnections
	case session.EventWebGUI:
		return ui.PageWebGUI
	case session.EventLog:
		return ui.PageLogs
	default:
		return ui.PageOverview
	}
}
