package system

import (
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

// The textarea sanitizer removes every U+FFFD, including valid UTF-8. Use a
// single printable cell internally and restore the glyph when rendering.
const yamlReplacementCell = "\ufdd0"

// The textarea represents newlines and tab stops visually. Keep the original
// source bytes for untouched text, including CRLF and literal tabs in scalars.
func yamlEditorToken(raw string) (display string, size int) {
	switch raw[0] {
	case '\r':
		if strings.HasPrefix(raw, "\r\n") {
			return "\n", 2
		}
		return "\n", 1
	case '\t':
		return "    ", 1
	}
	r, n := utf8.DecodeRuneInString(raw)
	if r == utf8.RuneError && n == 1 {
		return `\ufffd`, n
	}
	if r == utf8.RuneError {
		return yamlReplacementCell, n
	}
	if raw[:n] == yamlReplacementCell {
		return `\ufdd0`, n
	}
	if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
		return diagnostics.EscapeTerminal(raw[:n]), n
	}
	return raw[:n], n
}

func yamlEditorDisplay(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for len(raw) > 0 {
		v, n := yamlEditorToken(raw)
		b.WriteString(v)
		raw = raw[n:]
	}
	return b.String()
}

// Split at a byte offset in the displayed text. Editing inside an expanded
// tab replaces only that tab with spaces.
func splitYAMLDisplay(raw string, at int) (string, string) {
	for i := 0; i < len(raw); {
		if at == 0 {
			return raw[:i], raw[i:]
		}
		v, n := yamlEditorToken(raw[i:])
		if at < len(v) {
			return raw[:i] + v[:at], v[at:] + raw[i+n:]
		}
		i += n
		at -= len(v)
	}
	return raw, ""
}

func (m *Model) updateYAMLInput(msg tea.Msg) tea.Cmd {
	o := &m.overrides
	if key, ok := msg.(tea.KeyPressMsg); ok && ui.IsPasteKey(key) {
		return ui.ReadClipboard
	}
	if clipboard, ok := msg.(ui.ClipboardPasteMsg); ok {
		if clipboard.Err() != nil {
			o.notice = clipboard.Err().Error()
			return nil
		}
		msg = tea.PasteMsg{Content: clipboard.Text}
	}
	before := o.input.Value()
	if p, ok := msg.(tea.PasteMsg); ok {
		// Bubble Tea reports the column in runes within the current logical line.
		at := 0
		for range o.input.Line() {
			at += strings.IndexByte(before[at:], '\n') + 1
		}
		line := before[at:]
		for range o.input.Column() {
			_, n := utf8.DecodeRuneInString(line)
			at += n
			line = line[n:]
		}
		left, right := splitYAMLDisplay(o.yamlRaw, at)
		o.yamlRaw = left + p.Content + right
		o.input.InsertString(yamlEditorDisplay(p.Content))
		return m.refreshOverridePreview()
	}
	if key, ok := msg.(tea.KeyPressMsg); ok && strings.ContainsAny(key.Text, "�"+yamlReplacementCell) {
		return m.updateYAMLInput(tea.PasteMsg{Content: key.Text})
	}
	var cmd tea.Cmd
	o.input, cmd = o.input.Update(msg)
	after := o.input.Value()
	if before == after {
		return cmd
	}
	start := 0
	for start < len(before) && start < len(after) && before[start] == after[start] {
		start++
	}
	a, b := len(before), len(after)
	for a > start && b > start && before[a-1] == after[b-1] {
		a--
		b--
	}
	left, _ := splitYAMLDisplay(o.yamlRaw, start)
	_, right := splitYAMLDisplay(o.yamlRaw, a)
	insertion := after[start:b]
	if strings.Contains(o.yamlRaw, "\r\n") {
		insertion = strings.ReplaceAll(insertion, "\n", "\r\n")
	}
	o.yamlRaw = left + insertion + right
	return tea.Batch(cmd, m.refreshOverridePreview())
}
