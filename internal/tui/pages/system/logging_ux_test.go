package system

import (
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

func TestLogging_SectionFollowsNetwork(t *testing.T) {
	m, _ := loggingModel("info", 4)
	rows := m.rows()
	network, logging, about := -1, -1, -1
	for i, r := range rows {
		switch r.section {
		case ui.NetworkSectionTitle:
			network = i
		case ui.LoggingSectionTitle:
			if logging < 0 {
				logging = i
			}
		case ui.AboutSectionTitle:
			if about < 0 {
				about = i
			}
		}
	}
	if !(network < logging && logging < about) {
		t.Fatalf("section order network=%d logging=%d about=%d", network, logging, about)
	}
}

func TestLogging_OutcomePreservesValue(t *testing.T) {
	for _, id := range []string{rowLogLevel, rowLogMaxSize, rowLogMaxFiles, rowLogDirectory} {
		m, _ := loggingModel("info", 4)
		m.focusID = id
		want := systemRowByID(m, id).value
		m.markRowOutcome(id, true, "")
		lines, _, _ := m.buildSectionContent()
		found := false
		for _, line := range lines {
			if strings.Contains(line, ui.DoneLabel) {
				found = strings.Contains(line, want)
			}
		}
		if !found {
			t.Fatalf("%s outcome hid value %q", id, want)
		}
	}
}
