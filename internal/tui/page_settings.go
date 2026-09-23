package tui

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	connectionspage "github.com/mihari-proxy/mihari/internal/tui/pages/connections"
	proxypage "github.com/mihari-proxy/mihari/internal/tui/pages/proxies"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type pagePreferencesClient interface {
	UpdateTUIPreferences(context.Context, protocol.UpdateTUIPreferencesRequest) (protocol.TUIPreferences, error)
}

type settingsFocus struct{ section, field int } // field -1 is a section header

type pageSettingsDialog struct {
	pages           []ui.PageID
	expanded        map[ui.PageID]bool
	directory       int
	area            int // directory, configuration, Cancel, Save
	focus           settingsFocus
	scroll          int
	original, draft protocol.ProxyPreferences
	saving          bool
	showDone        bool
	saveClock       time.Time
	err             string
}

func newPageSettings(page ui.PageID, prefs protocol.TUIPreferences) *pageSettingsDialog {
	pages := ui.RailPages()
	expanded := make(map[ui.PageID]bool, len(pages))
	for _, id := range pages {
		expanded[id] = true
	}
	d := &pageSettingsDialog{pages: pages, expanded: expanded, area: 1,
		original: prefs.EffectiveProxies(), draft: prefs.EffectiveProxies()}
	for i, id := range d.pages {
		if id == page {
			d.directory = i
			break
		}
	}
	d.focus = settingsFocus{d.directory, -1}
	return d
}

func (d *pageSettingsDialog) rows() []settingsFocus {
	var rows []settingsFocus
	for i, id := range d.pages {
		rows = append(rows, settingsFocus{i, -1})
		if id == ui.PageProxies && d.expanded[id] {
			rows = append(rows, settingsFocus{i, 0}, settingsFocus{i, 1}, settingsFocus{i, 2})
		}
	}
	return rows
}

func (d *pageSettingsDialog) key(key string) ModalAction {
	if d.saving {
		return ModalNone
	}
	switch key {
	case "esc":
		return ModalClose
	case "ctrl+s":
		return ModalConfirm
	case "]", "[":
		for _, id := range d.pages {
			d.expanded[id] = key == "]"
		}
		if key == "[" && d.focus.field >= 0 {
			d.focus.field = -1
		}
		return ModalNone
	case "tab", "shift+tab":
		delta := 1
		if key == "shift+tab" {
			delta = 3
		}
		d.area = (d.area + delta) % 4
		if d.area == 1 {
			d.directory = d.focus.section
		}
		return ModalNone
	}
	switch d.area {
	case 0:
		switch key {
		case "up":
			d.directory = max(0, d.directory-1)
		case "down":
			d.directory = min(len(d.pages)-1, d.directory+1)
		case "enter":
			d.expanded[d.pages[d.directory]] = true
			d.focus, d.area = settingsFocus{d.directory, -1}, 1
		}
	case 1:
		switch key {
		case "left", "right":
			if d.focus.field == 2 {
				delta := 1
				if key == "left" {
					delta = -1
				}
				next := max(1, min(protocol.MaxLatencyTestConcurrency, d.draft.LatencyTestConcurrency+delta))
				if next != d.draft.LatencyTestConcurrency {
					d.draft.LatencyTestConcurrency = next
					d.clearSaveFeedback()
				}
			}
		case "up", "down", "pgup", "pgdown":
			rows := d.rows()
			for i, row := range rows {
				if row != d.focus {
					continue
				}
				step := 1
				if key == "pgup" || key == "pgdown" {
					step = 5
				}
				if key == "up" || key == "pgup" {
					step = -step
				}
				d.focus = rows[max(0, min(len(rows)-1, i+step))]
				d.directory = d.focus.section
				break
			}
		case "enter", "space":
			if d.focus.field < 0 {
				id := d.pages[d.focus.section]
				d.expanded[id] = !d.expanded[id]
			} else if d.focus.field == 0 {
				d.draft.ExtraLatency = !d.draft.ExtraLatency
				d.clearSaveFeedback()
			} else if d.focus.field == 1 {
				d.draft.AutoLatencyTest = !d.draft.AutoLatencyTest
				d.clearSaveFeedback()
			}
		}
	case 2, 3:
		switch key {
		case "left", "right":
			d.area = 5 - d.area
		case "enter":
			if d.area == 2 {
				return ModalClose
			}
			return ModalConfirm
		}
	}
	return ModalNone
}

func (d *pageSettingsDialog) view(width, height int) string {
	theme := ui.DefaultTheme()
	if Classify(width, height) == ui.TooSmall {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, theme.Muted.Render(ui.ResizeRequired))
	}
	boxWidth := min(88, width-4)
	inner := boxWidth - 6
	leftWidth := 14
	rightWidth := inner - leftWidth - 3
	hint := wrapPageSettingsHint(pageSettingsHint(d.area == 1 && d.focus.field == 2), inner)
	rowsHeight := max(1, min(19, height-10-strings.Count(hint, "\n")))
	var right []string
	focusLine := 0
	for i, id := range d.pages {
		marker := "▸ "
		if d.expanded[id] {
			marker = "▾ "
		}
		line := "  " + marker + ui.PageLabel(id)
		if d.focus == (settingsFocus{i, -1}) {
			focusLine = len(right)
			if d.area == 1 {
				line = theme.RowFocus.Render("› " + marker + ui.PageLabel(id))
			}
		}
		if d.expanded[id] {
			line += theme.Muted.Render(" " + strings.Repeat("─", max(0, rightWidth-lipgloss.Width(line)-1)))
		}
		right = append(right, ui.TruncateVisible(line, rightWidth))
		if d.expanded[id] {
			if id == ui.PageProxies {
				for field, label := range []string{"Extra latency display", "Automatic latency test", "Test concurrency"} {
					checked := d.draft.ExtraLatency
					if field == 1 {
						checked = d.draft.AutoLatencyTest
					}
					check := "[ ]"
					if checked {
						check = "[x]"
					}
					if field == 2 {
						check = fmt.Sprintf("< %d >", d.draft.LatencyTestConcurrency)
					}
					labelWidth := rightWidth - 6 - lipgloss.Width(check)
					text := "    " + ui.PadCell(ui.TruncateVisible(label, labelWidth), labelWidth, ui.AlignLeft) + "  " + check
					if d.focus == (settingsFocus{i, field}) {
						focusLine = len(right)
						if d.area == 1 {
							text = theme.RowFocus.Render(text)
						}
					}
					right = append(right, text)
				}
			} else {
				right = append(right, theme.Muted.Render(ui.TruncateVisible("    No settings available yet", rightWidth)))
			}
		}
	}
	d.scroll = ui.EnsureLineVisible(d.scroll, rowsHeight, len(right), focusLine, focusLine+1)
	right = ui.SliceLines(right, d.scroll, rowsHeight)
	left := []string{theme.Muted.Render("Sections"), ""}
	for i, id := range d.pages {
		label := "  " + ui.PageLabel(id)
		if i == d.directory {
			label = "● " + ui.PageLabel(id)
			if d.area == 0 {
				label = theme.RowFocus.Render(label)
			} else {
				label = theme.Title.Render(label)
			}
		}
		left = append(left, label)
	}
	leftStart := max(0, d.directory+3-rowsHeight)
	if rowsHeight >= len(left) {
		leftStart = 0
	}
	left = ui.SliceLines(left, leftStart, rowsHeight)
	var lines []string
	for i := 0; i < rowsHeight; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		lines = append(lines, ui.PadCell(l, leftWidth, ui.AlignLeft)+theme.Muted.Render(" │ ")+ui.PadCell(r, rightWidth, ui.AlignLeft))
	}
	cancel, save := "[Cancel]", "[Save]"
	if d.area == 2 {
		cancel = theme.RowFocus.Render(cancel)
	}
	if d.area == 3 {
		save = theme.RowFocus.Render(save)
	}
	buttons := cancel + "  " + save + " " + theme.Muted.Render("Ctrl+S")
	status := d.saveStatus(theme, inner)
	body := theme.Title.Render("Page Settings") + "\n\n" + strings.Join(lines, "\n") + "\n" + status + "\n" +
		strings.Repeat(" ", max(0, inner-lipgloss.Width(buttons))) + buttons + "\n" + theme.Muted.Render(hint)
	box := theme.Dialog.Width(boxWidth).Render(body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func pageSettingsHint(adjust bool) string {
	if adjust {
		return "←/→ adjust (1–50)  Tab area  ] Expand all  [ Collapse all  Esc cancel"
	}
	return "Tab area  ↑/↓ move  Enter select  ] Expand all  [ Collapse all  Esc cancel"
}

// wrapPageSettingsHint keeps Expand all and Collapse all together on the next
// line when the minimum 72-column dialog cannot hold the full footer.
func wrapPageSettingsHint(hint string, width int) string {
	if width < 1 || lipgloss.Width(hint) <= width {
		return hint
	}
	const marker = "] Expand all"
	index := strings.Index(hint, marker)
	if index <= 0 {
		return hint
	}
	head := strings.TrimRight(hint[:index], " ")
	tail := hint[index:]
	if lipgloss.Width(head) <= width && lipgloss.Width(tail) <= width {
		return head + "\n" + tail
	}
	return hint
}

func (d *pageSettingsDialog) clearSaveFeedback() {
	d.showDone = false
	d.err = ""
}

func (d *pageSettingsDialog) saveStatus(theme ui.Theme, width int) string {
	switch {
	case d.saving:
		clock := d.saveClock
		if clock.IsZero() {
			clock = time.Unix(0, 0)
		}
		return ui.RenderStatusChip(theme, ui.StatusChipPending, ui.SpinnerLabel(clock, "Saving"))
	case d.err != "":
		badge := ui.RenderStatusChip(theme, ui.StatusChipFailed, ui.FailedLabel)
		room := width - lipgloss.Width(badge) - 2
		if room < 1 {
			return badge
		}
		detail := ui.TruncateVisible(d.err, room)
		if detail == "" {
			return badge
		}
		return badge + "  " + theme.Danger.Render(detail)
	case d.showDone:
		return ui.RenderStatusChip(theme, ui.StatusChipDone, ui.DoneLabel)
	default:
		return ""
	}
}

const pageSettingsSpinInterval = 100 * time.Millisecond

type pageSettingsSpinMsg struct {
	dialog *pageSettingsDialog
	at     time.Time
}

func (d *pageSettingsDialog) spinCmd() tea.Cmd {
	dialog := d
	return tea.Tick(pageSettingsSpinInterval, func(at time.Time) tea.Msg {
		return pageSettingsSpinMsg{dialog: dialog, at: at}
	})
}

type pageSettingsSavedMsg struct {
	epoch       uint64
	generation  uint64
	dialog      *pageSettingsDialog
	preferences protocol.TUIPreferences
	err         error
}

func (m pageSettingsSavedMsg) Err() error                        { return m.err }
func (m pageSettingsSavedMsg) Warnings() protocol.WarningOutcome { return m.preferences.WarningOutcome }

var settingsOperationID atomic.Uint64

func (model *Model) savePageSettings() tea.Cmd {
	d := model.pageSettings
	if d == nil || d.saving {
		return nil
	}
	if d.draft == d.original {
		d.showDone = true
		d.err = ""
		return nil
	}
	if model.preferencesClient == nil || !model.preferencesLoaded {
		d.showDone = false
		d.err = "Page settings unavailable; wait for the daemon"
		return nil
	}
	if model.preferences.EffectiveProxies() != d.original {
		d.showDone = false
		d.err = "Page settings changed elsewhere; reopen to review"
		return nil
	}
	d.saving, d.showDone, d.err = true, false, ""
	d.saveClock = time.Now()
	draft, revision := d.draft, model.preferences.Revision
	epoch, generation := model.statusEpoch, model.preferencesGeneration
	client := model.preferencesClient
	parent := model.pageCtx
	if parent == nil {
		parent = context.Background()
	}
	id := fmt.Sprintf("tui-settings-%d-%d", time.Now().UnixNano(), settingsOperationID.Add(1))
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		prefs, err := client.UpdateTUIPreferences(ctx, protocol.UpdateTUIPreferencesRequest{OperationID: id, IfRevision: &revision, Proxies: &draft})
		return pageSettingsSavedMsg{dialog: d, preferences: prefs, err: err, epoch: epoch, generation: generation}
	}
}

func (model *Model) applyPreferences(prefs protocol.TUIPreferences) {
	if model.preferencesLoaded && prefs.Revision < model.preferences.Revision {
		return
	}
	model.preferences, model.preferencesLoaded = prefs, true
	if page, ok := model.pages[ui.PageConnections].(*connectionspage.Model); ok {
		page.SetPreferences(prefs)
	}
	if page, ok := model.pages[ui.PageProxies].(*proxypage.Model); ok {
		page.SetPreferences(prefs)
	}
}

func (model *Model) updatePageSettings(message tea.Msg) (tea.Cmd, bool) {
	if saved, ok := message.(pageSettingsSavedMsg); ok {
		if saved.epoch != model.statusEpoch || saved.generation != model.preferencesGeneration {
			if model.pageSettings == saved.dialog {
				saved.dialog.saving = false
				saved.dialog.showDone = false
				saved.dialog.err = "Daemon connection changed; reopen settings to review"
			}
			return nil, true
		}
		if saved.err == nil {
			model.applyPreferences(saved.preferences)
		}
		if model.pageSettings == saved.dialog {
			saved.dialog.saving = false
			if saved.err == nil {
				saved.dialog.showDone = true
				saved.dialog.err = ""
				saved.dialog.original = saved.preferences.EffectiveProxies()
				saved.dialog.draft = saved.dialog.original
			} else {
				saved.dialog.showDone = false
				saved.dialog.err = diagnosticSingleLine(saved.err.Error())
			}
		}
		return nil, true
	}
	if spin, ok := message.(pageSettingsSpinMsg); ok {
		if model.pageSettings != spin.dialog || !spin.dialog.saving {
			return nil, true
		}
		spin.dialog.saveClock = spin.at
		return spin.dialog.spinCmd(), true
	}
	if model.pageSettings == nil {
		return nil, false
	}
	if key, ok := message.(tea.KeyPressMsg); ok {
		if Classify(model.width, model.height) == ui.TooSmall && key.String() != "esc" {
			return nil, true
		}
		switch model.pageSettings.key(key.String()) {
		case ModalClose:
			model.pageSettings = nil
		case ModalConfirm:
			cmd := model.savePageSettings()
			if model.pageSettings != nil && model.pageSettings.saving {
				return tea.Batch(cmd, model.pageSettings.spinCmd()), true
			}
			return cmd, true
		}
		return nil, true
	}
	return nil, false
}
