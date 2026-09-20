package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func (m *Model) showDuplicateNames() {
	if len(m.proxyNamesPending) == 0 || m.modal != nil || m.pageSettings != nil || (m.installation != nil && m.installation.visible) || (m.exportLogs != nil && !m.exportLogs.Closed()) {
		return
	}
	const limit = 8
	names := make([]string, 0, min(limit, len(m.proxyNamesPending)))
	for _, name := range m.proxyNamesPending[:min(limit, len(m.proxyNamesPending))] {
		name = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, ansi.Strip(name))
		names = append(names, ui.TruncateVisible(ui.DisplayProxyName(name), 48))
	}
	body := "The loaded nodes contain duplicate names. Mihari displays and tests one entry per name. The tested node may differ from the node selected by a proxy group. The service can continue running.\n\n" + strings.Join(names, "\n")
	if extra := len(m.proxyNamesPending) - limit; extra > 0 {
		body += fmt.Sprintf("\nAnd %d more", extra)
	}
	m.modal = NewDetail("Duplicate node names detected", body+"\n\nPress Esc to continue")
	m.proxyNamesPending = nil
}
