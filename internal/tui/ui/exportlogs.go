package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/mihari-proxy/mihari/internal/logging"
)

// OpenExportLogsMsg asks the root model to open the shared log export dialog.
type OpenExportLogsMsg struct{}

// ErrLocalLogStorageUnavailable classifies the unavailable local capability.
var ErrLocalLogStorageUnavailable = errors.New("local log storage unavailable")

// ExportLogsOptions supplies the local exporter and side-effect boundaries.
type ExportLogsOptions struct {
	Context        context.Context
	Now            func() time.Time
	DefaultDir     string
	Exists         func(dir, name string) (bool, error)
	Export         func(context.Context, logging.ExportRequest) (logging.ExportResult, error)
	WriteClipboard func(string) error
}

type exportResultMsg struct {
	Generation uint64
	Result     logging.ExportResult
	Err        error
	Warning    bool
}

type exportRunner struct {
	mu      sync.Mutex
	parent  context.Context
	export  func(context.Context, logging.ExportRequest) (logging.ExportResult, error)
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
}

func newExportRunner(parent context.Context, export func(context.Context, logging.ExportRequest) (logging.ExportResult, error)) *exportRunner {
	if parent == nil {
		parent = context.Background()
	}
	if export == nil {
		export = func(context.Context, logging.ExportRequest) (logging.ExportResult, error) {
			return logging.ExportResult{}, logging.ErrInvalidExportRequest
		}
	}
	return &exportRunner{parent: parent, export: export}
}

func (r *exportRunner) Start(generation uint64, request logging.ExportRequest) (<-chan exportResultMsg, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return nil, false
	}
	ctx, cancel := context.WithCancel(r.parent)
	result := make(chan exportResultMsg, 1)
	done := make(chan struct{})
	r.running, r.cancel, r.done = true, cancel, done
	go func() {
		message := exportResultMsg{Generation: generation}
		var warned atomic.Bool
		// Keep raw errors on the worker boundary. Only a fixed notice crosses to UI.
		request.OnWarning = func(error) { warned.Store(true) }
		defer func() {
			if recover() != nil {
				message.Result = logging.ExportResult{}
				message.Err = errExportPanicked
			}
			cancel()
			message.Warning = warned.Load()
			result <- message
			close(result)
			r.mu.Lock()
			r.running, r.cancel, r.done = false, nil, nil
			close(done)
			r.mu.Unlock()
		}()
		message.Result, message.Err = r.export(ctx, request)
	}()
	return result, true
}

func (r *exportRunner) Cancel() {
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (r *exportRunner) CancelAndWait() {
	r.mu.Lock()
	cancel, done, running := r.cancel, r.done, r.running
	r.mu.Unlock()
	if !running || done == nil {
		return
	}
	if cancel != nil {
		cancel()
	}
	<-done
}

type exportFocus uint8

const (
	exportFocusRange exportFocus = iota
	exportFocusFrom
	exportFocusTo
	exportFocusOutput
)

// ExportLogsModel is the reusable shared log export dialog.
type ExportLogsModel struct {
	options                         ExportLogsOptions
	runner                          *exportRunner
	closed, pending                 bool
	generation                      uint64
	openedAt                        time.Time
	rangeKind                       logging.RangeKind
	from, to, output, defaultOutput string
	focus                           exportFocus
	cursors                         map[exportFocus]int
	resultPath, message             string
	warning                         bool
}

// NewExportLogsModel creates an initially closed dialog without an idle goroutine.
func NewExportLogsModel(options ExportLogsOptions) *ExportLogsModel {
	if options.Context == nil {
		options.Context = context.Background()
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().In(time.Local) }
	}
	if options.WriteClipboard == nil {
		options.WriteClipboard = clipboard.WriteAll
	}
	return &ExportLogsModel{options: options, runner: newExportRunner(options.Context, options.Export), closed: true}
}

// Open resets the editable form using one local-time sample.
func (m *ExportLogsModel) Open() {
	if m.pending {
		return
	}
	m.openedAt = m.options.Now()
	m.rangeKind = logging.RangeLast24Hours
	m.from = m.openedAt.Add(-24 * time.Hour).Format(exportTimeLayout)
	m.to = m.openedAt.Format(exportTimeLayout)
	m.defaultOutput = m.defaultPath(m.openedAt)
	m.output = m.defaultOutput
	m.focus = exportFocusRange
	m.cursors = map[exportFocus]int{exportFocusFrom: TextCursorEnd(m.from), exportFocusTo: TextCursorEnd(m.to), exportFocusOutput: TextCursorEnd(m.output)}
	m.resultPath, m.message, m.closed = "", "", false
	m.warning = false
}

// Update handles dialog-owned input and returns whether the root must stop routing it.
func (m *ExportLogsModel) Update(message tea.Msg) (tea.Cmd, bool) {
	if _, ok := message.(OpenExportLogsMsg); ok {
		m.Open()
		return nil, true
	}
	if m.closed {
		return nil, false
	}
	if result, ok := message.(exportResultMsg); ok {
		if !m.pending || result.Generation != m.generation {
			return nil, true
		}
		m.pending = false
		m.warning = result.Warning
		if errors.Is(result.Err, context.Canceled) {
			m.message = ExportCancelled
			return nil, true
		}
		if result.Err != nil {
			m.message = exportErrorMessage(result.Err)
			return nil, true
		}
		m.resultPath, m.message = result.Result.Path, ""
		return nil, true
	}
	key, isKey := message.(tea.KeyPressMsg)
	if isKey && key.String() == "ctrl+c" {
		return nil, false
	}
	if m.resultPath != "" {
		if !isKey {
			return nil, false
		}
		switch key.String() {
		case "enter":
			if m.options.WriteClipboard(m.resultPath) != nil {
				m.message = ExportCopyFailed
			} else {
				m.message = ""
			}
			return nil, true
		case "esc":
			m.closed = true
			return nil, true
		case "q":
			return nil, false
		default:
			return nil, true
		}
	}
	if m.pending {
		if isKey && key.String() == "esc" {
			m.runner.Cancel()
		}
		if isKey || IsTextEditMsg(message) {
			return nil, true
		}
		return nil, false
	}
	if isKey {
		switch key.String() {
		case "esc":
			m.closed = true
			return nil, true
		case "q":
			if !m.textFocused() {
				return nil, false
			}
		case "tab":
			m.moveFocus(1)
			return nil, true
		case "shift+tab":
			m.moveFocus(-1)
			return nil, true
		case "enter":
			if m.focus == exportFocusRange {
				m.cycleRange()
				return nil, true
			}
			return m.submit(), true
		}
	}
	if m.textFocused() && IsTextEditMsg(message) {
		return m.editFocused(message), true
	}
	if isKey {
		return nil, true
	}
	return nil, false
}

func (m *ExportLogsModel) submit() tea.Cmd {
	now := m.options.Now()
	exportRange := logging.ExportRange{Kind: m.rangeKind}
	switch m.rangeKind {
	case logging.RangeLast24Hours:
		exportRange.From, exportRange.To = now.Add(-24*time.Hour).UTC(), now.UTC()
	case logging.RangeLast60Minutes:
		exportRange.From, exportRange.To = now.Add(-60*time.Minute).UTC(), now.UTC()
	case logging.RangeBetween:
		from, errFrom := parseExportTime(m.from, m.openedAt.Location())
		to, errTo := parseExportTime(m.to, m.openedAt.Location())
		if errFrom != nil || errTo != nil {
			m.message = ExportTimeInvalid
			return nil
		}
		if from.After(to) {
			m.message = ExportRangeInvalid
			return nil
		}
		exportRange.From, exportRange.To = from.UTC(), to.UTC()
	}
	isDefault := m.output == m.defaultOutput
	if isDefault {
		m.defaultOutput = m.defaultPath(now)
		m.output = m.defaultOutput
		m.cursors[exportFocusOutput] = TextCursorEnd(m.output)
	}
	request := logging.ExportRequest{Now: now, Range: exportRange, AutoNumber: isDefault}
	if !isDefault {
		request.OutputPath = strings.TrimSpace(m.output)
	}
	m.generation++
	results, ok := m.runner.Start(m.generation, request)
	if !ok {
		m.message = ExportBusy
		return nil
	}
	m.pending, m.message = true, ""
	m.warning = false
	return func() tea.Msg { return <-results }
}

func (m *ExportLogsModel) defaultPath(now time.Time) string {
	base := "mihari-logs-" + now.Format("20060102-150405-0700")
	name := base + ".zip"
	if m.options.Exists != nil {
		for suffix := 0; ; suffix++ {
			exists, err := m.options.Exists(m.options.DefaultDir, name)
			if err != nil || !exists {
				break
			}
			name = fmt.Sprintf("%s-%d.zip", base, suffix+1)
		}
	}
	return filepath.Join(m.options.DefaultDir, name)
}

func (m *ExportLogsModel) cycleRange() {
	switch m.rangeKind {
	case logging.RangeLast24Hours:
		m.rangeKind = logging.RangeLast60Minutes
	case logging.RangeLast60Minutes:
		m.rangeKind = logging.RangeBetween
	case logging.RangeBetween:
		m.rangeKind = logging.RangeAll
	default:
		m.rangeKind = logging.RangeLast24Hours
	}
}
func (m *ExportLogsModel) editableFocuses() []exportFocus {
	if m.rangeKind == logging.RangeBetween {
		return []exportFocus{exportFocusRange, exportFocusFrom, exportFocusTo, exportFocusOutput}
	}
	return []exportFocus{exportFocusRange, exportFocusOutput}
}
func (m *ExportLogsModel) moveFocus(delta int) {
	fields := m.editableFocuses()
	index := 0
	for i, field := range fields {
		if field == m.focus {
			index = i
			break
		}
	}
	m.focus = fields[(index+delta+len(fields))%len(fields)]
}
func (m *ExportLogsModel) textFocused() bool { return m.focus != exportFocusRange }
func (m *ExportLogsModel) editFocused(message tea.Msg) tea.Cmd {
	value := ""
	switch m.focus {
	case exportFocusFrom:
		value = m.from
	case exportFocusTo:
		value = m.to
	case exportFocusOutput:
		value = m.output
	}
	value, cursor, _, cmd := EditTextField(value, m.cursors[m.focus], message, 4096)
	m.cursors[m.focus] = cursor
	switch m.focus {
	case exportFocusFrom:
		m.from = value
	case exportFocusTo:
		m.to = value
	case exportFocusOutput:
		m.output = value
	}
	return cmd
}

// View renders the dialog centered in the supplied terminal dimensions.
func (m *ExportLogsModel) View(width, height int) string {
	if m.closed {
		return ""
	}
	theme := DefaultTheme()
	if m.resultPath != "" {
		body := theme.Title.Render(ExportComplete) + "\n" + m.resultPath
		body += "\n\nReview before sharing: node names, domains/IPs, and traffic metadata may remain."
		if m.warning {
			body += "\n\n" + exportWarningNotice
		}
		if m.message != "" {
			body += "\n" + theme.Danger.Render(m.message)
		}
		body += "\n\n" + ExportSuccessHelp
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, theme.Dialog.Width(min(84, max(36, width-4))).Render(body))
	}
	line := func(field exportFocus, label, value string) string {
		marker := "  "
		if m.focus == field && !m.pending {
			marker = FocusMarker
		}
		return marker + label + "  " + value
	}
	lines := []string{theme.Title.Render(ExportLogsTitle), "", "  " + ExportNowLabel + "  " + m.openedAt.Format("2006-01-02 15:04:05 -07:00"), line(exportFocusRange, ExportRangeLabel, exportRangeLabel(m.rangeKind))}
	if m.rangeKind == logging.RangeBetween {
		lines = append(lines, line(exportFocusFrom, ExportFromLabel, m.from), line(exportFocusTo, ExportToLabel, m.to))
	}
	lines = append(lines, line(exportFocusOutput, ExportOutputLabel, m.output))
	if m.pending {
		lines = append(lines, "", ExportPending)
	}
	if m.message != "" {
		lines = append(lines, "", theme.Danger.Render(m.message))
	}
	if m.warning {
		lines = append(lines, "", exportWarningNotice)
	}
	lines = append(lines, "", RenderFooter("", ModeExportLogs, FooterOpt{}))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, theme.Dialog.Width(min(84, max(36, width-4))).Render(strings.Join(lines, "\n")))
}

// Closed reports whether the dialog is hidden.
func (m *ExportLogsModel) Closed() bool { return m.closed }

// Pending reports whether the current generation has not delivered its result.
func (m *ExportLogsModel) Pending() bool { return m.pending }

// CancelAndWait cancels and joins the runner's current generation.
func (m *ExportLogsModel) CancelAndWait() { m.runner.CancelAndWait() }

func parseExportTime(value string, location *time.Location) (time.Time, error) {
	parsed, err := time.ParseInLocation(exportTimeLayout, value, location)
	if err != nil || parsed.Format(exportTimeLayout) != value {
		return time.Time{}, errors.New("invalid export time")
	}
	return parsed, nil
}
func exportRangeLabel(kind logging.RangeKind) string {
	switch kind {
	case logging.RangeLast60Minutes:
		return ExportRange60Minutes
	case logging.RangeBetween:
		return ExportRangeBetween
	case logging.RangeAll:
		return ExportRangeAll
	default:
		return ExportRange24Hours
	}
}
func exportErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrLocalLogStorageUnavailable):
		return "Local log storage unavailable"
	case errors.Is(err, context.Canceled):
		return ExportCancelled
	case errors.Is(err, logging.ErrNoLogLines):
		return ExportNoLogLines
	case errors.Is(err, logging.ErrExportTargetExists):
		return ExportTargetExists
	case errors.Is(err, logging.ErrInvalidExportRequest), errors.Is(err, logging.ErrExportTargetChanged):
		return ExportTargetInvalid
	default:
		return ExportFailed
	}
}

const exportTimeLayout = "2006-01-02 15:04"

const exportWarningNotice = "Export cleanup or durability could not be confirmed.\nTemporary export data may remain."

var errExportPanicked = errors.New("log export failed")
