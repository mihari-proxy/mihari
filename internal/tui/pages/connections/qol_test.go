package connections

import (
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestColumns_ChainGetsWidthAndSurvivesBeforeRule(t *testing.T) {
	m := New(nil, nil)
	m.SetPreferences(protocol.TUIPreferences{ConnectionsColumns: []string{"host", "source", "destination", "chain", "rule", "traffic"}})
	m.SetSize(150, 24)
	cols, widths := m.keptConnectionColumns()
	sizes := map[string]int{}
	for i, c := range cols {
		sizes[c.ID] = widths[i]
	}
	if sizes["chain"] <= sizes["host"] || sizes["traffic"] != 15 {
		t.Fatalf("widths=%v", sizes)
	}
	m.SetSize(58, 24)
	cols, _ = m.keptConnectionColumns()
	ids := []string{}
	for _, c := range cols {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "host,chain,traffic" {
		t.Fatalf("narrow columns=%v", ids)
	}
}

func TestTraffic_CompactRatesStayCompleteAndAligned(t *testing.T) {
	m := New(nil, nil)
	m.SetPreferences(protocol.TUIPreferences{ConnectionsColumns: []string{"host", "traffic"}})
	m.SetSize(58, 24)
	width := -1
	for _, tc := range []struct {
		speed int64
		want  string
	}{{-1, "0"}, {0, "0"}, {1024, "1K"}, {1610612736, "1.5G"}, {1048472678, "999.9M"}, {1048575, "1M"}, {1<<63 - 1, "8E"}} {
		row := stripConnANSI(m.renderConnection(protocol.Connection{UploadSpeed: tc.speed, DownloadSpeed: tc.speed}, false)[0])
		if width < 0 {
			width = len([]rune(row))
		}
		if len([]rune(row)) != width {
			t.Fatalf("rate changed row width: %q", row)
		}
		if !strings.Contains(row, "↑"+tc.want) || !strings.Contains(row, "↓"+tc.want) || strings.Contains(row, "…") {
			t.Fatalf("speed=%d row=%q", tc.speed, row)
		}
	}
	if !strings.Contains(m.HelpContent(), "powers of 1024") {
		t.Fatal("compact units missing from help")
	}
}
