package update

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestReplacementPreview_InstalledLabelIsDisplayOnly(t *testing.T) {
	c, s := replacementFixture()
	s.Targets[0].Version = ""
	before := mustReplacementPreview(t, c, s)
	s.Targets[0].UnrecognizedVersion = "local"
	after := mustReplacementPreview(t, c, s)
	if after.Snapshot.Targets[0].UnrecognizedVersion != "local" || after.Risk != ReplacementUnknown {
		t.Fatal("installed build label or unknown comparison was lost")
	}
	if before.ID != after.ID {
		t.Fatal("display evidence changed the confirmation wire")
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("display evidence entered serialized preview")
	}
	if ReplacementWarning(before) != ReplacementWarning(after) {
		t.Fatal("CLI warning changed")
	}
	var beforeError, afterError protocol.APIError
	if !errors.As(ReplacementConfirmationError(before), &beforeError) || !errors.As(ReplacementConfirmationError(after), &afterError) {
		t.Fatal("missing API error")
	}
	beforeJSON, err = json.Marshal(beforeError)
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, err = json.Marshal(afterError)
	if err != nil {
		t.Fatal(err)
	}
	if string(beforeJSON) != string(afterJSON) || strings.Contains(string(afterJSON), "local") {
		t.Fatal("public JSON changed")
	}
	if err := RecheckReplacement(after, c, s); err != nil {
		t.Fatal(err)
	}
	s.Targets[0].SHA256 = strings.Repeat("c", 64)
	if err := RecheckReplacement(after, c, s); err == nil {
		t.Fatal("display evidence bypassed identity binding")
	}
}

func TestReplacementPreview_FiltersInstalledDisplayEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, version, label, want string
		exists                     bool
	}{
		{"spaces", "", " local ", "local", true},
		{"unsafe", "", "private\nvalue", "", true},
		{"oversized", "", strings.Repeat("a", 129), "", true},
		{"known", "v1.2.3", "local", "", true},
		{"absent", "", "local", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, s := replacementFixture()
			s.Targets[0].Version, s.Targets[0].UnrecognizedVersion, s.Targets[0].Exists = tc.version, tc.label, tc.exists
			p := mustReplacementPreview(t, c, s)
			if got := p.Snapshot.Targets[0].UnrecognizedVersion; got != tc.want {
				t.Fatalf("label=%q want %q", got, tc.want)
			}
		})
	}
}

func TestReplacementPreview_RejectsConflictingInstalledLabelsForSamePath(t *testing.T) {
	c, s := replacementFixture()
	s.Targets[0].Version, s.Targets[0].UnrecognizedVersion = "", "local"
	other := s.Targets[0]
	other.Roles = []string{"binary"}
	s.Targets = append(s.Targets, other)
	if p := mustReplacementPreview(t, c, s); len(p.Snapshot.Targets) != 1 || p.Snapshot.Targets[0].UnrecognizedVersion != "local" {
		t.Fatal("canonical alias lost display evidence")
	}
	s.Targets[1].UnrecognizedVersion = "other"
	if _, err := NewReplacementPreview(c, s); err == nil {
		t.Fatal("conflicting display observations accepted")
	}
	s.Targets[1].Path += "-other"
	s.Targets[1].UnrecognizedVersion = "local"
	if p := mustReplacementPreview(t, c, s); len(p.Snapshot.Targets) != 2 {
		t.Fatal("same version at different paths was merged")
	}
}
