package system

import (
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"github.com/mihari-proxy/mihari/internal/update"
)

func TestUpdateConfirmation_InstalledVersionsAndRisk(t *testing.T) {
	for _, tc := range []struct {
		name, version, label, want, copy string
		exists, service                  bool
	}{
		{"custom service", "", "dev-setup-6a47df6-dirty-20260913.2", "Unknown[dev-setup-6a47df6-dirty-20260913.2]", ui.UpdateUnknownBuild, true, true},
		{"missing version", "", "", "Unknown", ui.UpdateUnknownVersion, true, false},
		{"downgrade", "v2.0.0", "", "v2.0.0", ui.UpdateDowngradeCompatibility, true, true},
		{"upgrade", "v0.9.3", "", "v0.9.3", "", true, false},
		{"absent", "", "", "Not installed", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, f := replacementFixture(t)
			c, s := f.prepared.Preview.Candidate, f.prepared.Preview.Snapshot
			s.Targets[0].Version, s.Targets[0].UnrecognizedVersion, s.Targets[0].Exists = tc.version, tc.label, tc.exists
			if tc.service {
				s.Targets[0].Roles = []string{"service"}
			}
			p, err := update.NewReplacementPreview(c, s)
			if err != nil {
				t.Fatal(err)
			}
			f.prepared.Preview = p
			intent := m.confirmPreparedMihariUpdate(f.prepared)().(ui.ActionIntentMsg)
			got := intent.MihariUpdate
			if got == nil {
				t.Fatal("missing structured update confirmation")
			}
			if len(got.Installed) != 1 || got.Installed[0].Version != tc.want || got.Compatibility != tc.copy || got.TargetVersion != c.Version {
				t.Fatalf("wrong update confirmation: %+v", got)
			}
			wantAfter := ui.UpdateAfterStandalone
			if tc.service {
				wantAfter = ui.UpdateAfterService
			}
			if got.AfterConfirmation != wantAfter {
				t.Fatal("incorrect service impact")
			}
			if tc.label != "" && (strings.Contains(intent.Object, tc.label) || strings.Contains(intent.Impact, tc.label)) {
				t.Fatal("TUI label leaked into generic action copy")
			}
		})
	}
}

func TestUpdateConfirmation_DoesNotMergeCopiesOrGuessMissingVersion(t *testing.T) {
	m, f := replacementFixture(t)
	c, s := f.prepared.Preview.Candidate, f.prepared.Preview.Snapshot
	s.Targets[0].Version, s.Targets[0].UnrecognizedVersion = "", "local"
	other := s.Targets[0]
	other.Path += "-second"
	other.Roles = []string{"service"}
	s.Targets = append(s.Targets, other)
	for _, missing := range []bool{false, true} {
		if missing {
			s.Targets[1].UnrecognizedVersion = ""
		}
		p, err := update.NewReplacementPreview(c, s)
		if err != nil {
			t.Fatal(err)
		}
		f.prepared.Preview = p
		got := m.confirmPreparedMihariUpdate(f.prepared)().(ui.ActionIntentMsg).MihariUpdate
		if got == nil || len(got.Installed) != 2 {
			t.Fatal("distinct copies were not displayed independently")
		}
		if got.Installed[0].Version != "Unknown[local]" || got.Installed[0].Role != "Binary" || got.Installed[1].Role != "Service" {
			t.Fatal("wrong copy labels")
		}
		if missing {
			if got.Installed[1].Version != "Unknown" || got.Compatibility != ui.UpdateUnknownVersion {
				t.Fatal("missing version was guessed or misdescribed")
			}
		} else if got.Installed[1].Version != "Unknown[local]" {
			t.Fatal("same local version not preserved")
		}
	}
}
